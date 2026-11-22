package services

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestFirstUseLeavesExistingHostAlone verifies that connecting cannot re-register a service.
func TestFirstUseLeavesExistingHostAlone(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("PATH", filepath.Join(home, "no-service-manager"))
	file, err := definitionPath(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("existing service"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := EnableOnFirstUse(); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(file); err != nil || string(data) != "existing service" {
		t.Fatalf("existing service changed: %q, %v", data, err)
	}
}

// TestDisableStopsManagedHost verifies that disabling hosting also stops the current service.
func TestDisableStopsManagedHost(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	calls := filepath.Join(dir, "calls")
	t.Setenv("RMT_TEST_CALLS", calls)
	var manager string
	var want []string
	switch runtime.GOOS {
	case "darwin":
		manager = "launchctl"
		want = []string{"disable", "print", "bootout"}
	case "linux":
		manager = "systemctl"
		want = []string{"--user show --property=LoadState --value rmt.service", "--user disable --now rmt.service"}
	default:
		t.Skip("unsupported service manager")
	}
	fake := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$RMT_TEST_CALLS\"\n"
	if err := os.WriteFile(filepath.Join(dir, manager), []byte(fake), 0700); err != nil {
		t.Fatal(err)
	}
	if err := Disable(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(got) != len(want) {
		t.Fatalf("service manager calls = %q", got)
	}
	for i := range want {
		if manager == "launchctl" {
			got[i], _, _ = strings.Cut(got[i], " ")
		}
		if got[i] != want[i] {
			t.Fatalf("call %d = %q, want %q", i+1, got[i], want[i])
		}
	}
}

// TestServiceDefinitionsPreservePaths checks escaping and custom configuration paths.
func TestServiceDefinitionsPreservePaths(t *testing.T) {
	plist := launchAgent(`/Users/me/rmt & tools/rmt`, `/bin:/other & stuff`, map[string]string{"XDG_CONFIG_HOME": `/Users/me/custom config`, "CODEX_HOME": `/Users/me/codex`}, `/Users/me/rmt.log`)
	var v any
	if err := xml.Unmarshal([]byte(plist), &v); err != nil {
		t.Fatalf("invalid plist XML: %v", err)
	}
	if !strings.Contains(plist, "rmt &amp; tools") || (!strings.Contains(plist, "XDG_CONFIG_HOME") || !strings.Contains(plist, "CODEX_HOME")) {
		t.Fatal("plist omitted escaped path or config")
	}
	unit := systemdUnit(`/home/me/rmt 100%/$bin`, `/bin:/tools`, map[string]string{"XDG_CONFIG_HOME": `/home/me/custom config`, "CODEX_HOME": `/home/me/codex`})
	if !strings.Contains(unit, `"/home/me/rmt 100%%/$$bin"`) || (!strings.Contains(unit, `"XDG_CONFIG_HOME=/home/me/custom config"`) || !strings.Contains(unit, `"CODEX_HOME=/home/me/codex"`)) {
		t.Fatalf("invalid unit: %s", unit)
	}
}

func TestNativePathsExcludesSecrets(t *testing.T) {
	t.Setenv("CODEX_HOME", "/custom/codex")
	t.Setenv("OPENAI_API_KEY", "secret-value")
	t.Setenv("OPENCODE_PASSWORD", "secret-value")
	paths := nativePaths()
	if paths["CODEX_HOME"] != "/custom/codex" {
		t.Fatal("native config location missing")
	}
	if _, ok := paths["OPENAI_API_KEY"]; ok {
		t.Fatal("copied API credential")
	}
	if _, ok := paths["OPENCODE_PASSWORD"]; ok {
		t.Fatal("copied service password")
	}
}
