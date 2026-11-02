package rmt

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

	"github.com/fschrhunt/rmt/src/network"
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
	dir, err := networkDir()
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
	return target{base: "http://" + net.JoinHostPort(ip, controlPort), client: &http.Client{Timeout: 5 * time.Second}, ip: ip}, nil
}

// connect launches a native agent interface, optionally resuming a saved session.
func connect(host, agent string, extra []string, saved *session) error {
	if agent != "opencode" && agent != "codex" {
		return fmt.Errorf("unknown agent %q (choose opencode or codex)", agent)
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
	var info hostInfo
	if e = json.NewDecoder(io.LimitReader(res.Body, 4096)).Decode(&info); e != nil || info.workspace() == "" {
		return errors.New("invalid host response")
	}
	if !(config{Agents: info.Agents}).enabled(agent) {
		return fmt.Errorf("%s is not enabled on %s; run 'rmt setup' on that host", agent, host)
	}
	dir := info.workspace()
	if saved != nil {
		if !strings.EqualFold(saved.Host, host) || saved.Agent != agent || !insideWorkspace(info.workspace(), saved.Dir) {
			return errors.New("session is outside the host workspace or belongs to another host")
		}
		dir = saved.Dir
	}
	if agent == "opencode" {
		if saved != nil {
			extra = append([]string{"--session", saved.ID}, extra...)
		}
		if target.profile == nil {
			return openCode(target.ip, dir, extra)
		}
		transport, err := target.profile.Transport()
		if err != nil {
			return err
		}
		endpoint, err := url.Parse(target.base + "/opencode")
		if err != nil {
			return err
		}
		return openCodeAt(endpoint, transport, dir, extra)
	}
	if target.profile != nil {
		return codexDirect(*target.profile, dir, extra, saved)
	}
	var args []string
	if saved != nil {
		args = append([]string{"resume", "--remote", "ws://" + net.JoinHostPort(target.ip, codexPort), "-C", dir, saved.ID}, extra...)
	} else {
		args = append([]string{"--remote", "ws://" + net.JoinHostPort(target.ip, codexPort), "-C", dir}, extra...)
	}
	binary, e := exec.LookPath(agent)
	if e != nil {
		return fmt.Errorf("install %s locally: %w", agent, e)
	}
	return syscall.Exec(binary, append([]string{agent}, args...), os.Environ())
}
