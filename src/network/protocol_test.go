package network

import "testing"

// TestHostInfoCompatibility accepts both current and earlier workspace responses.
func TestHostInfoCompatibility(t *testing.T) {
	if got := (Info{Dev: "/old"}).WorkspacePath(); got != "/old" {
		t.Fatal(got)
	}
	if got := (Info{Workspace: "/new", Dev: "/old"}).WorkspacePath(); got != "/new" {
		t.Fatal(got)
	}
}
