package rmt

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fschrhunt/rmt/src/network"
)

// TestWorkspaceRejectsSiblingAndTraversal keeps discovered sessions inside the chosen workspace.
func TestWorkspaceRejectsSiblingAndTraversal(t *testing.T) {
	for _, tc := range []struct {
		path string
		want bool
	}{{"/home/me/dev", true}, {"/home/me/dev/app", true}, {"/home/me/dev-other", false}, {"/home/me/dev/../other", false}, {"relative", false}} {
		if got := insideWorkspace("/home/me/dev", tc.path); got != tc.want {
			t.Errorf("%q: got %v", tc.path, got)
		}
	}
}

// TestPairedSessionsDoNotRequireTailscale scans multiple pinned hosts without a VPN.
func TestPairedSessionsDoNotRequireTailscale(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("PATH", t.TempDir())
	dir := filepath.Join(home, "config", "rmt")
	for _, name := range []string{"one", "two"} {
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer test-token" {
				http.Error(w, "unauthorized", 401)
				return
			}
			switch r.URL.Path {
			case "/info":
				fmt.Fprint(w, `{"workspace":"/projects","agents":["opencode"]}`)
			case "/opencode/api/session/active":
				fmt.Fprint(w, `{"data":{}}`)
			case "/opencode/api/session":
				fmt.Fprint(w, `{"data":[{"id":"session","location":{"directory":"/projects"}}]}`)
			default:
				http.NotFound(w, r)
			}
		}))
		t.Cleanup(srv.Close)
		sum := sha256.Sum256(srv.Certificate().Raw)
		profile := network.Profile{URL: srv.URL, Fingerprint: hex.EncodeToString(sum[:]), Token: "test-token"}
		if err := network.SaveProfile(dir, name, profile); err != nil {
			t.Fatal(err)
		}
	}
	sessions, problems, complete, err := scanSessions("")
	if err != nil || !complete || len(problems) != 0 || len(sessions) != 2 {
		t.Fatalf("sessions=%v problems=%v complete=%v err=%v", sessions, problems, complete, err)
	}
	filtered, problems, complete, err := scanSessions("one")
	if err != nil || !complete || len(problems) != 0 || len(filtered) != 1 || filtered[0].Host != "one" {
		t.Fatalf("filtered=%v problems=%v complete=%v err=%v", filtered, problems, complete, err)
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
