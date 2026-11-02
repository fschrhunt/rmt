package rmt

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
	return openCodeAt(&url.URL{Scheme: "http", Host: net.JoinHostPort(ip, openCodePort)}, nil, workspace, extra)
}

// openCodeAt runs the native client through a local bridge to a selected host.
func openCodeAt(target *url.URL, transport http.RoundTripper, workspace string, extra []string) error {
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

// openCodeSessions lists saved root sessions and active drains on one backend.
func openCodeSessions(ctx context.Context, client *http.Client, base, host, workspace string) ([]session, error) {
	var active struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := jsonGet(ctx, client, base+"/api/session/active", &active); err != nil {
		return nil, fmt.Errorf("active sessions: %w", err)
	}
	var sessions []session
	cursor := ""
	seen := make(map[string]bool)
	for {
		q := url.Values{"limit": {"100"}, "parentID": {"null"}, "archived": {"false"}}
		if cursor != "" {
			q.Set("cursor", cursor)
		}
		var page struct {
			Data []struct {
				ID    string `json:"id"`
				Title string `json:"title"`
				Time  struct {
					Updated int64 `json:"updated"`
				} `json:"time"`
				Location struct {
					Directory string `json:"directory"`
				} `json:"location"`
			} `json:"data"`
			Cursor struct {
				Next string `json:"next"`
			} `json:"cursor"`
		}
		if err := jsonGet(ctx, client, base+"/api/session?"+q.Encode(), &page); err != nil {
			return nil, err
		}
		for _, v := range page.Data {
			if v.ID == "" || !insideWorkspace(workspace, v.Location.Directory) {
				continue
			}
			_, isActive := active.Data[v.ID]
			sessions = append(sessions, session{Name: sessionName("opencode", v.ID), Host: host, Agent: "opencode", ID: v.ID, Dir: v.Location.Directory, Title: v.Title, Active: isActive, Updated: v.Time.Updated / 1000})
		}
		if len(page.Data) == 0 || page.Cursor.Next == "" {
			break
		}
		if seen[page.Cursor.Next] {
			return nil, errors.New("OpenCode repeated a session cursor")
		}
		seen[page.Cursor.Next] = true
		cursor = page.Cursor.Next
	}
	return sessions, nil
}
