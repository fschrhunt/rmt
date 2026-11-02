package rmt

import (
	"os"
	"path/filepath"
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
