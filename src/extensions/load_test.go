package extensions

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, code := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(code), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestLoadEmptyAndMissingDirectory(t *testing.T) {
	for _, dir := range []string{t.TempDir(), filepath.Join(t.TempDir(), "missing")} {
		r, err := Load(dir)
		if err != nil || len(r.Programs()) != 0 || r.Has("x") {
			t.Fatalf("Load(%q) = %v, %v", dir, r, err)
		}
	}
}

// TestLoadRejectsWritableOrLinkedExtensions protects trusted host-side callbacks.
func TestLoadRejectsWritableOrLinkedExtensions(t *testing.T) {
	dir := write(t, map[string]string{"unsafe.lua": `rmt.on("prepare", function() end)`})
	file := filepath.Join(dir, "unsafe.lua")
	if err := os.Chmod(file, 0666); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil {
		t.Fatal("accepted writable extension")
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "target.lua")
	if err := os.WriteFile(target, []byte(`rmt.on("prepare", function() end)`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, file); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil {
		t.Fatal("accepted linked extension")
	}
}

func TestLoadRejectsInvalidExtensions(t *testing.T) {
	cases := map[string]string{
		"syntax":         `rmt.program(`,
		"runtime":        `error("kaboom")`,
		"bad name":       `rmt.program({name="Bad Name", host=function() end})`,
		"no callbacks":   `rmt.program({name="p"})`,
		"unknown field":  `rmt.program({name="p", host=function() end, extra=1})`,
		"duplicate":      `rmt.program({name="p", host=function() end}) rmt.program({name="p", client=function() end})`,
		"unknown event":  `rmt.on("launch", function() end)`,
		"non-func event": `rmt.on("prepare", 1)`,
	}
	for name, code := range cases {
		if _, err := Load(write(t, map[string]string{"x.lua": code})); err == nil || !strings.Contains(err.Error(), "x.lua") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	dir := write(t, map[string]string{
		"a.lua": `rmt.program({name="p", host=function() end})`,
		"b.lua": `rmt.program({name="p", host=function() end})`,
	})
	if _, err := Load(dir); err == nil || !strings.Contains(err.Error(), "b.lua") {
		t.Errorf("duplicate across files: %v", err)
	}
}
