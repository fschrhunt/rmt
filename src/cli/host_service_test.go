package cli

import (
	"path/filepath"
	"testing"

	"github.com/fschrhunt/rmt/src/config"
)

// TestStartLocalHostDefaultsOn verifies that a first connection requests host setup.
func TestStartLocalHostDefaultsOn(t *testing.T) {
	called := false
	err := startLocalHost(t.TempDir(), config.Config{}, func() error { called = true; return nil })
	if err != nil || !called {
		t.Fatalf("setup called = %v, error = %v", called, err)
	}
}

// TestStartLocalHostHonorsOptOut verifies both a saved disable and legacy client-only settings.
func TestStartLocalHostHonorsOptOut(t *testing.T) {
	root := filepath.Join(t.TempDir(), ".rmt")
	if err := rememberHostDisabled(root); err != nil {
		t.Fatal(err)
	}
	if err := rememberHostDisabled(root); err != nil {
		t.Fatal(err)
	}
	noSetup := func() error { t.Fatal("an opted-out client tried to enable hosting"); return nil }
	if err := startLocalHost(root, config.Config{}, noSetup); err != nil {
		t.Fatal(err)
	}
	disabled := false
	if err := startLocalHost(t.TempDir(), config.Config{Serve: &disabled}, noSetup); err != nil {
		t.Fatal(err)
	}
}
