package client

import (
	"github.com/fschrhunt/rmt/src/network"
	"testing"
)

func TestRenamedHostKeepsIdentity(t *testing.T) {
	profiles := map[string]network.Profile{"old": {URL: "https://example.test:47473", Token: "saved"}}
	got, ok, err := findProfile(profiles, "new", "https://example.test:47473/")
	if err != nil || !ok || got.Token != "saved" {
		t.Fatalf("saved identity not found: %v", err)
	}
}
