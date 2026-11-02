// Remote connects native agent terminals to workspaces on your Tailscale devices.
package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
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
	UserID       int64
	Online       bool
	TailscaleIPs []string
}
type whois struct{ UserProfile struct{ ID int64 } }
type hostInfo struct {
	Dev string `json:"dev"`
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

// runHost runs both native backends from ~/dev and exposes them to the owner over Tailscale.
func runHost() error {
	s, e := tailnet()
	if e != nil {
		return e
	}
	ip := ipv4(s.Self)
	if ip == "" || s.Self.UserID == 0 {
		return errors.New("Tailscale must be connected")
	}
	home, e := os.UserHomeDir()
	if e != nil {
		return e
	}
	dev := filepath.Join(home, "dev")
	if st, e := os.Stat(dev); e != nil || !st.IsDir() {
		return fmt.Errorf("workspace %s is missing", dev)
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
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	children := []*exec.Cmd{exec.CommandContext(ctx, "opencode", "serve", "--hostname", "127.0.0.1", "--port", op), exec.CommandContext(ctx, "codex", "app-server", "--listen", "ws://127.0.0.1:"+cp)}
	done := make(chan error, 2)
	for i, c := range children {
		c.Dir = dev
		c.Stdout = io.Discard
		c.Stderr = io.Discard
		if i == 0 {
			c.Env = append(os.Environ(), "OPENCODE_PASSWORD="+password, "OPENCODE_DISABLE_EXTERNAL_SKILLS=1", "OPENCODE_DISABLE_CLAUDE_CODE_PROMPT=1")
		}
		if e = c.Start(); e != nil {
			stop()
			return fmt.Errorf("start %s: %w", c.Path, e)
		}
		go func(c *exec.Cmd) { done <- fmt.Errorf("%s stopped: %v", filepath.Base(c.Path), c.Wait()) }(c)
	}
	control := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/info" || r.Method != "GET" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(hostInfo{Dev: dev})
	})
	proxy := func(port string, auth bool) http.Handler {
		p := httputil.NewSingleHostReverseProxy(&url.URL{Scheme: "http", Host: net.JoinHostPort("127.0.0.1", port)})
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if auth {
				r.SetBasicAuth("opencode", password)
			}
			p.ServeHTTP(w, r)
		})
	}
	handlers := []http.Handler{control, proxy(op, true), proxy(cp, false)}
	failed := make(chan error, 3)
	for i, l := range listeners {
		srv := &http.Server{Handler: sameUser(s.Self.UserID, handlers[i]), ReadHeaderTimeout: 5 * time.Second}
		go func(l net.Listener) { failed <- srv.Serve(l) }(l)
	}
	fmt.Fprintf(os.Stderr, "remote: serving %s on %s\n", dev, s.Self.HostName)
	select {
	case <-ctx.Done():
		return nil
	case e := <-done:
		return e
	case e := <-failed:
		return e
	}
}

// translateLocation maps the local startup directory to the host workspace for OpenCode.
func translateLocation(r *http.Request, local, remote string) {
	q := r.URL.Query()
	if q.Get("location[directory]") == local {
		q.Set("location[directory]", remote)
		r.URL.RawQuery = q.Encode()
	}
}

// openCode starts a local workspace-path bridge and runs the unmodified native UI.
func openCode(ip, remoteDev string, extra []string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	localDev := filepath.Join(home, "dev")
	if st, err := os.Stat(localDev); err != nil || !st.IsDir() {
		return fmt.Errorf("local workspace %s is missing", localDev)
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	defer l.Close()
	p := httputil.NewSingleHostReverseProxy(&url.URL{Scheme: "http", Host: net.JoinHostPort(ip, openCodePort)})
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		translateLocation(r, localDev, remoteDev)
		p.ServeHTTP(w, r)
	}), ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = srv.Serve(l) }()
	defer srv.Close()
	args := append([]string{"--server", "http://" + l.Addr().String(), localDev}, extra...)
	c := exec.Command("opencode", args...)
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return c.Run()
}

// connect replaces this process with the native local agent interface.
func connect(host, agent string, extra []string) error {
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
		return fmt.Errorf("cannot reach %s; start 'remote serve' there: %w", host, e)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return fmt.Errorf("host refused connection: %s", res.Status)
	}
	var info hostInfo
	if e = json.NewDecoder(io.LimitReader(res.Body, 4096)).Decode(&info); e != nil || info.Dev == "" {
		return errors.New("invalid host response")
	}
	if agent == "opencode" {
		return openCode(ip, info.Dev, extra)
	}
	args := append([]string{"--remote", "ws://" + net.JoinHostPort(ip, codexPort), "-C", info.Dev}, extra...)
	binary, e := exec.LookPath(agent)
	if e != nil {
		return fmt.Errorf("install %s locally: %w", agent, e)
	}
	return syscall.Exec(binary, append([]string{agent}, args...), os.Environ())
}

// main dispatches the host and client commands.
func main() {
	log.SetFlags(0)
	a := os.Args[1:]
	if len(a) == 1 && a[0] == "serve" {
		if e := runHost(); e != nil {
			log.Fatal("remote: ", e)
		}
		return
	}
	if len(a) < 2 || a[0] == "--help" || a[0] == "help" {
		fmt.Fprintln(os.Stderr, "Usage: remote serve | remote <host> <opencode|codex> [agent options...]")
		if len(a) == 0 || a[0] == "help" || a[0] == "--help" {
			return
		}
		os.Exit(2)
	}
	if e := connect(a[0], a[1], a[2:]); e != nil {
		log.Fatal("remote: ", e)
	}
}
