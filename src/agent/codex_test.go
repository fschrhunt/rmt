package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

// TestCodexSessionsUsesRemoteThreads checks native WebSocket discovery and workspace filtering.
func TestCodexSessionsUsesRemoteThreads(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.CloseNow()
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		for {
			var req struct {
				ID     *int   `json:"id"`
				Method string `json:"method"`
			}
			if wsjson.Read(ctx, c, &req) != nil {
				return
			}
			if req.ID == nil {
				continue
			}
			var result any = map[string]any{}
			if req.Method == "thread/list" {
				result = map[string]any{"data": []any{map[string]any{"id": "one", "cwd": "/home/me/dev/app", "name": "Example", "status": map[string]any{"type": "active"}}, map[string]any{"id": "two", "cwd": "/home/me/private"}}, "nextCursor": nil}
			}
			if wsjson.Write(ctx, c, map[string]any{"id": *req.ID, "result": result}) != nil {
				return
			}
		}
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	got, err := CodexSessions(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), "server", "/home/me/dev")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "one" || got[0].Title != "Example" || !got[0].Active {
		b, _ := json.Marshal(got)
		t.Fatalf("unexpected sessions: %s", b)
	}
}
