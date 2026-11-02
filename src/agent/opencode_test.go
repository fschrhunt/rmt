package agent

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fschrhunt/rmt/src/session"
)

// TestTranslateLocation maps only the local startup root, preserving other paths.
func TestTranslateLocation(t *testing.T) {
	r := httptest.NewRequest("GET", "http://example/api/location?location%5Bdirectory%5D=%2FUsers%2Fme%2Fdev", nil)
	translateLocation(r, "/Users/me/dev", "/home/me/dev")
	if got := r.URL.Query().Get("location[directory]"); got != "/home/me/dev" {
		t.Fatal(got)
	}
	r = httptest.NewRequest("GET", "http://example/?location%5Bdirectory%5D=%2Fother", nil)
	translateLocation(r, "/Users/me/dev", "/home/me/dev")
	if got := r.URL.Query().Get("location[directory]"); got != "/other" {
		t.Fatal(got)
	}
}

// TestLocalBridgeRequiresAuthentication protects the remote agent from other local clients.
func TestLocalBridgeRequiresAuthentication(t *testing.T) {
	reached := false
	h := localBridge(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		if r.Header.Get("Authorization") != "" {
			t.Error("local credentials forwarded upstream")
		}
		w.WriteHeader(http.StatusNoContent)
	}), "secret")
	for _, tc := range []struct {
		user, pass string
		want       int
	}{{"", "", 401}, {"opencode", "wrong", 401}, {"other", "secret", 401}, {"opencode", "secret", 204}} {
		r := httptest.NewRequest("GET", "http://localhost/", nil)
		if tc.user != "" {
			r.SetBasicAuth(tc.user, tc.pass)
		}
		w := httptest.NewRecorder()
		reached = false
		h.ServeHTTP(w, r)
		if w.Code != tc.want || reached != (tc.want == 204) {
			t.Fatalf("user %q: status %d, reached %v", tc.user, w.Code, reached)
		}
	}
}

// TestOpenCodeClientNeedsNoLocalWorkspace checks that a client works without ~/dev.
func TestOpenCodeClientNeedsNoLocalWorkspace(t *testing.T) {
	home := t.TempDir()
	bin := filepath.Join(home, "bin")
	if err := os.Mkdir(bin, 0700); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(home, "args")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$RMT_TEST_ARGS\"\n"
	if err := os.WriteFile(filepath.Join(bin, "opencode"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("PATH", bin)
	t.Setenv("RMT_TEST_ARGS", output)
	if err := OpenCode("127.0.0.1", "/other/workspace", nil); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(output)
	if err != nil || !strings.HasPrefix(string(data), "--server\nhttp://127.0.0.1:") {
		t.Fatalf("client arguments: %q, %v", data, err)
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
	got, err := OpenCodeSessions(context.Background(), srv.Client(), srv.URL, "mini", "/home/me/dev")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "ses_one" || !got[0].Active || got[1].ID != "ses_two" || got[1].Active {
		t.Fatalf("unexpected sessions: %+v", got)
	}
	if got[0].Name != session.Name("opencode", "ses_one") {
		t.Fatalf("unstable name: %s", got[0].Name)
	}
}
