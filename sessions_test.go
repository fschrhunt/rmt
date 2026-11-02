package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

// TestInsideDevRejectsSiblingAndTraversal keeps discovered sessions inside the chosen workspace.
func TestInsideDevRejectsSiblingAndTraversal(t *testing.T) {
	for _, tc := range []struct {
		path string
		want bool
	}{{"/home/me/dev", true}, {"/home/me/dev/app", true}, {"/home/me/dev-other", false}, {"/home/me/dev/../other", false}, {"relative", false}} {
		if got := insideWorkspace("/home/me/dev", tc.path); got != tc.want {
			t.Errorf("%q: got %v", tc.path, got)
		}
	}
}

// TestOpenCodeSessionsFollowsPagesAndReportsActive verifies native-session discovery.
func TestOpenCodeSessionsFollowsPagesAndReportsActive(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/session/active":
			fmt.Fprint(w, `{"data":{"ses_one":{}}}`)
		case "/api/session":
			if r.URL.Query().Get("cursor") == "next" {
				fmt.Fprint(w, `{"data":[{"id":"ses_two","title":"Second","location":{"directory":"/home/me/dev/app"}}],"cursor":{"next":null}}`)
				return
			}
			fmt.Fprint(w, `{"data":[{"id":"ses_one","title":"First","location":{"directory":"/home/me/dev"}},{"id":"ses_other","location":{"directory":"/home/me/private"}}],"cursor":{"next":"next"}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	got, err := openCodeSessions(context.Background(), srv.Client(), srv.URL, "mini", "/home/me/dev")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "ses_one" || !got[0].Active || got[1].ID != "ses_two" || got[1].Active {
		t.Fatalf("unexpected sessions: %+v", got)
	}
	if got[0].Name != sessionName("opencode", "ses_one") {
		t.Fatalf("unstable name: %s", got[0].Name)
	}
}

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
	got, err := codexSessions(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), "server", "/home/me/dev")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "one" || got[0].Title != "Example" || !got[0].Active {
		b, _ := json.Marshal(got)
		t.Fatalf("unexpected sessions: %s", b)
	}
}

// TestSelectSessionNeverGuesses verifies that multiple matching sessions need an explicit name.
func TestSelectSessionNeverGuesses(t *testing.T) {
	sessions := []session{{Name: "wolf", Agent: "codex"}, {Name: "bear", Agent: "codex"}, {Name: "fox", Agent: "opencode"}}
	if _, err := selectSession(sessions, "", "codex"); err == nil {
		t.Fatal("multiple sessions should be ambiguous")
	}
	got, err := selectSession(sessions, "bear", "")
	if err != nil || got.Name != "bear" {
		t.Fatalf("got %+v, %v", got, err)
	}
	if _, err = selectSession(sessions, "missing", ""); err == nil {
		t.Fatal("missing name should fail")
	}
}

// TestCollidingNamesUseAnimalPaths checks that collisions never choose the wrong session.
func TestCollidingNamesUseAnimalPaths(t *testing.T) {
	sessions := []session{
		{Name: sessionName("codex", "test-7"), Host: "mini", Agent: "codex", ID: "test-7"},
		{Name: sessionName("codex", "test-67"), Host: "mini", Agent: "codex", ID: "test-67"},
	}
	if sessions[0].Name != sessions[1].Name {
		t.Fatal("test sessions must have the same animal name")
	}
	if _, err := selectSession(sessions, sessions[0].Name, ""); err == nil {
		t.Fatal("colliding short names should be ambiguous")
	}
	for _, want := range sessions {
		ref := collisionReference(want, sessions)
		host, name, err := parseSessionReference(ref)
		if err != nil || host != "mini" || strings.ContainsAny(ref, "0123456789") {
			t.Fatalf("invalid collision reference: %q, %v", ref, err)
		}
		got, err := selectSession(sessions, name, "")
		if err != nil || got.ID != want.ID {
			t.Fatalf("collision fallback failed: %+v, %v", got, err)
		}
	}
}

// TestSessionReferencePreservesHostSelection checks the collision escape hatch.
func TestSessionReferencePreservesHostSelection(t *testing.T) {
	host, name, err := parseSessionReference("Mini/Akita-238932e4")
	if err != nil || host != "mini" || name != "akita-238932e4" {
		t.Fatalf("got %q, %q, %v", host, name, err)
	}
	if _, _, err := parseSessionReference("mini/"); err == nil {
		t.Fatal("empty handle should fail")
	}
	host, name, err = parseSessionReference("Mini/Puma/Crow/Fish")
	if err != nil || host != "mini" || name != "puma/crow/fish" {
		t.Fatalf("animal path: %q, %q, %v", host, name, err)
	}
}

// TestSessionNameStaysStable protects short names and older reconnect handles.
func TestSessionNameStaysStable(t *testing.T) {
	if got := sessionName("opencode", "ses_test"); got != "goose" {
		t.Fatalf("session name changed: %s", got)
	}
	sessions := []session{{Name: "goose", Agent: "opencode", ID: "ses_test"}}
	for _, previous := range []string{"akita-238932e4", "hen6fn"} {
		got, err := selectSession(sessions, previous, "")
		if err != nil || got.Name != "goose" {
			t.Fatalf("old name did not reconnect: %+v, %v", got, err)
		}
	}
}

// TestIncompleteDiscoveryNeverSelects prevents attaching to a different host's session.
func TestIncompleteDiscoveryNeverSelects(t *testing.T) {
	sessions := []session{{Name: "fox", Host: "mac", Agent: "opencode", ID: "one"}}
	if _, err := selectScanned(sessions, false, "fox", "", ""); err == nil {
		t.Fatal("selected from an incomplete scan")
	}
	if _, err := selectScanned(sessions, false, "fox", "mac", ""); err == nil {
		t.Fatal("selected from an incomplete host scan")
	}
	got, err := selectScanned(sessions, true, "fox", "", "")
	if err != nil || got.Host != "mac" {
		t.Fatalf("complete scan: %+v, %v", got, err)
	}
}
