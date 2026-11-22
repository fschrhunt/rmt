// Package services manages the rmt host through the user service manager.
package services

import (
	"context"
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

// Registrations lists known service definitions or loaded macOS jobs without changing them.
func Registrations() ([]string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	file, err := definitionPath(home)
	if err != nil {
		return nil, err
	}
	files := []string{file}
	if runtime.GOOS == "darwin" {
		files = append(files, filepath.Join(filepath.Dir(file), "com.fschr.rmt.plist"))
	}
	var found []string
	for _, file := range files {
		_, err := os.Lstat(file)
		name := filepath.Base(file)
		if errors.Is(err, os.ErrNotExist) && runtime.GOOS == "darwin" {
			ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
			label := fmt.Sprintf("gui/%d/%s", os.Getuid(), strings.TrimSuffix(name, ".plist"))
			loaded := exec.CommandContext(ctx, "launchctl", "print", label).Run() == nil
			cancel()
			if loaded {
				found = append(found, name)
			}
		} else if err == nil {
			found = append(found, name)
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
	return found, nil
}

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

// EnableOnFirstUse sets up hosting only when there is no existing service definition.
func EnableOnFirstUse() error { return enable(true) }

// Enable installs and starts the host service without replacing an existing definition.
func Enable() error { return enable(false) }

// definitionPath locates the service file for the current user.
func definitionPath(home string) (string, error) {
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "LaunchAgents", "com.rmt.host.plist"), nil
	case "linux":
		root := os.Getenv("XDG_CONFIG_HOME")
		if root == "" {
			root = filepath.Join(home, ".config")
		}
		if !filepath.IsAbs(root) {
			return "", errors.New("XDG_CONFIG_HOME must be an absolute path")
		}
		return filepath.Join(root, "systemd", "user", "rmt.service"), nil
	default:
		return "", fmt.Errorf("host service installation is not supported on %s", runtime.GOOS)
	}
}

// enable leaves existing registrations entirely alone on the automatic path.
func enable(firstUse bool) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	servicePath, err := definitionPath(home)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(servicePath); err == nil {
		if firstUse {
			return nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	} else {
		legacy, err := legacyHostDefined(home)
		if err != nil {
			return err
		}
		if legacy || hostAlreadyRunning(home) {
			if firstUse {
				return nil
			}
			return errors.New("rmt hosting is already set up outside the managed service; stop it and retire any older service definition before enabling a new one")
		}
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
		return errors.New("install rmt in a permanent directory before enabling the host service")
	}
	// Prefer a stable installed command symlink over a versioned package-manager target.
	if installed, err := exec.LookPath("rmt"); err == nil {
		if installed, err = filepath.Abs(installed); err == nil {
			if target, err := filepath.EvalSymlinks(installed); err == nil && target == binary {
				binary = installed
			}
		}
	}
	var content string
	switch runtime.GOOS {
	case "darwin":
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
		content = systemdUnit(binary, os.Getenv("PATH"), nativePaths())
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
	} else if firstUse {
		return nil
	}
	if runtime.GOOS == "darwin" {
		domain := fmt.Sprintf("gui/%d", os.Getuid())
		label := domain + "/com.rmt.host"
		if err = run("launchctl", "enable", label); err == nil && exec.Command("launchctl", "print", label).Run() != nil {
			err = run("launchctl", "bootstrap", domain, servicePath)
		}
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

// legacyHostDefined recognizes the macOS LaunchAgent used before com.rmt.host.
func legacyHostDefined(home string) (bool, error) {
	if runtime.GOOS != "darwin" {
		return false, nil
	}
	_, err := os.Lstat(filepath.Join(home, "Library", "LaunchAgents", "com.fschr.rmt.plist"))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

// hostAlreadyRunning checks rmt's private control socket independently of its service label.
func hostAlreadyRunning(home string) bool {
	conn, err := net.DialTimeout("unix", filepath.Join(home, ".rmt", "host.sock"), 200*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// Disable stops the host and prevents it from starting at login, preserving its files and pairings.
func Disable() error {
	switch runtime.GOOS {
	case "darwin":
		domain := fmt.Sprintf("gui/%d", os.Getuid())
		label := domain + "/com.rmt.host"
		if err := run("launchctl", "disable", label); err != nil {
			return err
		}
		if exec.Command("launchctl", "print", label).Run() == nil {
			return run("launchctl", "bootout", label)
		}
		return nil
	case "linux":
		state, _ := exec.Command("systemctl", "--user", "show", "--property=LoadState", "--value", "rmt.service").Output()
		if strings.TrimSpace(string(state)) == "not-found" {
			return nil
		}
		return run("systemctl", "--user", "disable", "--now", "rmt.service")
	default:
		return fmt.Errorf("host service management is not supported on %s", runtime.GOOS)
	}
}

// run reports service manager failures with their diagnostic output.
func run(name string, args ...string) error {
	if output, err := exec.Command(name, args...).CombinedOutput(); err != nil {
		return fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(string(output)))
	}
	return nil
}
