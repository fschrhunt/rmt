package hosts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/fschrhunt/rmt/src/extensions"
	"github.com/fschrhunt/rmt/src/network"
)

// openProgram connects to a program's native shared backend without taking ownership of it.
func openProgram(ctx context.Context, name, home, directory string, registry *extensions.Registry, run func(string, []string) (string, string, int, error)) (*programBackend, error) {
	switch name {
	case "opencode":
		return openCodeService(ctx)
	case "codex":
		return codexService(ctx, home)
	default:
		if registry == nil || !registry.Has(name) {
			return nil, errors.New("unsupported program")
		}
		definition, err := registry.Host(ctx, name, directory, run)
		if err != nil {
			return nil, err
		}
		b := &programBackend{name: name, authorization: definition["authorization"]}
		if socket := definition["socket"]; socket != "" {
			if definition["url"] != "" {
				return nil, errors.New("extension must choose a socket or URL")
			}
			path, err := privateSocket(socket)
			if err != nil {
				return nil, err
			}
			b.target = &url.URL{Scheme: "http", Host: "localhost"}
			b.transport = &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", path)
			}}
		} else {
			b.target, err = localServiceURL(definition["url"])
			if err != nil {
				return nil, fmt.Errorf("extension backend: %w", err)
			}
			b.transport = &http.Transport{Proxy: nil, IdleConnTimeout: 30 * time.Second}
		}
		return b, nil
	}
}

// serveProgram attaches a shell to a native backend; detaching never stops that backend.
func (m *shellSessions) serveProgram(w http.ResponseWriter, r *http.Request) {
	parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/program/"), "/", 4)
	if len(parts) < 3 {
		http.NotFound(w, r)
		return
	}
	token, name, action := parts[0], parts[1], parts[2]
	m.mu.Lock()
	s := m.sessions[token]
	m.mu.Unlock()
	if s == nil || s.owner != network.DeviceKey(r.Context()) {
		http.Error(w, "session unavailable", http.StatusNotFound)
		return
	}
	if name != "opencode" && name != "codex" && (m.extensions == nil || !m.extensions.Has(name)) {
		http.NotFound(w, r)
		return
	}
	if !m.enabled(name) {
		http.Error(w, "program disabled on host", http.StatusServiceUnavailable)
		return
	}
	if problem := m.problems[name]; problem != "" {
		http.Error(w, problem, http.StatusServiceUnavailable)
		return
	}
	switch action {
	case "prepare":
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var request struct {
			Command string `json:"command"`
		}
		if json.NewDecoder(io.LimitReader(r.Body, 128<<10)).Decode(&request) != nil || len(request.Command) > 64<<10 || request.Command == "" {
			http.Error(w, "invalid program command", http.StatusBadRequest)
			return
		}
		s.mu.Lock()
		if s.closed || !s.attached || s.starting || s.running {
			s.mu.Unlock()
			http.Error(w, "session is busy or closed", http.StatusConflict)
			return
		}
		s.starting = true
		s.mu.Unlock()
		result, err := s.sh.prepareProgram(request.Command, name)
		if err == nil && m.extensions != nil {
			var messages strings.Builder
			run := func(directory string, args []string) (string, string, int, error) {
				out, stderr, status, err := s.sh.runExtension(directory, args)
				messages.WriteString(stderr)
				return out, stderr, status, err
			}
			result.Directory, result.Args, err = m.extensions.Prepare(r.Context(), name, result.Directory, result.Args, run)
			result.Output += messages.String()
			if err == nil && !filepath.IsAbs(result.Directory) {
				err = errors.New("extension returned an invalid directory")
			}
		}
		s.mu.Lock()
		s.starting = false
		closed := s.closed || !s.attached
		if err == nil && !closed {
			s.preparedDirectory = result.Directory
			s.preparedName = name
		}
		s.mu.Unlock()
		if closed {
			http.Error(w, "session closed", http.StatusConflict)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(struct{ Output, Error string }{result.Output, err.Error()})
			return
		}
		_ = json.NewEncoder(w).Encode(result)
	case "start":
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		s.mu.Lock()
		if s.closed || !s.attached || s.starting || s.running {
			s.mu.Unlock()
			http.Error(w, "program is starting or session is closed", http.StatusConflict)
			return
		}
		// A lost stop request may leave an old attachment; replacing it never stops the service.
		if s.program != nil {
			if closer, ok := s.program.transport.(interface{ CloseIdleConnections() }); ok {
				closer.CloseIdleConnections()
			}
			s.program = nil
		}
		s.starting = true
		directory := s.directory
		if s.preparedName == name && s.preparedDirectory != "" {
			directory = s.preparedDirectory
		}
		s.mu.Unlock()
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		run := func(directory string, args []string) (string, string, int, error) {
			return runHostCommand(ctx, directory, args)
		}
		b, err := openProgram(ctx, name, m.home, directory, m.extensions, run)
		s.mu.Lock()
		s.starting = false
		closed := s.closed || !s.attached
		if err == nil && !closed {
			s.program = b
		}
		s.mu.Unlock()
		if err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		if closed {
			http.Error(w, "session closed", http.StatusConflict)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case "proxy":
		s.mu.Lock()
		b := s.program
		if s.closed || b == nil || b.name != name {
			s.mu.Unlock()
			http.Error(w, "program is not attached", http.StatusServiceUnavailable)
			return
		}
		s.proxying++
		s.mu.Unlock()
		defer func() {
			s.mu.Lock()
			s.proxying--
			s.mu.Unlock()
		}()
		prefix := "/program/" + token + "/" + name + "/proxy"
		http.StripPrefix(prefix, http.HandlerFunc(b.proxy)).ServeHTTP(w, r)
	default:
		http.NotFound(w, r)
	}
}
