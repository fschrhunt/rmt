package programs

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/fschrhunt/rmt/src/extensions"
	"github.com/fschrhunt/rmt/src/network"
)

// Custom runs an extension's native client through a private authenticated local bridge.
func Custom(ctx context.Context, registry *extensions.Registry, profile network.Profile, address, name, directory string, args []string) error {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	defer listener.Close()
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return err
	}
	token := base64.RawURLEncoding.EncodeToString(secret)
	prefix := "/" + token
	target, err := url.Parse(address)
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
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.Transport = transport
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if len(path) < len(prefix) || subtle.ConstantTimeCompare([]byte(path[:len(prefix)]), []byte(prefix)) != 1 || (len(path) > len(prefix) && path[len(prefix)] != '/') {
			http.NotFound(w, r)
			return
		}
		path = strings.TrimPrefix(path, prefix)
		for _, part := range strings.Split(path, "/") {
			if part == "." || part == ".." {
				http.NotFound(w, r)
				return
			}
		}
		r.URL.Path = path
		if r.URL.Path == "" {
			r.URL.Path = "/"
		}
		r.URL.RawPath = ""
		proxy.ServeHTTP(w, r)
	})
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = server.Serve(listener) }()
	defer server.Close()
	endpoint := "http://" + listener.Addr().String() + prefix
	argv, err := registry.Client(ctx, name, directory, endpoint, args)
	if err != nil {
		return err
	}
	if len(argv) == 0 || argv[0] == "" {
		return errors.New("extension did not provide a client command")
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = append(os.Environ(), "RMT_ENDPOINT="+endpoint, "RMT_DIRECTORY="+directory)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}
