// rmt connects native agent terminals to workspaces on your Tailscale devices.
package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	controlPort  = "47470"
	openCodePort = "47471"
	codexPort    = "47472"
)

type status struct {
	Self node
	Peer map[string]node
}
type node struct {
	HostName     string
	OS           string
	UserID       int64
	Online       bool
	TailscaleIPs []string
}
type whois struct{ UserProfile struct{ ID int64 } }
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

// tailnet reads the local Tailscale daemon's view of devices.
func tailnet() (status, error) {
	var s status
	b, e := exec.Command("tailscale", "status", "--json").Output()
	if e != nil {
		return s, fmt.Errorf("tailscale status: %w", e)
	}
	e = json.Unmarshal(b, &s)
	return s, e
}

// ipv4 returns the device's first IPv4 address.
func ipv4(n node) string {
	for _, v := range n.TailscaleIPs {
		if ip := net.ParseIP(v); ip != nil && ip.To4() != nil {
			return v
		}
	}
	return ""
}

// resolve finds one online device owned by the current Tailscale user.
func resolve(s status, name string) (string, error) {
	var nodes []node
	if strings.EqualFold(s.Self.HostName, name) {
		nodes = append(nodes, s.Self)
	}
	for _, n := range s.Peer {
		if strings.EqualFold(n.HostName, name) {
			nodes = append(nodes, n)
		}
	}
	if len(nodes) != 1 {
		return "", fmt.Errorf("host %q not found uniquely on your tailnet", name)
	}
	n := nodes[0]
	if n.UserID != s.Self.UserID {
		return "", errors.New("host belongs to another Tailscale user")
	}
	if !n.Online && !strings.EqualFold(s.Self.HostName, name) {
		return "", fmt.Errorf("host %q is offline", name)
	}
	if ip := ipv4(n); ip != "" {
		return ip, nil
	}
	return "", errors.New("host has no Tailscale IPv4 address")
}

// sameUser verifies each request against Tailscale's authenticated user identity.
func sameUser(owner int64, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip, _, e := net.SplitHostPort(r.RemoteAddr)
		if e != nil {
			http.Error(w, "forbidden", 403)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		b, e := exec.CommandContext(ctx, "tailscale", "whois", "--json", ip).Output()
		var id whois
		if e != nil || json.Unmarshal(b, &id) != nil || id.UserProfile.ID == 0 || id.UserProfile.ID != owner {
			http.Error(w, "forbidden", 403)
			return
		}
		h.ServeHTTP(w, r)
	})
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
	s, e := tailnet()
	if e != nil {
		return e
	}
	ip := ipv4(s.Self)
	if ip == "" || s.Self.UserID == 0 {
		return errors.New("Tailscale must be connected")
	}
	cfg, e := loadConfig()
	if e != nil {
		return e
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
	for _, p := range []string{controlPort, openCodePort, codexPort} {
		l, e := net.Listen("tcp", net.JoinHostPort(ip, p))
		if e != nil {
			return fmt.Errorf("listen on %s: %w", p, e)
		}
		listeners = append(listeners, l)
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
	failed := make(chan error, 3)
	for i, l := range listeners {
		srv := &http.Server{Handler: sameUser(s.Self.UserID, handlers[i]), ReadHeaderTimeout: 5 * time.Second}
		go func(l net.Listener) { failed <- srv.Serve(l) }(l)
	}
	fmt.Fprintf(os.Stderr, "rmt: serving %s on %s\n", workspace, s.Self.HostName)
	select {
	case <-ctx.Done():
		return nil
	case e := <-failed:
		return e
	}
}

// localBridge limits the local proxy to clients holding this attachment's password.
func localBridge(next http.Handler, password string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		username, supplied, ok := r.BasicAuth()
		if !ok || username != "opencode" || subtle.ConstantTimeCompare([]byte(supplied), []byte(password)) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="rmt"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		r.Header.Del("Authorization")
		next.ServeHTTP(w, r)
	})
}

// translateLocation maps the local startup directory to the host workspace for OpenCode.
func translateLocation(r *http.Request, local, remote string) {
	q := r.URL.Query()
	if q.Get("location[directory]") == local {
		q.Set("location[directory]", remote)
		r.URL.RawQuery = q.Encode()
	}
}

// openCode starts a workspace-path bridge and runs the unmodified local native UI.
func openCode(ip, workspace string, extra []string) error {
	localDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("current directory: %w", err)
	}
	localDir, err = filepath.EvalSymlinks(localDir)
	if err != nil {
		return fmt.Errorf("current directory: %w", err)
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	defer l.Close()
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return err
	}
	password := base64.RawURLEncoding.EncodeToString(secret)
	p := httputil.NewSingleHostReverseProxy(&url.URL{Scheme: "http", Host: net.JoinHostPort(ip, openCodePort)})
	srv := &http.Server{Handler: localBridge(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		translateLocation(r, localDir, workspace)
		p.ServeHTTP(w, r)
	}), password), ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = srv.Serve(l) }()
	defer srv.Close()
	args := append([]string{"--server", "http://" + l.Addr().String(), localDir}, extra...)
	c := exec.Command("opencode", args...)
	c.Env = append(os.Environ(), "OPENCODE_PASSWORD="+password)
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return c.Run()
}

// connect launches a native agent interface, optionally resuming a saved session.
func connect(host, agent string, extra []string, saved *session) error {
	if agent != "opencode" && agent != "codex" {
		return fmt.Errorf("unknown agent %q (choose opencode or codex)", agent)
	}
	s, e := tailnet()
	if e != nil {
		return e
	}
	ip, e := resolve(s, host)
	if e != nil {
		return e
	}
	client := http.Client{Timeout: 5 * time.Second}
	res, e := client.Get("http://" + net.JoinHostPort(ip, controlPort) + "/info")
	if e != nil {
		return fmt.Errorf("cannot reach %s; start 'rmt serve' there: %w", host, e)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return fmt.Errorf("host refused connection: %s", res.Status)
	}
	var info hostInfo
	if e = json.NewDecoder(io.LimitReader(res.Body, 4096)).Decode(&info); e != nil || info.workspace() == "" {
		return errors.New("invalid host response")
	}
	if !(config{Agents: info.Agents}).enabled(agent) {
		return fmt.Errorf("%s is not enabled on %s; run 'rmt setup' on that host", agent, host)
	}
	dir := info.workspace()
	if saved != nil {
		if !strings.EqualFold(saved.Host, host) || saved.Agent != agent || !insideWorkspace(info.workspace(), saved.Dir) {
			return errors.New("session is outside the host workspace or belongs to another host")
		}
		dir = saved.Dir
	}
	if agent == "opencode" {
		if saved != nil {
			extra = append([]string{"--session", saved.ID}, extra...)
		}
		return openCode(ip, dir, extra)
	}
	var args []string
	if saved != nil {
		args = append([]string{"resume", "--remote", "ws://" + net.JoinHostPort(ip, codexPort), "-C", dir, saved.ID}, extra...)
	} else {
		args = append([]string{"--remote", "ws://" + net.JoinHostPort(ip, codexPort), "-C", dir}, extra...)
	}
	binary, e := exec.LookPath(agent)
	if e != nil {
		return fmt.Errorf("install %s locally: %w", agent, e)
	}
	return syscall.Exec(binary, append([]string{agent}, args...), os.Environ())
}

// main dispatches host, session discovery, and native-client commands.
func main() {
	log.SetFlags(0)
	a := os.Args[1:]
	if len(a) >= 1 && a[0] == "setup" {
		if len(a) != 1 {
			log.Fatal("usage: rmt setup")
		}
		if err := setup(os.Stdin, os.Stdout); err != nil {
			log.Fatal("rmt: ", err)
		}
		return
	}
	if len(a) >= 1 && a[0] == "config" {
		if err := configCommand(a[1:]); err != nil {
			log.Fatal("rmt: ", err)
		}
		return
	}
	if len(a) == 1 && a[0] == "serve" {
		if err := runHost(); err != nil {
			log.Fatal("rmt: ", err)
		}
		return
	}
	if len(a) >= 1 && a[0] == "ls" {
		if len(a) > 2 {
			log.Fatal("usage: rmt ls [host]")
		}
		host := ""
		if len(a) == 2 {
			host = a[1]
		}
		if err := listSessions(host); err != nil {
			log.Fatal("rmt: ", err)
		}
		return
	}
	if len(a) >= 1 && a[0] == "connect" {
		if len(a) < 2 {
			log.Fatal("usage: rmt connect [host/]<name> [agent options...]")
		}
		host, name, err := parseSessionReference(a[1])
		if err != nil {
			log.Fatal("rmt: ", err)
		}
		saved, err := findSession(name, host, "")
		if err != nil {
			log.Fatal("rmt: ", err)
		}
		if err = connect(saved.Host, saved.Agent, a[2:], &saved); err != nil {
			log.Fatal("rmt: ", err)
		}
		return
	}
	if len(a) < 2 || a[0] == "--help" || a[0] == "help" {
		fmt.Fprintln(os.Stderr, "Usage: rmt setup | rmt serve | rmt config [set <workspace>] | rmt ls [host] | rmt connect <name> | rmt <host> <opencode|codex> [--connect] [agent options...]")
		if len(a) == 0 || a[0] == "help" || a[0] == "--help" {
			return
		}
		os.Exit(2)
	}
	if len(a) > 2 && a[2] == "--connect" {
		saved, err := findSession("", a[0], a[1])
		if err != nil {
			log.Fatal("rmt: ", err)
		}
		if err = connect(saved.Host, saved.Agent, a[3:], &saved); err != nil {
			log.Fatal("rmt: ", err)
		}
		return
	}
	if err := connect(a[0], a[1], a[2:], nil); err != nil {
		log.Fatal("rmt: ", err)
	}
}
