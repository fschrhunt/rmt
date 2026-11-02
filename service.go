// Service installation starts rmt at login using the platform's user service manager.
package main

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// xmlValue escapes a user-controlled path or environment value for a launchd plist.
func xmlValue(value string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(value))
	return b.String()
}

// launchAgent describes an rmt service without inheriting unrelated shell secrets.
func launchAgent(binary, path, configHome, logPath string) string {
	configEnv := ""
	if configHome != "" {
		configEnv = "<key>XDG_CONFIG_HOME</key><string>" + xmlValue(configHome) + "</string>"
	}
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>Label</key><string>com.rmt.host</string>
<key>ProgramArguments</key><array><string>%s</string><string>serve</string></array>
<key>EnvironmentVariables</key><dict><key>PATH</key><string>%s</string>%s</dict>
<key>RunAtLoad</key><true/><key>KeepAlive</key><true/>
<key>StandardErrorPath</key><string>%s</string>
</dict></plist>
`, xmlValue(binary), xmlValue(path), configEnv, xmlValue(logPath))
}

// systemdQuote escapes a value for a systemd unit, including specifier expansion.
func systemdQuote(value string) string { return strconv.Quote(strings.ReplaceAll(value, "%", "%%")) }

// systemdUnit describes a user service using the installed binary and PATH.
func systemdUnit(binary, path, configHome string) string {
	configEnv := ""
	if configHome != "" {
		configEnv = "Environment=" + systemdQuote("XDG_CONFIG_HOME="+configHome) + "\n"
	}
	return fmt.Sprintf("[Unit]\nDescription=rmt agent host\nAfter=network-online.target\n\n[Service]\nExecStart=%s serve\nEnvironment=%s\n%sRestart=on-failure\nRestartSec=5\n\n[Install]\nWantedBy=default.target\n", systemdQuote(strings.ReplaceAll(binary, "$", "$$")), systemdQuote("PATH="+path), configEnv)
}

// installService installs and starts a new user service without replacing an existing one.
func installService() error {
	s, err := tailnet()
	if err != nil || ipv4(s.Self) == "" || s.Self.UserID == 0 {
		return errors.New("connect Tailscale before installing the host service")
	}
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(ipv4(s.Self), controlPort), 300*time.Millisecond)
	if err == nil {
		conn.Close()
		return errors.New("a host is already listening; leave its existing service running")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
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
		return errors.New("install the rmt executable before installing a service")
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
		content = launchAgent(binary, os.Getenv("PATH"), os.Getenv("XDG_CONFIG_HOME"), logPath)
	case "linux":
		root := os.Getenv("XDG_CONFIG_HOME")
		if root == "" {
			root = filepath.Join(home, ".config")
		}
		if !filepath.IsAbs(root) {
			return errors.New("XDG_CONFIG_HOME must be an absolute path")
		}
		servicePath = filepath.Join(root, "systemd", "user", "rmt.service")
		content = systemdUnit(binary, os.Getenv("PATH"), os.Getenv("XDG_CONFIG_HOME"))
	default:
		return fmt.Errorf("automatic service installation is not supported on %s", runtime.GOOS)
	}
	if err := os.MkdirAll(filepath.Dir(servicePath), 0700); err != nil {
		return err
	}
	file, err := os.OpenFile(servicePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if errors.Is(err, os.ErrExist) {
		return fmt.Errorf("service file already exists at %s; it was not changed", servicePath)
	}
	if err != nil {
		return err
	}
	_, err = file.WriteString(content)
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(servicePath)
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
		err = run("launchctl", "bootstrap", fmt.Sprintf("gui/%d", os.Getuid()), servicePath)
	} else {
		if err = run("systemctl", "--user", "daemon-reload"); err == nil {
			err = run("systemctl", "--user", "enable", "--now", "rmt.service")
		}
	}
	if err != nil {
		return fmt.Errorf("service file saved at %s, but could not start it: %w", servicePath, err)
	}
	return nil
}
