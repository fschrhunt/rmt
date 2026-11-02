// Setup guides a user through configuring this machine as an agent host.
package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// setup asks for a workspace and agents, saves them, and offers native sign-in.
func setup(input io.Reader, output io.Writer) error {
	path, home, err := configPath()
	if err != nil {
		return err
	}
	current, err := readConfig(path, home)
	if err != nil {
		return err
	}
	reader := bufio.NewReader(input)
	ask := func(prompt string) (string, error) {
		fmt.Fprint(output, prompt)
		line, err := reader.ReadString('\n')
		if err != nil {
			return "", errors.New("setup cancelled; configuration was not saved")
		}
		return strings.TrimSpace(line), nil
	}
	fmt.Fprintln(output, "Set up rmt on this machine")
	fmt.Fprintln(output)
	if s, err := tailnet(); err != nil || ipv4(s.Self) == "" || s.Self.UserID == 0 {
		fmt.Fprintln(output, "Tailscale: not connected or unavailable (connect it before starting the host)")
	} else {
		fmt.Fprintf(output, "Tailscale: connected as %s\n", s.Self.HostName)
	}
	installed := map[string]bool{}
	for _, name := range []string{"opencode", "codex"} {
		_, err := exec.LookPath(name)
		installed[name] = err == nil
		state := "not found on PATH"
		if installed[name] {
			state = "installed"
		}
		fmt.Fprintf(output, "%s: %s\n", name, state)
	}
	if installed["codex"] {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		help, err := exec.CommandContext(ctx, "codex", "app-server", "--help").Output()
		cancel()
		if err != nil || !strings.Contains(string(help), "--ws-auth") || !strings.Contains(string(help), "--ws-token-sha256") {
			fmt.Fprintln(output, "Codex needs an app-server version with --ws-auth and --ws-token-sha256; update it before serving Codex.")
		}
	}
	fmt.Fprintln(output, "\nThe workspace is where agents start on this machine.")
	var workspace string
	for {
		value, err := ask(fmt.Sprintf("Workspace [%s]: ", current.Workspace))
		if err != nil {
			return err
		}
		if value == "" {
			value = current.Workspace
		}
		if value != "~" && !strings.HasPrefix(value, "~/") && !filepath.IsAbs(value) {
			value, err = filepath.Abs(value)
			if err != nil {
				return err
			}
		}
		workspace, err = workspacePath(value, home)
		if err != nil {
			fmt.Fprintln(output, err)
			continue
		}
		info, err := os.Stat(workspace)
		if err != nil || !info.IsDir() {
			fmt.Fprintln(output, "Choose an existing directory.")
			continue
		}
		break
	}
	defaultChoice := "1"
	if current.enabled("opencode") && !current.enabled("codex") {
		defaultChoice = "2"
	}
	if !current.enabled("opencode") && current.enabled("codex") {
		defaultChoice = "3"
	}
	fmt.Fprintln(output, "\nAgents to serve:\n  1. OpenCode and Codex\n  2. OpenCode\n  3. Codex")
	var agents []string
	for {
		choice, err := ask(fmt.Sprintf("Choose [1-3, default %s]: ", defaultChoice))
		if err != nil {
			return err
		}
		if choice == "" {
			choice = defaultChoice
		}
		switch choice {
		case "1":
			agents = []string{"opencode", "codex"}
		case "2":
			agents = []string{"opencode"}
		case "3":
			agents = []string{"codex"}
		default:
			fmt.Fprintln(output, "Enter 1, 2, or 3.")
			continue
		}
		break
	}
	fmt.Fprintf(output, "\nWorkspace: %s\nAgents:    %s\nConfig:    %s\n", workspace, strings.Join(agents, ", "), path)
	for _, agent := range agents {
		if !installed[agent] {
			fmt.Fprintf(output, "%s must be installed before starting the host.\n", agent)
		}
	}
	answer, err := ask("Save these settings? [Y/n]: ")
	if err != nil {
		return err
	}
	if answer != "" && !strings.EqualFold(answer, "y") && !strings.EqualFold(answer, "yes") {
		return errors.New("setup cancelled; configuration was not saved")
	}
	if err := saveConfig(path, home, workspace, agents); err != nil {
		return err
	}
	fmt.Fprintln(output, "\nSettings saved. Start with 'rmt serve', or restart the existing host service when it is safe.")
	fmt.Fprintln(output, "The selected agents must also be installed on any device running their local interface.")
	// Native login is optional; rmt never reads or stores agent credentials.
	for _, agent := range agents {
		if !installed[agent] {
			continue
		}
		answer, err := ask(fmt.Sprintf("Run %s's native sign-in now? [y/N]: ", agent))
		if err != nil {
			fmt.Fprintln(output, "Setup complete; sign in through the agent when needed.")
			return nil
		}
		if !strings.EqualFold(answer, "y") && !strings.EqualFold(answer, "yes") {
			continue
		}
		args := []string{"login"}
		if agent == "opencode" {
			args = []string{"auth", "login"}
		}
		cmd := exec.Command(agent, args...)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = reader, output, output
		if input == os.Stdin && reader.Buffered() == 0 {
			cmd.Stdin = os.Stdin
		}
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(output, "%s sign-in did not complete: %v\n", agent, err)
		}
	}
	answer, err = ask("Install and start a host service for this user? [y/N]: ")
	if err == nil && (strings.EqualFold(answer, "y") || strings.EqualFold(answer, "yes")) {
		if err := installService(); err != nil {
			return fmt.Errorf("settings saved; could not install host service: %w", err)
		}
		fmt.Fprintln(output, "Host service installed and start requested.")
	}
	fmt.Fprintln(output, "Connect from another device with: rmt <this-host> <agent>")
	return nil
}
