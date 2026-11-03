package config

import (
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// TestConfigValidation rejects invalid settings before starting a host.
func TestConfigValidation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.toml")
	for _, data := range []string{`shell = "relative"`, `listen = "0.0.0.0:nope"`, `programs = []`, `unknown = true`, "[hosts]\nx='https://x'\nX='https://y'"} {
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := Read(path); err == nil {
			t.Errorf("accepted %s", data)
		}
	}
}

// TestConcurrentMigration preserves a single identity when processes start together.
func TestConcurrentMigration(t *testing.T) {
	root := t.TempDir()
	legacy, dir := filepath.Join(root, "old"), filepath.Join(root, ".rmt")
	if err := os.Mkdir(legacy, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "identity.pem"), []byte("identity"), 0600); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- migrate(legacy, dir) }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile(filepath.Join(dir, "identity.pem"))
	if err != nil || string(got) != "identity" {
		t.Fatalf("identity: %q %v", got, err)
	}
}

// TestReadPrograms checks the new editable configuration field.
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

// TestMigrateMovesLegacyFiles keeps identity, tokens and modes intact, renames
// config.toml, and leaves stale sockets behind.
func TestMigrateMovesLegacyFiles(t *testing.T) {
	root := t.TempDir()
	legacy, dir := filepath.Join(root, "old"), filepath.Join(root, ".rmt")
	// Unix socket paths are short-limited, so the legacy side lives under /tmp.
	short, err := os.MkdirTemp("/tmp", "rmt")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(short)
	legacy = short
	for name, data := range map[string]string{"config.toml": "serve = false", "identity.pem": "key", "devices.json": "{}", "hosts.json": "{}"} {
		if err := os.WriteFile(filepath.Join(legacy, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	ln, err := net.Listen("unix", filepath.Join(legacy, "host.sock"))
	if err != nil {
		t.Fatal(err)
	}
	ln.(*net.UnixListener).SetUnlinkOnClose(false)
	ln.Close()

	if err := migrate(legacy, dir); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]string{"settings.toml": "serve = false", "identity.pem": "key", "devices.json": "{}", "hosts.json": "{}"} {
		got, err := os.ReadFile(filepath.Join(dir, name))
		info, statErr := os.Stat(filepath.Join(dir, name))
		if err != nil || statErr != nil || string(got) != data || info.Mode().Perm() != 0600 {
			t.Errorf("%s not migrated intact: %v %v", name, err, statErr)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "host.sock")); err == nil {
		t.Error("socket was migrated")
	}
	if _, err := os.Stat(filepath.Join(legacy, "config.toml")); err == nil {
		t.Error("legacy file left behind")
	}
	if err := migrate(legacy, dir); err != nil {
		t.Fatalf("repeat migration: %v", err)
	}
}

// TestMigrateRefusesRunningHost prevents two services from writing separate registries.
func TestMigrateRefusesRunningHost(t *testing.T) {
	legacy, err := os.MkdirTemp("/tmp", "rmt-migration-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(legacy)
	if err := os.WriteFile(filepath.Join(legacy, "identity.pem"), []byte("identity"), 0600); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", filepath.Join(legacy, "host.sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	dir := filepath.Join(t.TempDir(), ".rmt")
	if err := migrate(legacy, dir); err == nil {
		t.Fatal("migrated while old service is active")
	}
	if _, err := os.Stat(filepath.Join(legacy, "identity.pem")); err != nil {
		t.Fatal("legacy identity moved")
	}
}

// TestMigrateRefusesConflicts leaves both sides untouched rather than overwrite.
func TestMigrateRefusesConflicts(t *testing.T) {
	root := t.TempDir()
	legacy, dir := filepath.Join(root, "old"), filepath.Join(root, ".rmt")
	for _, d := range []string{legacy, dir} {
		if err := os.Mkdir(d, 0700); err != nil {
			t.Fatal(err)
		}
	}
	for path, data := range map[string]string{
		filepath.Join(legacy, "identity.pem"): "old",
		filepath.Join(legacy, "config.toml"):  "old",
		filepath.Join(dir, "settings.toml"):   "new",
	} {
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := migrate(legacy, dir); err == nil {
		t.Fatal("conflict was not reported")
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "settings.toml")); string(got) != "new" {
		t.Error("destination overwritten")
	}
	if _, err := os.Stat(filepath.Join(dir, "identity.pem")); err == nil {
		t.Error("partial migration after conflict")
	}
}
