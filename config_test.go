package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWorkspaceConfig checks the default and an independently configured workspace.
func TestWorkspaceConfig(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".config", "rmt", "config.json")
	cfg, err := readConfig(path, home)
	if err != nil || cfg.Workspace != filepath.Join(home, "dev") {
		t.Fatalf("default workspace: %+v, %v", cfg, err)
	}
	workspace := filepath.Join(home, "projects")
	if err := os.Mkdir(workspace, 0700); err != nil {
		t.Fatal(err)
	}
	if err := setWorkspace(path, home, "~/projects"); err != nil {
		t.Fatal(err)
	}
	cfg, err = readConfig(path, home)
	if err != nil || cfg.Workspace != workspace {
		t.Fatalf("configured workspace: %+v, %v", cfg, err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("config permissions: %v, %v", info, err)
	}
}

// TestInvalidConfigIsPreserved prevents configuration updates from silently discarding data.
func TestInvalidConfigIsPreserved(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "config.json")
	original := []byte(`{"workspace":"/tmp","unknown":true}`)
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	if err := setWorkspace(path, home, home); err == nil {
		t.Fatal("expected unknown config field to fail")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != string(original) {
		t.Fatalf("config changed: %q, %v", data, err)
	}
}

// TestConfigValidation rejects invalid workspaces without writing configuration.
func TestConfigValidation(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "config.json")
	if err := setWorkspace(path, home, filepath.Join(home, "missing")); err == nil {
		t.Fatal("expected missing directory error")
	}
	if _, err := workspacePath("relative", home); err == nil {
		t.Fatal("expected relative config path error")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("unexpected config file: %v", err)
	}
}

// TestOpenCodeClientNeedsNoLocalWorkspace checks that a client works without ~/dev.
func TestOpenCodeClientNeedsNoLocalWorkspace(t *testing.T) {
	home := t.TempDir()
	bin := filepath.Join(home, "bin")
	if err := os.Mkdir(bin, 0700); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(home, "args")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$RMT_TEST_ARGS\"\n"
	if err := os.WriteFile(filepath.Join(bin, "opencode"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("PATH", bin)
	t.Setenv("RMT_TEST_ARGS", output)
	if err := openCode("127.0.0.1", "/other/workspace", nil); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(output)
	if err != nil || !strings.HasPrefix(string(data), "--server\nhttp://127.0.0.1:") {
		t.Fatalf("client arguments: %q, %v", data, err)
	}
}

// TestHostInfoCompatibility accepts both current and earlier workspace responses.
func TestHostInfoCompatibility(t *testing.T) {
	if got := (hostInfo{Dev: "/old"}).workspace(); got != "/old" {
		t.Fatal(got)
	}
	if got := (hostInfo{Workspace: "/new", Dev: "/old"}).workspace(); got != "/new" {
		t.Fatal(got)
	}
}
