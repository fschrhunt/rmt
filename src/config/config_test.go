package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestWorkspaceConfig checks the default and an independently configured workspace.
func TestWorkspaceConfig(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".config", "rmt", "config.json")
	cfg, err := Read(path, home)
	if err != nil || cfg.Workspace != filepath.Join(home, "dev") {
		t.Fatalf("default workspace: %+v, %v", cfg, err)
	}
	workspace := filepath.Join(home, "projects")
	if err := os.Mkdir(workspace, 0700); err != nil {
		t.Fatal(err)
	}
	if err := SetWorkspace(path, home, "~/projects"); err != nil {
		t.Fatal(err)
	}
	cfg, err = Read(path, home)
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
	if err := SetWorkspace(path, home, home); err == nil {
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
	if err := SetWorkspace(path, home, filepath.Join(home, "missing")); err == nil {
		t.Fatal("expected missing directory error")
	}
	if _, err := WorkspacePath("relative", home); err == nil {
		t.Fatal("expected relative config path error")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("unexpected config file: %v", err)
	}
}

// TestAgentDefaultsAndValidation keeps older configuration compatible and rejects typos.
func TestAgentDefaultsAndValidation(t *testing.T) {
	c := Config{}
	if !c.Enabled("opencode") || !c.Enabled("codex") {
		t.Fatal("older configs must enable both agents")
	}
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"agents":["codex","unknown"]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(path, t.TempDir()); err == nil {
		t.Fatal("expected invalid agent error")
	}
}
