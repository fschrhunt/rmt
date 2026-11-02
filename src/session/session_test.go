package session

import (
	"strings"
	"testing"
)

// TestWorkspaceRejectsSiblingAndTraversal keeps discovered sessions inside the chosen workspace.
func TestWorkspaceRejectsSiblingAndTraversal(t *testing.T) {
	for _, tc := range []struct {
		path string
		want bool
	}{{"/home/me/dev", true}, {"/home/me/dev/app", true}, {"/home/me/dev-other", false}, {"/home/me/dev/../other", false}, {"relative", false}} {
		if got := InsideWorkspace("/home/me/dev", tc.path); got != tc.want {
			t.Errorf("%q: got %v", tc.path, got)
		}
	}
}

// TestSelectSessionNeverGuesses verifies that multiple matching sessions need an explicit name.
func TestSelectSessionNeverGuesses(t *testing.T) {
	sessions := []Session{{Name: "wolf", Agent: "codex"}, {Name: "bear", Agent: "codex"}, {Name: "fox", Agent: "opencode"}}
	if _, err := Select(sessions, "", "codex"); err == nil {
		t.Fatal("multiple sessions should be ambiguous")
	}
	got, err := Select(sessions, "bear", "")
	if err != nil || got.Name != "bear" {
		t.Fatalf("got %+v, %v", got, err)
	}
	if _, err = Select(sessions, "missing", ""); err == nil {
		t.Fatal("missing name should fail")
	}
}

// TestCollidingNamesUseAnimalPaths checks that collisions never choose the wrong session.
func TestCollidingNamesUseAnimalPaths(t *testing.T) {
	sessions := []Session{
		{Name: Name("codex", "test-7"), Host: "mini", Agent: "codex", ID: "test-7"},
		{Name: Name("codex", "test-67"), Host: "mini", Agent: "codex", ID: "test-67"},
	}
	if sessions[0].Name != sessions[1].Name {
		t.Fatal("test sessions must have the same animal name")
	}
	if _, err := Select(sessions, sessions[0].Name, ""); err == nil {
		t.Fatal("colliding short names should be ambiguous")
	}
	for _, want := range sessions {
		ref := CollisionReference(want, sessions)
		host, name, err := ParseReference(ref)
		if err != nil || host != "mini" || strings.ContainsAny(ref, "0123456789") {
			t.Fatalf("invalid collision reference: %q, %v", ref, err)
		}
		got, err := Select(sessions, name, "")
		if err != nil || got.ID != want.ID {
			t.Fatalf("collision fallback failed: %+v, %v", got, err)
		}
	}
}

// TestSessionReferencePreservesHostSelection checks the collision escape hatch.
func TestSessionReferencePreservesHostSelection(t *testing.T) {
	host, name, err := ParseReference("Mini/Akita-238932e4")
	if err != nil || host != "mini" || name != "akita-238932e4" {
		t.Fatalf("got %q, %q, %v", host, name, err)
	}
	if _, _, err := ParseReference("mini/"); err == nil {
		t.Fatal("empty handle should fail")
	}
	host, name, err = ParseReference("Mini/Puma/Crow/Fish")
	if err != nil || host != "mini" || name != "puma/crow/fish" {
		t.Fatalf("animal path: %q, %q, %v", host, name, err)
	}
}

// TestIncompleteDiscoveryNeverSelects prevents attaching to a different host's session.
func TestIncompleteDiscoveryNeverSelects(t *testing.T) {
	sessions := []Session{{Name: "fox", Host: "mac", Agent: "opencode", ID: "one"}}
	if _, err := SelectScanned(sessions, false, "fox", "", ""); err == nil {
		t.Fatal("selected from an incomplete scan")
	}
	if _, err := SelectScanned(sessions, false, "fox", "mac", ""); err == nil {
		t.Fatal("selected from an incomplete host scan")
	}
	got, err := SelectScanned(sessions, true, "fox", "", "")
	if err != nil || got.Host != "mac" {
		t.Fatalf("complete scan: %+v, %v", got, err)
	}
}
