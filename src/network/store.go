// Package network secures paired connections over any reachable IP network.
package network

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Profile holds a pinned host and its device credential; it belongs in a private file.
type Profile struct {
	URL         string `json:"url"`
	Fingerprint string `json:"fingerprint"`
	Token       string `json:"token"`
}

// randomToken produces an unpredictable URL-safe credential.
func randomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// privateDir creates a private storage directory and refuses unsafe permissions.
func privateDir(dir string) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("%s must be an owner-only directory", dir)
	}
	return nil
}

// PrepareStorage ensures private credentials can be saved before pairing begins.
func PrepareStorage(dir string) error { return privateDir(dir) }

// readPrivate reads an owner-only regular file without following a symlink.
func readPrivate(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return nil, fmt.Errorf("%s must be an owner-only regular file", path)
	}
	return os.ReadFile(path)
}

// writePrivate atomically saves an owner-only file and refuses symlinks.
func writePrivate(path string, data []byte) error {
	if err := privateDir(filepath.Dir(path)); err != nil {
		return err
	}
	if _, err := readPrivate(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".rmt-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(data)
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

// Profiles reads the user's paired hosts without exposing credentials in shared config.
func Profiles(dir string) (map[string]Profile, error) {
	profiles := map[string]Profile{}
	data, err := readPrivate(filepath.Join(dir, "hosts.json"))
	if errors.Is(err, os.ErrNotExist) {
		return profiles, nil
	}
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(data, &profiles); err != nil || profiles == nil {
		return nil, errors.New("invalid paired hosts file")
	}
	return profiles, nil
}

// SaveProfile saves a paired host under a simple name without replacing an existing host.
func SaveProfile(dir, name string, profile Profile) error {
	name = strings.ToLower(name)
	if err := ValidateName(name); err != nil {
		return err
	}
	profiles, err := Profiles(dir)
	if err != nil {
		return err
	}
	if _, ok := profiles[name]; ok {
		return errors.New("a paired host already uses that name")
	}
	profiles[name] = profile
	data, err := json.MarshalIndent(profiles, "", "  ")
	if err != nil {
		return err
	}
	return writePrivate(filepath.Join(dir, "hosts.json"), append(data, '\n'))
}

// ValidateName accepts simple host aliases used on the command line.
func ValidateName(name string) error {
	name = strings.ToLower(name)
	if name == "" || len(name) > 64 {
		return errors.New("host name must be 1 to 64 letters, digits, dots, or hyphens")
	}
	for _, c := range name {
		if !(c >= 'a' && c <= 'z') && !(c >= '0' && c <= '9') && c != '-' && c != '.' {
			return errors.New("host name must use letters, digits, dots, or hyphens")
		}
	}
	return nil
}

// RemoveProfile removes one local host profile without changing the remote device registry.
func RemoveProfile(dir, name string) error {
	profiles, err := Profiles(dir)
	if err != nil {
		return err
	}
	name = strings.ToLower(name)
	if _, ok := profiles[name]; !ok {
		return errors.New("paired host not found")
	}
	delete(profiles, name)
	data, err := json.MarshalIndent(profiles, "", "  ")
	if err != nil {
		return err
	}
	return writePrivate(filepath.Join(dir, "hosts.json"), append(data, '\n'))
}
