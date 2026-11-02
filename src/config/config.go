// Package config reads the user's host and connection settings.
package config

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

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

// Path returns the per-user configuration path and home directory.
func Path() (string, string, error) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", "", errors.New("cannot determine home directory")
	}
	root := os.Getenv("XDG_CONFIG_HOME")
	if root == "" {
		root = filepath.Join(home, ".config")
	}
	if !filepath.IsAbs(root) {
		return "", "", errors.New("XDG_CONFIG_HOME must be an absolute path")
	}
	return filepath.Join(root, "rmt", "config.toml"), home, nil
}

// Dir returns the directory containing the user's rmt configuration.
func Dir() (string, error) {
	path, _, err := Path()
	if err != nil {
		return "", err
	}
	return filepath.Dir(path), nil
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
			if (name != "opencode" && name != "codex") || seen[name] {
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
