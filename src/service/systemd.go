package service

import (
	"fmt"
	"strconv"
	"strings"
)

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
