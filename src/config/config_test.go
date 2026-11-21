package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfigValidation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.toml")
	for _, data := range []string{`shell = "relative"`, `listen = "0.0.0.0:nope"`, `programs = []`, `unknown = true`, `[prompt]
style = "zsh"`, "[hosts]\nx='https://x'\nX='https://y'"} {
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := Read(path); err == nil {
			t.Errorf("accepted %s", data)
		}
	}
}

func TestReadPromptStyle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.toml")
	if err := os.WriteFile(path, []byte("[prompt]\nstyle = \"inherit\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Prompt.Style != "inherit" {
		t.Fatalf("got %q", cfg.Prompt.Style)
	}
}

func TestReadPrograms(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.toml")
	if err := os.WriteFile(path, []byte(`serve = false
programs = ["codex"]`), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Serve == nil || *cfg.Serve {
		t.Fatal("serve setting not applied")
	}
	if !cfg.Enabled("codex") || cfg.Enabled("opencode") {
		t.Fatal("program selection not applied")
	}
}
