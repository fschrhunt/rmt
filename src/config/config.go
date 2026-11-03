// Package config reads the user's host and connection settings.
package config

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/BurntSushi/toml"
	"golang.org/x/sys/unix"
)

var programName = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)

// Config selects host listeners, native programs, and named remote addresses.
type Config struct {
	Serve    *bool             `toml:"serve,omitempty"`
	Programs []string          `toml:"programs,omitempty"`
	Listen   string            `toml:"listen,omitempty"`
	Shell    string            `toml:"shell,omitempty"`
	Hosts    map[string]string `toml:"hosts,omitempty"`
}

// Enabled reports whether a host serves a program; missing settings enable both.
func (c Config) Enabled(program string) bool {
	if c.Programs == nil {
		return program == "opencode" || program == "codex"
	}
	for _, name := range c.Programs {
		if name == program {
			return true
		}
	}
	return false
}

// Path returns the per-user settings path and home directory. Any pre-~/.rmt
// installation is migrated into ~/.rmt on first use.
func Path() (string, string, error) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", "", errors.New("cannot determine home directory")
	}
	root := filepath.Join(home, ".rmt")
	if err := migrate(legacyDir(home), root); err != nil {
		return "", "", err
	}
	if info, err := os.Lstat(root); err == nil {
		owner, ok := info.Sys().(*syscall.Stat_t)
		if !info.IsDir() || info.Mode().Perm()&0077 != 0 || !ok || owner.Uid != uint32(os.Getuid()) {
			return "", "", fmt.Errorf("%s must be an owner-only directory owned by this user", root)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", "", err
	}
	return filepath.Join(root, "settings.toml"), home, nil
}

// Dir returns ~/.rmt, which holds settings, identity, device and host files flat.
func Dir() (string, error) {
	path, _, err := Path()
	if err != nil {
		return "", err
	}
	return filepath.Dir(path), nil
}

// legacyDir is where rmt kept its files before ~/.rmt: $XDG_CONFIG_HOME/rmt
// when that is an absolute path, otherwise ~/.config/rmt.
func legacyDir(home string) string {
	root := os.Getenv("XDG_CONFIG_HOME")
	if !filepath.IsAbs(root) {
		root = filepath.Join(home, ".config")
	}
	return filepath.Join(root, "rmt")
}

// migrate moves the regular files of a legacy rmt directory into dir, renaming
// config.toml to settings.toml. File contents and modes are preserved, so
// existing identities and paired devices keep working. It never overwrites a
// different file; identical copies are accepted after an interrupted migration.
// Sockets stay behind and the old service must stop before migration.
func migrate(legacy, dir string) error {
	if legacy == dir {
		return nil
	}
	info, err := os.Lstat(legacy)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("migrate %s: %w", legacy, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("migrate %s: not a directory; move it aside or migrate to %s by hand", legacy, dir)
	}
	if conn, err := net.DialTimeout("unix", filepath.Join(legacy, "host.sock"), 200*time.Millisecond); err == nil {
		conn.Close()
		return fmt.Errorf("the old rmt host service is running; stop it before migrating %s to %s", legacy, dir)
	}
	if info.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("migrate %s: directory must be owner-only", legacy)
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("migrate %s: %w", dir, err)
	}
	if info, err := os.Lstat(dir); err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("migrate %s: destination must be an owner-only directory", dir)
	}
	lock, err := os.OpenFile(filepath.Join(dir, ".migration.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return fmt.Errorf("lock migration: %w", err)
	}
	defer lock.Close()
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX); err != nil {
		return fmt.Errorf("lock migration: %w", err)
	}
	defer unix.Flock(int(lock.Fd()), unix.LOCK_UN)
	entries, err := os.ReadDir(legacy)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("migrate %s: %w", legacy, err)
	}
	moves := map[string]string{}
	for _, entry := range entries {
		mode := entry.Type()
		if mode&os.ModeSocket != 0 {
			continue
		}
		if !mode.IsRegular() {
			return fmt.Errorf("migrate %s: unexpected non-file %q; remove it or migrate to %s by hand", legacy, entry.Name(), dir)
		}
		target := entry.Name()
		if target == "config.toml" {
			target = "settings.toml"
		}
		if _, err := os.Lstat(filepath.Join(dir, target)); err == nil {
			same, err := sameFile(filepath.Join(legacy, entry.Name()), filepath.Join(dir, target))
			if err != nil || !same {
				return fmt.Errorf("migrate %s: %s already exists with different contents or permissions", legacy, filepath.Join(dir, target))
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("migrate %s: %w", legacy, err)
		}
		moves[entry.Name()] = target
	}
	if len(moves) == 0 {
		return nil
	}
	var created []string
	for name, target := range moves {
		dst := filepath.Join(dir, target)
		if _, err := os.Lstat(dst); err == nil {
			continue
		}
		if err := copyFile(filepath.Join(legacy, name), dst); err != nil {
			for _, path := range created {
				_ = os.Remove(path)
			}
			return fmt.Errorf("migrate %s: %w", name, err)
		}
		created = append(created, dst)
	}
	if err := syncDirectory(dir); err != nil {
		return fmt.Errorf("sync migrated files: %w", err)
	}
	for name := range moves {
		if err := os.Remove(filepath.Join(legacy, name)); err != nil {
			return fmt.Errorf("remove migrated %s: %w", name, err)
		}
	}
	if err := syncDirectory(legacy); err != nil {
		return fmt.Errorf("sync legacy directory: %w", err)
	}
	_ = os.Remove(legacy) // only succeeds when nothing is left behind
	return nil
}

// sameFile recognizes a completed copy after an interrupted migration.
func sameFile(a, b string) (bool, error) {
	var hashes [2][sha256.Size]byte
	var modes [2]os.FileMode
	for i, path := range []string{a, b} {
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() {
			return false, errors.New("migration file is not regular")
		}
		modes[i] = info.Mode().Perm()
		file, err := os.Open(path)
		if err != nil {
			return false, err
		}
		hash := sha256.New()
		_, err = io.Copy(hash, file)
		_ = file.Close()
		if err != nil {
			return false, err
		}
		copy(hashes[i][:], hash.Sum(nil))
	}
	return modes[0] == modes[1] && hashes[0] == hashes[1], nil
}

// copyFile commits a synced complete copy without replacing another file.
func copyFile(src, dst string) (err error) {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.CreateTemp(filepath.Dir(dst), ".migrating-")
	if err != nil {
		return err
	}
	temp := out.Name()
	defer os.Remove(temp)
	defer func() {
		if cerr := out.Close(); err == nil {
			err = cerr
		}
	}()
	if err := out.Chmod(info.Mode().Perm()); err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	if err := out.Sync(); err != nil {
		return err
	}
	return os.Link(temp, dst)
}

// syncDirectory persists file additions or removals before the next migration stage.
func syncDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

// Read loads and validates settings; a missing file uses defaults.
func Read(path string) (Config, error) {
	c := Config{}
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return c, fmt.Errorf("read config: %w", err)
	}
	if err == nil {
		metadata, err := toml.Decode(string(data), &c)
		if err != nil {
			return c, fmt.Errorf("invalid config %s: %w", path, err)
		}
		if keys := metadata.Undecoded(); len(keys) > 0 {
			return c, fmt.Errorf("invalid config %s: unknown setting %s", path, keys[0])
		}
	}
	if c.Shell != "" && !filepath.IsAbs(c.Shell) {
		return c, fmt.Errorf("invalid config %s: shell must be an absolute path", path)
	}
	if c.Programs != nil {
		if len(c.Programs) == 0 {
			return c, fmt.Errorf("invalid config %s: select at least one program", path)
		}
		seen := map[string]bool{}
		for _, name := range c.Programs {
			if !programName.MatchString(name) || seen[name] {
				return c, fmt.Errorf("invalid config %s: unsupported or duplicate program %q", path, name)
			}
			seen[name] = true
		}
	}
	if c.Listen != "" {
		_, port, err := net.SplitHostPort(c.Listen)
		n, parseErr := strconv.Atoi(port)
		if err != nil || parseErr != nil || n < 1 || n > 65535 {
			return c, fmt.Errorf("invalid config %s: listen must be an address with a valid port", path)
		}
	}
	names := map[string]bool{}
	for name, address := range c.Hosts {
		lower := strings.ToLower(name)
		if name == "" || strings.TrimSpace(name) != name || strings.TrimSpace(address) != address || address == "" || names[lower] {
			return c, fmt.Errorf("invalid config %s: invalid or duplicate host name", path)
		}
		names[lower] = true
	}
	return c, nil
}

// Load reads this user's effective settings.
func Load() (Config, error) {
	path, _, err := Path()
	if err != nil {
		return Config{}, err
	}
	return Read(path)
}
