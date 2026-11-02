// Configuration selects the workspace used by an rmt host.
package rmt

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// config contains the host settings stored in the user's rmt config file.
type config struct {
	Workspace string   `json:"workspace"`
	Agents    []string `json:"agents,omitempty"`
	Listen    string   `json:"listen,omitempty"`
}

// enabled reports whether a host serves an agent; older configs enable both.
func (c config) enabled(agent string) bool {
	if c.Agents == nil {
		return agent == "opencode" || agent == "codex"
	}
	for _, name := range c.Agents {
		if name == agent {
			return true
		}
	}
	return false
}

// configPath returns the per-user configuration path and home directory.
func configPath() (string, string, error) {
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
	return filepath.Join(root, "rmt", "config.json"), home, nil
}

// workspacePath resolves an absolute workspace path, expanding the current user's home.
func workspacePath(value, home string) (string, error) {
	if value == "" {
		return filepath.Join(home, "dev"), nil
	}
	if value == "~" {
		value = home
	} else if strings.HasPrefix(value, "~/") {
		value = filepath.Join(home, value[2:])
	}
	if !filepath.IsAbs(value) {
		return "", errors.New("workspace must be an absolute path or start with ~/")
	}
	return filepath.Clean(value), nil
}

// readConfig loads the host workspace or uses ~/dev when no config file exists.
func readConfig(path, home string) (config, error) {
	c := config{}
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return c, fmt.Errorf("read config: %w", err)
	}
	if err == nil {
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		if err = decoder.Decode(&c); err != nil {
			return c, fmt.Errorf("invalid config %s: %w", path, err)
		}
		if err = decoder.Decode(&struct{}{}); err != io.EOF {
			return c, fmt.Errorf("invalid config %s: unexpected trailing data", path)
		}
	}
	c.Workspace, err = workspacePath(c.Workspace, home)
	if err != nil {
		return c, fmt.Errorf("invalid config %s: %w", path, err)
	}
	if c.Agents != nil {
		if len(c.Agents) == 0 {
			return c, fmt.Errorf("invalid config %s: select at least one agent", path)
		}
		seen := map[string]bool{}
		for _, agent := range c.Agents {
			if (agent != "opencode" && agent != "codex") || seen[agent] {
				return c, fmt.Errorf("invalid config %s: unsupported or duplicate agent %q", path, agent)
			}
			seen[agent] = true
		}
	}
	if c.Listen != "" {
		_, port, err := net.SplitHostPort(c.Listen)
		n, parseErr := strconv.Atoi(port)
		if err != nil || parseErr != nil || n < 1 || n > 65535 {
			return c, fmt.Errorf("invalid config %s: listen must be an address with a valid port", path)
		}
	}
	return c, nil
}

// loadConfig reads this user's effective host configuration.
func loadConfig() (config, error) {
	path, home, err := configPath()
	if err != nil {
		return config{}, err
	}
	return readConfig(path, home)
}

// setWorkspace safely replaces the workspace setting after checking the directory exists.
func setWorkspace(path, home, value string) error {
	c, err := readConfig(path, home)
	if err != nil {
		return err
	}
	return saveConfig(path, home, value, c.Agents, c.Listen)
}

// saveConfig validates and atomically saves host workspace, agents and network settings.
func saveConfig(path, home, value string, agents []string, listen string) error {
	if value == "" {
		return errors.New("workspace cannot be empty")
	}
	if value != "~" && !strings.HasPrefix(value, "~/") && !filepath.IsAbs(value) {
		var err error
		value, err = filepath.Abs(value)
		if err != nil {
			return err
		}
	}
	workspace, err := workspacePath(value, home)
	if err != nil {
		return err
	}
	info, err := os.Stat(workspace)
	if err != nil {
		return fmt.Errorf("workspace %s: %w", workspace, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("workspace %s is not a directory", workspace)
	}
	if _, err := readConfig(path, home); err != nil {
		return err
	}
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return errors.New("config file is a symlink; edit it directly")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	if listen != "" {
		_, port, err := net.SplitHostPort(listen)
		n, parseErr := strconv.Atoi(port)
		if err != nil || parseErr != nil || n < 1 || n > 65535 {
			return errors.New("listen must be an address with a valid port")
		}
	}
	data, err := json.MarshalIndent(config{Workspace: workspace, Agents: agents, Listen: listen}, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	file, err := os.CreateTemp(filepath.Dir(path), ".config-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if err = file.Chmod(0600); err == nil {
		_, err = file.Write(data)
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

// configCommand prints the effective workspace or sets it for the next host start.
func configCommand(args []string) error {
	path, home, err := configPath()
	if err != nil {
		return err
	}
	if len(args) == 0 {
		c, err := readConfig(path, home)
		if err != nil {
			return err
		}
		agents := c.Agents
		if agents == nil {
			agents = []string{"opencode", "codex"}
		}
		listen := c.Listen
		if listen == "" {
			listen = "off"
		}
		fmt.Printf("workspace: %s\nagents:    %s\nlisten:    %s\nconfig:    %s\n", c.Workspace, strings.Join(agents, ", "), listen, path)
		return nil
	}
	if len(args) != 2 || args[0] != "set" {
		return errors.New("usage: rmt config [set <workspace>]")
	}
	if err := setWorkspace(path, home, args[1]); err != nil {
		return err
	}
	c, err := readConfig(path, home)
	if err != nil {
		return err
	}
	fmt.Printf("Workspace set to %s. Restart the host service when it is safe to do so.\n", c.Workspace)
	return nil
}
