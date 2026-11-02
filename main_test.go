package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestResolveRequiresAnOwnedOnlineDevice protects the host selection boundary.
func TestResolveRequiresAnOwnedOnlineDevice(t *testing.T) {
	s := status{Self: node{HostName: "mac", UserID: 1, TailscaleIPs: []string{"100.64.0.1"}}, Peer: map[string]node{"a": {HostName: "mini", UserID: 1, Online: true, TailscaleIPs: []string{"100.64.0.2"}}, "b": {HostName: "other", UserID: 2, Online: true, TailscaleIPs: []string{"100.64.0.3"}}, "c": {HostName: "offline", UserID: 1}}}
	if got, e := resolve(s, "mini"); e != nil || got != "100.64.0.2" {
		t.Fatalf("resolve mini: %q, %v", got, e)
	}
	for _, name := range []string{"other", "offline", "missing"} {
		if _, e := resolve(s, name); e == nil {
			t.Errorf("resolved %s", name)
		}
	}
}

// TestAgentSupervisionIsIndependent keeps a healthy backend alive while another restarts.
func TestAgentSupervisionIsIndependent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dir := t.TempDir()
	crashes := filepath.Join(dir, "crashes")
	healthy := filepath.Join(dir, "healthy")
	done := make(chan struct{}, 2)
	go func() {
		superviseAgent(ctx, "crashing-test", func() *exec.Cmd {
			c := exec.CommandContext(ctx, "/bin/sh", "-c", `printf x >> "$TEST_PATH"; exit 1`)
			c.Env = append(os.Environ(), "TEST_PATH="+crashes)
			return c
		})
		done <- struct{}{}
	}()
	go func() {
		superviseAgent(ctx, "healthy-test", func() *exec.Cmd {
			c := exec.CommandContext(ctx, "/bin/sh", "-c", `printf x >> "$TEST_PATH"; exec /bin/sleep 10`)
			c.Env = append(os.Environ(), "TEST_PATH="+healthy)
			return c
		})
		done <- struct{}{}
	}()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		crashed, _ := os.ReadFile(crashes)
		started, _ := os.ReadFile(healthy)
		if len(crashed) >= 2 && len(started) == 1 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	crashed, _ := os.ReadFile(crashes)
	started, _ := os.ReadFile(healthy)
	cancel()
	<-done
	<-done
	if len(crashed) < 2 || len(started) != 1 {
		t.Fatalf("crashing starts %d, healthy starts %d", len(crashed), len(started))
	}
}

// TestSameUserRejectsUnknownPeer ensures unauthenticated clients cannot reach a backend.
func TestSameUserRejectsUnknownPeer(t *testing.T) {
	reached := false
	h := sameUser(1, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true }))
	r := httptest.NewRequest("GET", "http://localhost/", nil)
	r.RemoteAddr = "not-an-address"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if reached || w.Code != http.StatusForbidden {
		t.Fatalf("reached=%v status=%d", reached, w.Code)
	}
}

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

// TestSameUserUsesTailscaleIdentity checks that matching and foreign accounts are distinguished.
func TestSameUserUsesTailscaleIdentity(t *testing.T) {
	dir := t.TempDir()
	script := "#!/bin/sh\nif [ \"$3\" = 100.64.0.2 ]; then echo '{\"UserProfile\":{\"ID\":42}}'; else echo '{\"UserProfile\":{\"ID\":7}}'; fi\n"
	if err := os.WriteFile(filepath.Join(dir, "tailscale"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	h := sameUser(42, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	for _, tc := range []struct {
		ip   string
		want int
	}{{"100.64.0.2", 204}, {"100.64.0.3", 403}} {
		r := httptest.NewRequest("GET", "http://localhost/", nil)
		r.RemoteAddr = tc.ip + ":1234"
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Errorf("%s: got %d, want %d", tc.ip, w.Code, tc.want)
		}
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
