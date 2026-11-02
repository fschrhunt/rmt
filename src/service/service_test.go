package service

import (
	"encoding/xml"
	"strings"
	"testing"
)

// TestServiceDefinitionsPreservePaths checks escaping and custom configuration paths.
func TestServiceDefinitionsPreservePaths(t *testing.T) {
	plist := launchAgent(`/Users/me/rmt & tools/rmt`, `/bin:/other & stuff`, `/Users/me/custom config`, `/Users/me/rmt.log`)
	var v any
	if err := xml.Unmarshal([]byte(plist), &v); err != nil {
		t.Fatalf("invalid plist XML: %v", err)
	}
	if !strings.Contains(plist, "rmt &amp; tools") || !strings.Contains(plist, "XDG_CONFIG_HOME") {
		t.Fatal("plist omitted escaped path or config")
	}
	unit := systemdUnit(`/home/me/rmt 100%/$bin`, `/bin:/tools`, `/home/me/custom config`)
	if !strings.Contains(unit, `"/home/me/rmt 100%%/$$bin"`) || !strings.Contains(unit, `"XDG_CONFIG_HOME=/home/me/custom config"`) {
		t.Fatalf("invalid unit: %s", unit)
	}
}
