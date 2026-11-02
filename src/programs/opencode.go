// Package programs connects native program interfaces over remote connections.
package programs

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

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

// translateLocation maps the local startup directory to the host directory for OpenCode.
func translateLocation(r *http.Request, local, remote string) {
	q := r.URL.Query()
	if q.Get("location[directory]") == local {
		q.Set("location[directory]", remote)
		r.URL.RawQuery = q.Encode()
	}
}

// OpenCodeAt runs the native client through a local bridge to a selected host.
func OpenCodeAt(target *url.URL, transport http.RoundTripper, directory string, extra []string) error {
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
	p := httputil.NewSingleHostReverseProxy(target)
	if transport != nil {
		p.Transport = transport
		if closer, ok := transport.(interface{ CloseIdleConnections() }); ok {
			defer closer.CloseIdleConnections()
		}
	}
	srv := &http.Server{Handler: localBridge(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		translateLocation(r, localDir, directory)
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
