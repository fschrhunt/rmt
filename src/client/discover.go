// Package client connects to hosts and discovers their native sessions.
package client

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/fschrhunt/rmt/src/agent"
	"github.com/fschrhunt/rmt/src/config"
	"github.com/fschrhunt/rmt/src/network"
	"github.com/fschrhunt/rmt/src/session"
)

// hostSessions queries both backends independently so one failure does not hide the other.
func hostSessions(ctx context.Context, host, ip string) ([]session.Session, []error, bool) {
	client := &http.Client{Timeout: 3 * time.Second}
	base := "http://" + net.JoinHostPort(ip, network.ControlPort)
	return hostSessionsAt(ctx, host, client, base, "http://"+net.JoinHostPort(ip, network.OpenCodePort), "ws://"+net.JoinHostPort(ip, network.CodexPort), nil)
}

// hostSessionsAt queries a host over either the paired or legacy transport.
func hostSessionsAt(ctx context.Context, host string, client *http.Client, base, openCodeURL, codexURL string, ws *websocket.DialOptions) ([]session.Session, []error, bool) {
	var info network.Info
	if err := network.JSONGet(ctx, client, base+"/info", &info); err != nil {
		return nil, []error{fmt.Errorf("%s: host service unavailable: %w", host, err)}, false
	}
	if info.WorkspacePath() == "" {
		return nil, []error{fmt.Errorf("%s: invalid host workspace", host)}, false
	}
	var sessions []session.Session
	var errs []error
	if (config.Config{Agents: info.Agents}).Enabled("opencode") {
		op, err := agent.OpenCodeSessions(ctx, client, openCodeURL, host, info.WorkspacePath())
		if err != nil {
			errs = append(errs, fmt.Errorf("%s opencode: %w", host, err))
		} else {
			sessions = append(sessions, op...)
		}
	}
	if (config.Config{Agents: info.Agents}).Enabled("codex") {
		codex, err := agent.CodexSessions(ctx, codexURL, host, info.WorkspacePath(), ws)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s codex: %w", host, err))
		} else {
			sessions = append(sessions, codex...)
		}
	}
	return sessions, errs, true
}

// discoverSessions scans supported devices and reports whether every eligible device was checked.
func discoverSessions(s network.Status, hostFilter string, excluded map[string]network.Profile) ([]session.Session, []error, bool) {
	nodes := []network.Node{s.Self}
	for _, n := range s.Peer {
		nodes = append(nodes, n)
	}
	var sessions []session.Session
	var errs []error
	complete := true
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, n := range nodes {
		if _, ok := excluded[strings.ToLower(n.HostName)]; ok {
			continue
		}
		if n.UserID != s.Self.UserID || n.UserID == 0 || (hostFilter != "" && !strings.EqualFold(n.HostName, hostFilter)) {
			continue
		}
		if n.OS != "" && n.OS != "linux" && n.OS != "macOS" {
			continue
		}
		if !n.Online && !strings.EqualFold(n.HostName, s.Self.HostName) {
			mu.Lock()
			complete = false
			errs = append(errs, fmt.Errorf("%s: offline; sessions could not be checked", n.HostName))
			mu.Unlock()
			continue
		}
		ip := network.IPv4(n)
		if ip == "" {
			mu.Lock()
			complete = false
			errs = append(errs, fmt.Errorf("%s: no Tailscale IPv4 address", n.HostName))
			mu.Unlock()
			continue
		}
		wg.Add(1)
		go func(n network.Node, ip string) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			found, problems, available := hostSessions(ctx, n.HostName, ip)
			mu.Lock()
			sessions = append(sessions, found...)
			if !available || len(problems) > 0 {
				complete = false
			}
			if available || hostFilter != "" {
				errs = append(errs, problems...)
			}
			mu.Unlock()
		}(n, ip)
	}
	wg.Wait()
	sort.Slice(sessions, func(i, j int) bool {
		if sessions[i].Updated != sessions[j].Updated {
			return sessions[i].Updated > sessions[j].Updated
		}
		if sessions[i].Host != sessions[j].Host {
			return sessions[i].Host < sessions[j].Host
		}
		if sessions[i].Agent != sessions[j].Agent {
			return sessions[i].Agent < sessions[j].Agent
		}
		return sessions[i].Name < sessions[j].Name
	})
	return sessions, errs, complete
}

// ScanSessions checks paired hosts and, when available, legacy Tailscale devices.
func ScanSessions(hostFilter string) ([]session.Session, []error, bool, error) {
	dir, err := config.Dir()
	if err != nil {
		return nil, nil, false, err
	}
	profiles, err := network.Profiles(dir)
	if err != nil {
		return nil, nil, false, err
	}
	var sessions []session.Session
	var problems []error
	complete := true
	var mu sync.Mutex
	var wg sync.WaitGroup
	for name, profile := range profiles {
		if hostFilter != "" && !strings.EqualFold(name, hostFilter) {
			continue
		}
		wg.Add(1)
		go func(name string, profile network.Profile) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			client, err := profile.Client()
			var found []session.Session
			var errs []error
			available := false
			if err != nil {
				errs = []error{fmt.Errorf("%s: %w", name, err)}
			} else {
				defer client.CloseIdleConnections()
				wsClient := *client
				wsClient.Timeout = 0
				wsURL := "wss://" + strings.TrimPrefix(profile.URL, "https://") + "/codex/"
				found, errs, available = hostSessionsAt(ctx, name, client, profile.URL, profile.URL+"/opencode", wsURL, &websocket.DialOptions{HTTPClient: &wsClient})
			}
			mu.Lock()
			defer mu.Unlock()
			sessions = append(sessions, found...)
			problems = append(problems, errs...)
			if !available || len(errs) > 0 {
				complete = false
			}
		}(name, profile)
	}
	wg.Wait()
	_, pairedHost := profiles[strings.ToLower(hostFilter)]
	if hostFilter == "" || !pairedHost {
		s, tailErr := network.Tailscale()
		if tailErr != nil {
			if len(profiles) == 0 || hostFilter != "" {
				return nil, nil, false, fmt.Errorf("no paired host or Tailscale connection: %w", tailErr)
			}
		} else {
			if hostFilter != "" {
				if _, err := network.Resolve(s, hostFilter); err != nil {
					return nil, nil, false, err
				}
			}
			found, errs, ok := discoverSessions(s, hostFilter, profiles)
			sessions = append(sessions, found...)
			problems = append(problems, errs...)
			if !ok {
				complete = false
			}
		}
	}
	sort.Slice(sessions, func(i, j int) bool {
		if sessions[i].Updated != sessions[j].Updated {
			return sessions[i].Updated > sessions[j].Updated
		}
		if sessions[i].Host != sessions[j].Host {
			return sessions[i].Host < sessions[j].Host
		}
		if sessions[i].Agent != sessions[j].Agent {
			return sessions[i].Agent < sessions[j].Agent
		}
		return sessions[i].Name < sessions[j].Name
	})
	return sessions, problems, complete, nil
}
