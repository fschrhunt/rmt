package programs

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

// TestLocalBridgeRequiresAuthentication protects the remote program from other local clients.
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

// TestOpenCodeClientNeedsNoLocalProject checks that a client works without a matching project directory.
func TestOpenCodeClientNeedsNoLocalProject(t *testing.T) {
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
	if err := OpenCodeAt(&url.URL{Scheme: "http", Host: "127.0.0.1:47471"}, nil, "/other/project", nil); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(output)
	if err != nil || !strings.HasPrefix(string(data), "--server\nhttp://127.0.0.1:") {
		t.Fatalf("client arguments: %q, %v", data, err)
	}
}
