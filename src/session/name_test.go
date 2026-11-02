package session

import (
	"testing"
)

// TestSessionNameStaysStable protects short names and older reconnect handles.
func TestSessionNameStaysStable(t *testing.T) {
	if got := Name("opencode", "ses_test"); got != "goose" {
		t.Fatalf("session name changed: %s", got)
	}
	sessions := []Session{{Name: "goose", Agent: "opencode", ID: "ses_test"}}
	for _, previous := range []string{"akita-238932e4", "hen6fn"} {
		got, err := Select(sessions, previous, "")
		if err != nil || got.Name != "goose" {
			t.Fatalf("old name did not reconnect: %+v, %v", got, err)
		}
	}
}
