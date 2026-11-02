package agent

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/fschrhunt/rmt/src/network"
	"github.com/fschrhunt/rmt/src/session"
)

// CodexDirect runs the native UI through an authenticated local-to-HTTPS bridge.
func CodexDirect(profile network.Profile, dir string, extra []string, saved *session.Session) error {
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
	endpoint, err := url.Parse(profile.URL + "/codex")
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
	if saved != nil {
		args = append(args, "resume")
	}
	args = append(args, "--remote", "ws://"+l.Addr().String(), "--remote-auth-token-env", "RMT_REMOTE_TOKEN", "-C", dir)
	if saved != nil {
		args = append(args, saved.ID)
	}
	args = append(args, extra...)
	c := exec.Command("codex", args...)
	c.Env = append(os.Environ(), "RMT_REMOTE_TOKEN="+token)
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	return c.Run()
}

// rpc sends a request and waits for its matching response, ignoring notifications.
func rpc(ctx context.Context, conn *websocket.Conn, id int, method string, params any, result any) error {
	if err := wsjson.Write(ctx, conn, map[string]any{"id": id, "method": method, "params": params}); err != nil {
		return err
	}
	for {
		var msg struct {
			ID     *int            `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := wsjson.Read(ctx, conn, &msg); err != nil {
			return err
		}
		if msg.ID == nil || *msg.ID != id {
			continue
		}
		if msg.Error != nil {
			return errors.New(msg.Error.Message)
		}
		return json.Unmarshal(msg.Result, result)
	}
}

// CodexSessions lists saved Codex threads in the host workspace.
func CodexSessions(ctx context.Context, address, host, workspace string, options ...*websocket.DialOptions) ([]session.Session, error) {
	var opts *websocket.DialOptions
	if len(options) > 0 {
		opts = options[0]
	}
	conn, _, err := websocket.Dial(ctx, address, opts)
	if err != nil {
		return nil, err
	}
	defer conn.CloseNow()
	conn.SetReadLimit(8 << 20)
	var initialized json.RawMessage
	if err = rpc(ctx, conn, 1, "initialize", map[string]any{"clientInfo": map[string]string{"name": "rmt", "title": "rmt", "version": "0.1.0"}}, &initialized); err != nil {
		return nil, fmt.Errorf("initialize: %w", err)
	}
	if err = wsjson.Write(ctx, conn, map[string]any{"method": "initialized", "params": map[string]any{}}); err != nil {
		return nil, err
	}
	var sessions []session.Session
	var cursor string
	seen := make(map[string]bool)
	for {
		params := map[string]any{"limit": 100, "sortKey": "updated_at", "modelProviders": []string{}, "sourceKinds": []string{"cli", "vscode", "appServer"}}
		if cursor != "" {
			params["cursor"] = cursor
		}
		var page struct {
			Data []struct {
				ID      string `json:"id"`
				Cwd     string `json:"cwd"`
				Name    string `json:"name"`
				Preview string `json:"preview"`
				Updated int64  `json:"updatedAt"`
				Status  struct {
					Type string `json:"type"`
				} `json:"status"`
			} `json:"data"`
			NextCursor string `json:"nextCursor"`
		}
		if err = rpc(ctx, conn, 2, "thread/list", params, &page); err != nil {
			return nil, fmt.Errorf("thread/list: %w", err)
		}
		for _, v := range page.Data {
			if v.ID == "" || !session.InsideWorkspace(workspace, v.Cwd) {
				continue
			}
			title := v.Name
			if title == "" {
				title = v.Preview
			}
			sessions = append(sessions, session.Session{Name: session.Name("codex", v.ID), Host: host, Agent: "codex", ID: v.ID, Dir: v.Cwd, Title: title, Active: v.Status.Type == "active", Updated: v.Updated})
		}
		if page.NextCursor == "" {
			break
		}
		if seen[page.NextCursor] {
			return nil, errors.New("Codex repeated a thread cursor")
		}
		seen[page.NextCursor] = true
		cursor = page.NextCursor
	}
	return sessions, nil
}
