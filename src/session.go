package rmt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/coder/websocket"
	"github.com/fschrhunt/rmt/src/network"
)

// session identifies a native conversation and the host workspace it belongs to.
type session struct {
	Name    string
	Host    string
	Agent   string
	ID      string
	Dir     string
	Title   string
	Active  bool
	Updated int64
}

// insideWorkspace accepts only the host workspace or a directory below it.
func insideWorkspace(workspace, dir string) bool {
	if !filepath.IsAbs(workspace) || !filepath.IsAbs(dir) {
		return false
	}
	rel, err := filepath.Rel(filepath.Clean(workspace), filepath.Clean(dir))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// jsonGet reads one bounded JSON response from an agent server.
func jsonGet(ctx context.Context, client *http.Client, address string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %s", resp.Status)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(out)
}

// hostSessions queries both backends independently so one failure does not hide the other.
func hostSessions(ctx context.Context, host, ip string) ([]session, []error, bool) {
	client := &http.Client{Timeout: 3 * time.Second}
	base := "http://" + net.JoinHostPort(ip, controlPort)
	return hostSessionsAt(ctx, host, client, base, "http://"+net.JoinHostPort(ip, openCodePort), "ws://"+net.JoinHostPort(ip, codexPort), nil)
}

// hostSessionsAt queries a host over either the paired or legacy transport.
func hostSessionsAt(ctx context.Context, host string, client *http.Client, base, openCodeURL, codexURL string, ws *websocket.DialOptions) ([]session, []error, bool) {
	var info hostInfo
	if err := jsonGet(ctx, client, base+"/info", &info); err != nil {
		return nil, []error{fmt.Errorf("%s: host service unavailable: %w", host, err)}, false
	}
	if info.workspace() == "" {
		return nil, []error{fmt.Errorf("%s: invalid host workspace", host)}, false
	}
	var sessions []session
	var errs []error
	if (config{Agents: info.Agents}).enabled("opencode") {
		op, err := openCodeSessions(ctx, client, openCodeURL, host, info.workspace())
		if err != nil {
			errs = append(errs, fmt.Errorf("%s opencode: %w", host, err))
		} else {
			sessions = append(sessions, op...)
		}
	}
	if (config{Agents: info.Agents}).enabled("codex") {
		codex, err := codexSessions(ctx, codexURL, host, info.workspace(), ws)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s codex: %w", host, err))
		} else {
			sessions = append(sessions, codex...)
		}
	}
	return sessions, errs, true
}

// discoverSessions scans supported devices and reports whether every eligible device was checked.
func discoverSessions(s network.Status, hostFilter string, excluded map[string]network.Profile) ([]session, []error, bool) {
	nodes := []network.Node{s.Self}
	for _, n := range s.Peer {
		nodes = append(nodes, n)
	}
	var sessions []session
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

// scanSessions checks paired hosts and, when available, legacy Tailscale devices.
func scanSessions(hostFilter string) ([]session, []error, bool, error) {
	dir, err := networkDir()
	if err != nil {
		return nil, nil, false, err
	}
	profiles, err := network.Profiles(dir)
	if err != nil {
		return nil, nil, false, err
	}
	var sessions []session
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
			var found []session
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

// safeTitle makes session titles single-line and bounded for terminal output.
func safeTitle(title string) string {
	title = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, title)
	title = strings.Join(strings.Fields(title), " ")
	r := []rune(title)
	if len(r) > 70 {
		return string(r[:67]) + "..."
	}
	return title
}

// parseSessionReference separates an optional host from an animal handle or collision fallback.
func parseSessionReference(ref string) (string, string, error) {
	ref = strings.ToLower(ref)
	if host, name, ok := strings.Cut(ref, "/"); ok {
		if host == "" || name == "" || strings.HasPrefix(name, "/") || strings.Contains(name, "//") || strings.HasSuffix(name, "/") {
			return "", "", errors.New("usage: rmt connect [host/]<name> [agent options...]")
		}
		return host, name, nil
	}
	if ref == "" {
		return "", "", errors.New("session name is empty")
	}
	return "", ref, nil
}

// matchesSession recognizes animal handles, animal paths, and both earlier handle formats.
func matchesSession(v session, name string) bool {
	if name == "" || name == v.Name {
		return true
	}
	if v.ID == "" {
		return false
	}
	if name == previousSessionName(v.Agent, v.ID) || name == legacySessionName(v.Agent, v.ID) {
		return true
	}
	parts, words := strings.Split(name, "/"), sessionWords(v.Agent, v.ID)
	if len(parts) > len(words) {
		return false
	}
	for i, part := range parts {
		if part != words[i] {
			return false
		}
	}
	return true
}

// collisionReference produces a host-qualified animal path that identifies one session.
func collisionReference(target session, sessions []session) string {
	words := sessionWords(target.Agent, target.ID)
	for n := 1; n <= len(words); n++ {
		name := strings.Join(words[:n], "/")
		matches := 0
		for _, v := range sessions {
			if strings.EqualFold(v.Host, target.Host) && matchesSession(v, name) {
				matches++
			}
		}
		if matches == 1 {
			return target.Host + "/" + name
		}
	}
	// A complete collision remains ambiguous rather than selecting the wrong session.
	return target.Host + "/" + strings.Join(words, "/")
}

// listSessions prints native sessions and warns when some hosts could not be scanned.
func listSessions(host string) error {
	sessions, problems, _, err := scanSessions(host)
	if err != nil {
		return err
	}
	if len(sessions) == 0 {
		fmt.Println("No sessions found in workspaces on reachable hosts.")
	} else {
		counts := make(map[string]int)
		for _, v := range sessions {
			counts[v.Name]++
		}
		fmt.Printf("%-8s %-12s %-10s %-8s %s\n", "NAME", "HOST", "AGENT", "STATE", "SESSION")
		for _, v := range sessions {
			state := "saved"
			if v.Active {
				state = "active"
			}
			title := safeTitle(v.Title)
			if counts[v.Name] > 1 {
				title += fmt.Sprintf(" (connect: %s)", collisionReference(v, sessions))
			}
			fmt.Printf("%-8s %-12s %-10s %-8s %s\n", v.Name, v.Host, v.Agent, state, title)
		}
	}
	for _, err := range problems {
		fmt.Fprintln(os.Stderr, "rmt:", err)
	}
	if host != "" && len(sessions) == 0 && len(problems) > 0 {
		return errors.New("could not fully check this host for sessions")
	}
	return nil
}

// selectSession rejects missing or ambiguous handles instead of selecting arbitrarily.
func selectSession(sessions []session, name, agent string) (session, error) {
	var matches []session
	for _, v := range sessions {
		if matchesSession(v, name) && (agent == "" || v.Agent == agent) {
			matches = append(matches, v)
		}
	}
	if len(matches) == 0 {
		return session{}, errors.New("no matching session found on reachable hosts; run 'rmt ls' to see available sessions")
	}
	if len(matches) > 1 {
		return session{}, fmt.Errorf("%d sessions match; run 'rmt ls' and use the indicated name", len(matches))
	}
	return matches[0], nil
}

// selectScanned refuses to identify a session from an incomplete discovery result.
func selectScanned(sessions []session, complete bool, name, host, agent string) (session, error) {
	if !complete {
		if host == "" {
			return session{}, errors.New("cannot safely identify a session while some devices could not be checked; use 'rmt connect <host>/<name>'")
		}
		return session{}, errors.New("cannot safely identify a session while this host could not be fully checked")
	}
	return selectSession(sessions, name, agent)
}

// findSession resolves an exact handle without choosing arbitrarily among collisions.
func findSession(name, host, agent string) (session, error) {
	sessions, problems, complete, err := scanSessions(host)
	if err != nil {
		return session{}, err
	}
	for _, err := range problems {
		fmt.Fprintln(os.Stderr, "rmt:", err)
	}
	return selectScanned(sessions, complete, name, host, agent)
}
