package hosts

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os/exec"
	"path/filepath"
	"strings"
)

// limitedOutput bounds command output while allowing the process to finish.
type limitedOutput struct {
	data     bytes.Buffer
	overflow bool
}

func (w *limitedOutput) Write(p []byte) (int, error) {
	n := len(p)
	remaining := (1 << 20) - w.data.Len()
	if len(p) > remaining {
		p = p[:remaining]
		w.overflow = true
	}
	_, _ = w.data.Write(p)
	return n, nil
}

// runHostCommand runs a host callback command under its request deadline.
func runHostCommand(ctx context.Context, directory string, args []string) (string, string, int, error) {
	if len(args) == 0 || args[0] == "" || !filepath.IsAbs(directory) {
		return "", "", 0, errors.New("invalid extension command or directory")
	}
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Dir = directory
	var stdout, stderr limitedOutput
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if stdout.overflow || stderr.overflow {
		return "", "", 0, errors.New("extension command output is too large")
	}
	if ctx.Err() != nil {
		return "", stderr.data.String(), 0, ctx.Err()
	}
	if err == nil {
		return stdout.data.String(), stderr.data.String(), 0, nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return stdout.data.String(), stderr.data.String(), exit.ExitCode(), nil
	}
	return "", stderr.data.String(), 0, err
}

// programBackend describes a native user-managed service, never owned by rmt.
type programBackend struct {
	name          string
	target        *url.URL
	password      string
	transport     http.RoundTripper
	authorization string
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

// proxy forwards authenticated requests to the program's native local backend.
func (b *programBackend) proxy(w http.ResponseWriter, r *http.Request) {
	if b.name == "opencode" {
		r.SetBasicAuth("opencode", b.password)
	} else if b.authorization != "" {
		r.Header.Set("Authorization", b.authorization)
	} else {
		r.Header.Del("Authorization")
	}
	proxy := httputil.NewSingleHostReverseProxy(b.target)
	if b.transport != nil {
		proxy.Transport = b.transport
	}
	proxy.ServeHTTP(w, r)
}
