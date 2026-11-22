package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fschrhunt/rmt/src/network"
)

// TestStatusOnFreshMachineDoesNotCreateState covers the read-only status path.
func TestStatusOnFreshMachineDoesNotCreateState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("PATH", "")
	var out bytes.Buffer
	if err := status("test", &out); err != nil || !strings.Contains(out.String(), "Local host: not running") {
		t.Fatalf("status = %q: %v", &out, err)
	}
	if _, err := os.Lstat(filepath.Join(home, ".rmt")); !os.IsNotExist(err) {
		t.Fatalf("status created configuration state: %v", err)
	}
}

// TestKnownHostsShowsAddressesWithoutCredentials checks aliases and pairing-state labels.
func TestKnownHostsShowsAddressesWithoutCredentials(t *testing.T) {
	var out bytes.Buffer
	printKnownHosts(&out, map[string]string{
		"mini": "https://mini:47473",
		"fat":  "https://secret:password@fat:47473",
	}, map[string]network.Profile{
		"old-mini": {URL: "https://mini:47473", Token: "bearer-secret", Fingerprint: "fingerprint-secret"},
	})
	got := out.String()
	if strings.Contains(got, "secret") || strings.Contains(got, "password") ||
		!strings.Contains(got, "  mini  https://mini:47473  (paired)") ||
		!strings.Contains(got, "  fat  (invalid address)  (not checked)") {
		t.Fatalf("known hosts = %q", got)
	}
}
