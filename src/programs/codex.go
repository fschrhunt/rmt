package programs

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/fschrhunt/rmt/src/network"
)

// CodexDirect runs the native UI through an authenticated local-to-HTTPS bridge.
func CodexDirect(profile network.Profile, address, dir string, extra []string) error {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	defer l.Close()
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return err
	}
	token := base64.RawURLEncoding.EncodeToString(secret)
	endpoint, err := url.Parse(address)
	if err != nil {
		return err
	}
	transport, err := profile.Transport()
	if err != nil {
		return err
	}
	if closer, ok := transport.(interface{ CloseIdleConnections() }); ok {
		defer closer.CloseIdleConnections()
	}
	p := httputil.NewSingleHostReverseProxy(endpoint)
	p.Transport = transport
	srv := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		value := r.Header.Get("Authorization")
		provided := strings.TrimPrefix(value, "Bearer ")
		if provided == value || subtle.ConstantTimeCompare([]byte(provided), []byte(token)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		r.Header.Del("Authorization")
		p.ServeHTTP(w, r)
	})}
	go func() { _ = srv.Serve(l) }()
	defer srv.Close()
	args := []string{}
	if len(extra) > 0 && extra[0] == "resume" {
		args = append(args, "resume")
		extra = extra[1:]
	}
	args = append(args, "--remote", "ws://"+l.Addr().String(), "--remote-auth-token-env", "RMT_REMOTE_TOKEN", "-C", dir)
	args = append(args, extra...)
	c := exec.Command("codex", args...)
	c.Env = append(os.Environ(), "RMT_REMOTE_TOKEN="+token)
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	return c.Run()
}
