package client

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/fschrhunt/rmt/src/agent"
	"github.com/fschrhunt/rmt/src/config"
	"github.com/fschrhunt/rmt/src/network"
	"github.com/fschrhunt/rmt/src/session"
)

// target holds either a paired HTTPS endpoint or a legacy Tailscale endpoint.
type target struct {
	base    string
	client  *http.Client
	ip      string
	profile *network.Profile
}

// resolveTarget prefers an explicitly paired name, then checks Tailscale devices.
func resolveTarget(name string) (target, error) {
	dir, err := config.Dir()
	if err != nil {
		return target{}, err
	}
	profiles, err := network.Profiles(dir)
	if err != nil {
		return target{}, err
	}
	if profile, ok := profiles[strings.ToLower(name)]; ok {
		client, err := profile.Client()
		if err != nil {
			return target{}, err
		}
		return target{base: profile.URL, client: client, profile: &profile}, nil
	}
	s, err := network.Tailscale()
	if err != nil {
		return target{}, fmt.Errorf("host %q is not paired; run 'rmt pair' or connect Tailscale", name)
	}
	ip, err := network.Resolve(s, name)
	if err != nil {
		return target{}, err
	}
	return target{base: "http://" + net.JoinHostPort(ip, network.ControlPort), client: &http.Client{Timeout: 5 * time.Second}, ip: ip}, nil
}

// Connect launches a native agent interface, optionally resuming a saved session.
func Connect(host, agentName string, extra []string, saved *session.Session) error {
	if agentName != "opencode" && agentName != "codex" {
		return fmt.Errorf("unknown agent %q (choose opencode or codex)", agentName)
	}
	target, e := resolveTarget(host)
	if e != nil {
		return e
	}
	defer target.client.CloseIdleConnections()
	res, e := target.client.Get(target.base + "/info")
	if e != nil {
		return fmt.Errorf("cannot reach %s; start 'rmt serve' there: %w", host, e)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return fmt.Errorf("host refused connection: %s", res.Status)
	}
	var info network.Info
	if e = json.NewDecoder(io.LimitReader(res.Body, 4096)).Decode(&info); e != nil || info.WorkspacePath() == "" {
		return errors.New("invalid host response")
	}
	if !(config.Config{Agents: info.Agents}).Enabled(agentName) {
		return fmt.Errorf("%s is not enabled on %s; run 'rmt setup' on that host", agentName, host)
	}
	dir := info.WorkspacePath()
	if saved != nil {
		if !strings.EqualFold(saved.Host, host) || saved.Agent != agentName || !session.InsideWorkspace(info.WorkspacePath(), saved.Dir) {
			return errors.New("session is outside the host workspace or belongs to another host")
		}
		dir = saved.Dir
	}
	if agentName == "opencode" {
		if saved != nil {
			extra = append([]string{"--session", saved.ID}, extra...)
		}
		if target.profile == nil {
			return agent.OpenCode(target.ip, dir, extra)
		}
		transport, err := target.profile.Transport()
		if err != nil {
			return err
		}
		endpoint, err := url.Parse(target.base + "/opencode")
		if err != nil {
			return err
		}
		return agent.OpenCodeAt(endpoint, transport, dir, extra)
	}
	if target.profile != nil {
		return agent.CodexDirect(*target.profile, dir, extra, saved)
	}
	var args []string
	if saved != nil {
		args = append([]string{"resume", "--remote", "ws://" + net.JoinHostPort(target.ip, network.CodexPort), "-C", dir, saved.ID}, extra...)
	} else {
		args = append([]string{"--remote", "ws://" + net.JoinHostPort(target.ip, network.CodexPort), "-C", dir}, extra...)
	}
	binary, e := exec.LookPath(agentName)
	if e != nil {
		return fmt.Errorf("install %s locally: %w", agentName, e)
	}
	return syscall.Exec(binary, append([]string{agentName}, args...), os.Environ())
}
