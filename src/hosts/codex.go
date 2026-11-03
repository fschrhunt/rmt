package hosts

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

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
	path, err := privateSocket(path)
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
