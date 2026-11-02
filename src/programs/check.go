package programs

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Check verifies that an installed program supports the flags rmt needs for its role.
func Check(name string, host bool) error {
	var args, flags []string
	switch name {
	case "opencode":
		args = []string{"--help"}
		flags = []string{"--server"}
		if host {
			args = []string{"service", "--help"}
			flags = []string{"start", "status", "get"}
		}
	case "codex":
		args = []string{"--help"}
		flags = []string{"--remote", "--remote-auth-token-env"}
		if host {
			args = []string{"app-server", "daemon", "--help"}
			flags = []string{"start"}
		}
	default:
		return fmt.Errorf("unsupported program %q", name)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s is missing or cannot report its capabilities: %w", name, err)
	}
	for _, flag := range flags {
		if !strings.Contains(string(output), flag) {
			return fmt.Errorf("installed %s does not support %s; update it on this machine", name, flag)
		}
	}
	return nil
}

// Version returns an installed program's reported version for mismatch diagnostics.
func Version(name string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, name, "--version").Output()
	if err != nil {
		return ""
	}
	version := strings.TrimSpace(string(output))
	if len(version) > 100 || strings.ContainsAny(version, "\r\n\x1b") {
		return ""
	}
	return version
}
