package services

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// systemdQuote escapes a value for a systemd unit, including specifier expansion.
func systemdQuote(value string) string { return strconv.Quote(strings.ReplaceAll(value, "%", "%%")) }

// systemdUnit describes a user service using the installed binary and PATH.
func systemdUnit(binary, path string, paths map[string]string) string {
	configEnv := ""
	keys := make([]string, 0, len(paths))
	for key := range paths {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		configEnv += "Environment=" + systemdQuote(key+"="+paths[key]) + "\n"
	}
	return fmt.Sprintf("[Unit]\nDescription=rmt host\nAfter=network-online.target\n\n[Service]\nExecStart=%s --serve\nEnvironment=%s\n%sRestart=on-failure\nRestartSec=5\n\n[Install]\nWantedBy=default.target\n", systemdQuote(strings.ReplaceAll(binary, "$", "$$")), systemdQuote("PATH="+path), configEnv)
}
