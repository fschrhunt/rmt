package rmt

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/fschrhunt/rmt/src/network"
)

const (
	controlPort  = "47470"
	openCodePort = "47471"
	codexPort    = "47472"
)

type hostInfo struct {
	Workspace string   `json:"workspace,omitempty"`
	Dev       string   `json:"dev,omitempty"` // Compatibility with older host services.
	Agents    []string `json:"agents,omitempty"`
}

// workspace returns the configured host workspace, including for older services.
func (h hostInfo) workspace() string {
	if h.Workspace != "" {
		return h.Workspace
	}
	return h.Dev
}

// freePort chooses an unused loopback port for an agent backend.
func freePort() (string, error) {
	l, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		return "", e
	}
	p := fmt.Sprint(l.Addr().(*net.TCPAddr).Port)
	l.Close()
	return p, nil
}

// superviseAgent keeps one backend running without interrupting other agents.
func superviseAgent(ctx context.Context, name string, command func() *exec.Cmd) {
	delay := time.Second
	for ctx.Err() == nil {
		started := time.Now()
		cmd := command()
		cmd.Stdout = io.Discard
		cmd.Stderr = os.Stderr
		err := cmd.Run()
		if ctx.Err() != nil {
			return
		}
		fmt.Fprintf(os.Stderr, "rmt: %s stopped (%v); restarting in %s\n", name, err, delay)
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		if time.Since(started) > 30*time.Second {
			delay = time.Second
		} else if delay < 30*time.Second {
			delay *= 2
			if delay > 30*time.Second {
				delay = 30 * time.Second
			}
		}
	}
}

// bearerProxy forwards authenticated Tailscale requests to an authenticated backend.
func bearerProxy(port, token string, basic bool) http.Handler {
	p := httputil.NewSingleHostReverseProxy(&url.URL{Scheme: "http", Host: net.JoinHostPort("127.0.0.1", port)})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if basic {
			r.SetBasicAuth("opencode", token)
		} else {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		p.ServeHTTP(w, r)
	})
}

// runHost runs native backends from the configured workspace on the owner's tailnet.
func runHost() error {
	cfg, e := loadConfig()
	if e != nil {
		return e
	}
	s, tailnetErr := network.Tailscale()
	ip := network.IPv4(s.Self)
	legacy := tailnetErr == nil && ip != "" && s.Self.UserID != 0
	if !legacy && cfg.Listen == "" {
		return errors.New("configure paired HTTPS in 'rmt setup' or connect Tailscale")
	}
	workspace := cfg.Workspace
	if st, e := os.Stat(workspace); e != nil || !st.IsDir() {
		return fmt.Errorf("workspace %s is not a directory; create it or run 'rmt config set <workspace>'", workspace)
	}
	var listeners []net.Listener
	defer func() {
		for _, l := range listeners {
			l.Close()
		}
	}()
	if legacy {
		for _, p := range []string{controlPort, openCodePort, codexPort} {
			l, e := net.Listen("tcp", net.JoinHostPort(ip, p))
			if e != nil {
				return fmt.Errorf("listen on %s: %w", p, e)
			}
			listeners = append(listeners, l)
		}
	}
	var direct net.Listener
	var paired *network.Host
	if cfg.Listen != "" {
		path, _, err := configPath()
		if err != nil {
			return err
		}
		paired, err = network.NewHost(filepath.Dir(path))
		if err != nil {
			return fmt.Errorf("paired host identity: %w", err)
		}
		direct, err = net.Listen("tcp", cfg.Listen)
		if err != nil {
			return fmt.Errorf("listen on %s: %w", cfg.Listen, err)
		}
		defer direct.Close()
		if err := paired.StartLocal(); err != nil {
			return fmt.Errorf("paired host control: %w", err)
		}
		defer paired.Close()
	}
	op, e := freePort()
	if e != nil {
		return e
	}
	cp, e := freePort()
	if e != nil {
		return e
	}
	if op == cp {
		return errors.New("backend port collision; retry")
	}
	secret := make([]byte, 32)
	if _, e = rand.Read(secret); e != nil {
		return e
	}
	password := base64.RawURLEncoding.EncodeToString(secret)
	digest := sha256.Sum256([]byte(password))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	var agents sync.WaitGroup
	defer func() { stop(); agents.Wait() }()
	if cfg.enabled("opencode") {
		agents.Add(1)
		go func() {
			defer agents.Done()
			superviseAgent(ctx, "opencode", func() *exec.Cmd {
				c := exec.CommandContext(ctx, "opencode", "serve", "--hostname", "127.0.0.1", "--port", op)
				c.Dir = workspace
				c.Env = append(os.Environ(), "OPENCODE_PASSWORD="+password)
				return c
			})
		}()
	}
	if cfg.enabled("codex") {
		agents.Add(1)
		go func() {
			defer agents.Done()
			superviseAgent(ctx, "codex", func() *exec.Cmd {
				c := exec.CommandContext(ctx, "codex", "app-server", "--listen", "ws://127.0.0.1:"+cp, "--ws-auth", "capability-token", "--ws-token-sha256", hex.EncodeToString(digest[:]))
				c.Dir = workspace
				return c
			})
		}()
	}
	control := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/info" || r.Method != "GET" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(hostInfo{Workspace: workspace, Dev: workspace, Agents: cfg.Agents})
	})
	disabled := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "agent disabled on host", http.StatusServiceUnavailable)
	})
	var opHandler, codexHandler http.Handler = disabled, disabled
	if cfg.enabled("opencode") {
		opHandler = bearerProxy(op, password, true)
	}
	if cfg.enabled("codex") {
		codexHandler = bearerProxy(cp, password, false)
	}
	handlers := []http.Handler{control, opHandler, codexHandler}
	failed := make(chan error, 4)
	for i, l := range listeners {
		srv := &http.Server{Handler: network.SameUser(s.Self.UserID, handlers[i]), ReadHeaderTimeout: 5 * time.Second}
		go func(l net.Listener) { failed <- srv.Serve(l) }(l)
	}
	if paired != nil {
		mux := http.NewServeMux()
		mux.HandleFunc("/identity", paired.Pairing)
		mux.HandleFunc("/pair", paired.Pairing)
		mux.Handle("/info", paired.Auth(control))
		mux.Handle("/opencode/", paired.Auth(http.StripPrefix("/opencode", opHandler)))
		mux.Handle("/codex/", paired.Auth(http.StripPrefix("/codex", codexHandler)))
		srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
		go func() { failed <- srv.Serve(tls.NewListener(direct, paired.TLS())) }()
		fmt.Fprintf(os.Stderr, "rmt: paired HTTPS listening on %s\n", cfg.Listen)
	}
	name := s.Self.HostName
	if name == "" {
		name, _ = os.Hostname()
	}
	fmt.Fprintf(os.Stderr, "rmt: serving %s on %s\n", workspace, name)
	select {
	case <-ctx.Done():
		return nil
	case e := <-failed:
		return e
	}
}
