package network

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// TestResolveRequiresAnOwnedOnlineDevice protects the host selection boundary.
func TestResolveRequiresAnOwnedOnlineDevice(t *testing.T) {
	s := Status{Self: Node{HostName: "mac", UserID: 1, TailscaleIPs: []string{"100.64.0.1"}}, Peer: map[string]Node{"a": {HostName: "mini", UserID: 1, Online: true, TailscaleIPs: []string{"100.64.0.2"}}, "b": {HostName: "other", UserID: 2, Online: true, TailscaleIPs: []string{"100.64.0.3"}}, "c": {HostName: "offline", UserID: 1}}}
	if got, e := Resolve(s, "mini"); e != nil || got != "100.64.0.2" {
		t.Fatalf("resolve mini: %q, %v", got, e)
	}
	for _, name := range []string{"other", "offline", "missing"} {
		if _, e := Resolve(s, name); e == nil {
			t.Errorf("resolved %s", name)
		}
	}
}

// TestSameUserRejectsUnknownPeer ensures unauthenticated clients cannot reach a backend.
func TestSameUserRejectsUnknownPeer(t *testing.T) {
	reached := false
	h := SameUser(1, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true }))
	r := httptest.NewRequest("GET", "http://localhost/", nil)
	r.RemoteAddr = "not-an-address"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if reached || w.Code != http.StatusForbidden {
		t.Fatalf("reached=%v status=%d", reached, w.Code)
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
	h := SameUser(42, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
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
