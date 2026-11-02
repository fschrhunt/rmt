package hosts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/fschrhunt/rmt/src/network"
)

// programBackend describes a native user-managed service, never owned by rmt.
type programBackend struct {
	name      string
	target    *url.URL
	password  string
	transport http.RoundTripper
}

// nativeCommand invokes a native service command without logging its output or credentials.
func nativeCommand(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("%s %s failed: %w", name, strings.Join(args, " "), err)
	}
	if len(output) > 4096 {
		return "", errors.New("native service response too large")
	}
	return strings.TrimSpace(string(output)), nil
}

// startNative waits for a service command without killing it if the client disconnects.
func startNative(ctx context.Context, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// localServiceURL accepts a native HTTP service only when it stays on this machine.
func localServiceURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "http" || u.Hostname() == "" || u.Port() == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("OpenCode returned an invalid service address")
	}
	host := u.Hostname()
	ip := net.ParseIP(host)
	switch {
	case host == "localhost" || (ip != nil && ip.IsLoopback()):
	case ip != nil && ip.IsUnspecified():
		if ip.To4() != nil {
			host = "127.0.0.1"
		} else {
			host = "::1"
		}
	case ip != nil:
		addresses, err := net.InterfaceAddrs()
		if err != nil {
			return nil, errors.New("cannot verify OpenCode's local service address")
		}
		local := false
		for _, address := range addresses {
			if network, ok := address.(*net.IPNet); ok && network.IP.Equal(ip) {
				local = true
				break
			}
		}
		if !local {
			return nil, errors.New("OpenCode service address does not belong to this machine")
		}
	default:
		return nil, errors.New("OpenCode service must use a local IP address or localhost")
	}
	return &url.URL{Scheme: "http", Host: net.JoinHostPort(host, u.Port())}, nil
}

// openCodeService discovers or starts OpenCode's own shared service and authenticates locally.
func openCodeService(ctx context.Context) (*programBackend, error) {
	disabled, err := nativeCommand(ctx, "opencode", "service", "get", "disabled")
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(disabled) == "true" {
		return nil, errors.New("OpenCode's shared service is disabled in its native configuration")
	}
	status, err := nativeCommand(ctx, "opencode", "service", "status")
	target, statusErr := localServiceURL(status)
	if err == nil && statusErr != nil && !strings.EqualFold(strings.TrimSpace(status), "stopped") {
		return nil, statusErr
	}
	if err != nil || statusErr != nil {
		// Native services may inherit output descriptors, so do not capture their output.
		if err := startNative(ctx, "opencode", "service", "start"); err != nil {
			return nil, fmt.Errorf("cannot start OpenCode's native service: %w", err)
		}
		status, err = nativeCommand(ctx, "opencode", "service", "status")
		if err != nil {
			return nil, err
		}
		target, err = localServiceURL(status)
		if err != nil {
			return nil, err
		}
	}
	raw, err := nativeCommand(ctx, "opencode", "service", "get", "password")
	if err != nil {
		return nil, err
	}
	password := raw
	if strings.HasPrefix(raw, "\"") {
		if err := json.Unmarshal([]byte(raw), &password); err != nil {
			return nil, errors.New("invalid OpenCode service credential")
		}
	}
	if password == "" || password == "null" {
		return nil, errors.New("OpenCode service credential unavailable")
	}
	transport := &http.Transport{Proxy: nil, IdleConnTimeout: 30 * time.Second}
	b := &programBackend{name: "opencode", target: target, password: password, transport: transport}
	checkCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(checkCtx, http.MethodGet, target.String()+"/api/info", nil)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth("opencode", password)
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect refused") }}
	response, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cannot reach OpenCode's native service: %w", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("OpenCode's native service refused authentication: %s", response.Status)
	}
	return b, nil
}

// privateCodexSocket resolves Codex's socket link and verifies owner-only access.
func privateCodexSocket(path string) (string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", errors.New("Codex's native control socket is unavailable")
	}
	info, err := os.Stat(resolved)
	if err != nil || info.Mode()&os.ModeSocket == 0 {
		return "", errors.New("Codex's native control socket is unavailable")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Getuid()) || info.Mode().Perm()&0077 != 0 {
		return "", errors.New("Codex's native control socket must be private and owned by this user")
	}
	return resolved, nil
}

// codexService starts Codex's own managed daemon if needed and connects to its private socket.
func codexService(ctx context.Context, home string) (*programBackend, error) {
	codexHome := os.Getenv("CODEX_HOME")
	if codexHome == "" {
		codexHome = filepath.Join(home, ".codex")
	}
	if !filepath.IsAbs(codexHome) {
		return nil, errors.New("CODEX_HOME must be an absolute path")
	}
	if err := os.MkdirAll(codexHome, 0700); err != nil {
		return nil, fmt.Errorf("cannot prepare Codex's home directory: %w", err)
	}
	if err := startNative(ctx, "codex", "app-server", "daemon", "start"); err != nil {
		return nil, fmt.Errorf("cannot start Codex's native daemon: %w", err)
	}
	path := filepath.Join(codexHome, "app-server-control", "app-server-control.sock")
	path, err := privateCodexSocket(path)
	if err != nil {
		return nil, err
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", path)
	if err != nil {
		return nil, fmt.Errorf("cannot reach Codex's native daemon: %w", err)
	}
	conn.Close()
	transport := &http.Transport{IdleConnTimeout: 30 * time.Second, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", path)
	}}
	return &programBackend{name: "codex", target: &url.URL{Scheme: "http", Host: "localhost"}, transport: transport}, nil
}

// openProgram connects to a program's native shared backend without taking ownership of it.
func openProgram(ctx context.Context, name, home string) (*programBackend, error) {
	switch name {
	case "opencode":
		return openCodeService(ctx)
	case "codex":
		return codexService(ctx, home)
	default:
		return nil, errors.New("unsupported program")
	}
}

// proxy forwards authenticated requests to the program's native local backend.
func (b *programBackend) proxy(w http.ResponseWriter, r *http.Request) {
	if b.name == "opencode" {
		r.SetBasicAuth("opencode", b.password)
	} else {
		r.Header.Del("Authorization")
	}
	proxy := httputil.NewSingleHostReverseProxy(b.target)
	if b.transport != nil {
		proxy.Transport = b.transport
	}
	proxy.ServeHTTP(w, r)
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
	if name != "opencode" && name != "codex" {
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
	case "start":
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		s.mu.Lock()
		if s.closed || !s.attached || s.starting {
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
		s.mu.Unlock()
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		b, err := openProgram(ctx, name, m.home)
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
		s.mu.Unlock()
		if b == nil || b.name != name {
			http.Error(w, "program is not attached", http.StatusServiceUnavailable)
			return
		}
		prefix := "/program/" + token + "/" + name + "/proxy"
		http.StripPrefix(prefix, http.HandlerFunc(b.proxy)).ServeHTTP(w, r)
	default:
		http.NotFound(w, r)
	}
}
