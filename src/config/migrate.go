package config

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

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
