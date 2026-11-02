// Package services installs the host with the user service manager.
package services

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// nativePaths retains only non-secret paths needed to locate native program settings.
func nativePaths() map[string]string {
	result := map[string]string{}
	for _, name := range []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME", "CODEX_HOME", "OPENCODE_CONFIG_DIR", "OPENCODE_CONFIG", "OPENCODE_DB"} {
		if value := os.Getenv(name); value != "" {
			result[name] = value
		}
	}
	return result
}

// Install installs and starts a user service without replacing an existing one.
// address is the host's control address, used to avoid starting a duplicate.
func Install(address string) error {
	conn, err := net.DialTimeout("tcp", address, 300*time.Millisecond)
	if err == nil {
		conn.Close()
		return nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	if rel, err := filepath.Rel(os.TempDir(), home); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil
	}
	binary, err := os.Executable()
	if err != nil {
		return err
	}
	binary, err = filepath.EvalSymlinks(binary)
	if err != nil {
		return err
	}
	if rel, err := filepath.Rel(os.TempDir(), binary); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil
	}
	// Prefer a stable installed command symlink over a versioned package-manager target.
	if installed, err := exec.LookPath("rmt"); err == nil {
		if installed, err = filepath.Abs(installed); err == nil {
			if target, err := filepath.EvalSymlinks(installed); err == nil && target == binary {
				binary = installed
			}
		}
	}
	var servicePath, content string
	switch runtime.GOOS {
	case "darwin":
		servicePath = filepath.Join(home, "Library", "LaunchAgents", "com.rmt.host.plist")
		logDir := filepath.Join(home, "Library", "Logs", "rmt")
		if err := os.MkdirAll(logDir, 0700); err != nil {
			return err
		}
		logPath := filepath.Join(logDir, "host.log")
		if info, err := os.Lstat(logPath); err == nil && info.Mode()&os.ModeSymlink != 0 {
			return errors.New("host log is a symlink; choose a regular log file")
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		if err := logFile.Close(); err != nil {
			return err
		}
		content = launchAgent(binary, os.Getenv("PATH"), nativePaths(), logPath)
	case "linux":
		root := os.Getenv("XDG_CONFIG_HOME")
		if root == "" {
			root = filepath.Join(home, ".config")
		}
		if !filepath.IsAbs(root) {
			return errors.New("XDG_CONFIG_HOME must be an absolute path")
		}
		servicePath = filepath.Join(root, "systemd", "user", "rmt.service")
		content = systemdUnit(binary, os.Getenv("PATH"), nativePaths())
	default:
		return fmt.Errorf("automatic service installation is not supported on %s", runtime.GOOS)
	}
	if err := os.MkdirAll(filepath.Dir(servicePath), 0700); err != nil {
		return err
	}
	file, err := os.OpenFile(servicePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err == nil {
		_, err = file.WriteString(content)
		if closeErr := file.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			_ = os.Remove(servicePath)
			return err
		}
	} else if !errors.Is(err, os.ErrExist) {
		return err
	}
	run := func(name string, args ...string) error {
		output, err := exec.Command(name, args...).CombinedOutput()
		if err != nil {
			return fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(string(output)))
		}
		return nil
	}
	if runtime.GOOS == "darwin" {
		label := fmt.Sprintf("gui/%d/com.rmt.host", os.Getuid())
		if exec.Command("launchctl", "print", label).Run() == nil {
			return nil
		}
		err = run("launchctl", "bootstrap", fmt.Sprintf("gui/%d", os.Getuid()), servicePath)
	} else {
		if err = run("systemctl", "--user", "daemon-reload"); err == nil {
			err = run("systemctl", "--user", "enable", "--now", "rmt.service")
		}
	}
	if err != nil {
		return fmt.Errorf("could not start the host service at %s: %w", servicePath, err)
	}
	return nil
}
