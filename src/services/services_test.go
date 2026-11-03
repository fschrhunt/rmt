package services

import (
	"encoding/xml"
	"strings"
	"testing"
)

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
