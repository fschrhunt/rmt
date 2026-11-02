package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSetupSavesSelection verifies setup stores the chosen workspace and agent.
func TestSetupSavesSelection(t *testing.T) {
	home := t.TempDir()
	workspace := filepath.Join(home, "projects")
	if err := os.Mkdir(workspace, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("PATH", t.TempDir())
	var output bytes.Buffer
	if err := setup(strings.NewReader("~/projects\n2\ny\n"), &output); err != nil {
		t.Fatal(err)
	}
	c, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if c.Workspace != workspace || !c.enabled("opencode") || c.enabled("codex") {
		t.Fatalf("unexpected config: %+v", c)
	}
	if !strings.Contains(output.String(), "must be installed") {
		t.Fatal("missing installation guidance")
	}
	if err := setWorkspace(filepath.Join(home, "config", "rmt", "config.json"), home, home); err != nil {
		t.Fatal(err)
	}
	c, err = loadConfig()
	if err != nil || c.enabled("codex") || !c.enabled("opencode") {
		t.Fatalf("changing workspace lost agents: %+v, %v", c, err)
	}
}

// TestSetupCancelLeavesConfigUntouched prevents an abandoned setup from overwriting settings.
func TestSetupCancelLeavesConfigUntouched(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("PATH", t.TempDir())
	var output bytes.Buffer
	if err := setup(strings.NewReader(home+"\n1\nn\n"), &output); err == nil {
		t.Fatal("expected cancellation")
	}
	if _, err := os.Stat(filepath.Join(home, "config", "rmt", "config.json")); !os.IsNotExist(err) {
		t.Fatalf("config unexpectedly written: %v", err)
	}
}

// TestAgentDefaultsAndValidation keeps older configuration compatible and rejects typos.
func TestAgentDefaultsAndValidation(t *testing.T) {
	c := config{}
	if !c.enabled("opencode") || !c.enabled("codex") {
		t.Fatal("older configs must enable both agents")
	}
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"agents":["codex","unknown"]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readConfig(path, t.TempDir()); err == nil {
		t.Fatal("expected invalid agent error")
	}
}
