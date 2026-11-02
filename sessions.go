// Session discovery reads native agent histories without creating a second session store.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
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

// Keep this list unchanged: its order and length determine existing session handles.
var animals = [...]string{
	"fox", "bird", "bear", "wolf", "frog", "deer", "duck", "goat", "hare", "hawk",
	"lion", "lynx", "mole", "moth", "puma", "seal", "swan", "toad", "wasp", "boar",
	"crab", "crow", "dove", "fish", "goose", "gull", "ibis", "koala", "lemur", "llama",
	"moose", "mouse", "newt", "okapi", "otter", "panda", "pike", "quail", "quokka", "raven",
	"shark", "sheep", "skunk", "snail", "snake", "squid", "stork", "tapir", "tiger", "trout",
	"tuna", "turkey", "viper", "whale", "zebra", "eagle", "finch", "gecko", "heron", "horse",
}

// sessionWords returns stable animal words for a native session and its collision fallback.
func sessionWords(agent, id string) []string {
	sum := sha256.Sum256([]byte(agent + "\x00" + id))
	words := make([]string, len(sum))
	for i, b := range sum {
		words[i] = animals[int(b)%len(animals)]
	}
	return words
}

// sessionName gives a native session a stable animal handle.
func sessionName(agent, id string) string {
	return sessionWords(agent, id)[0]
}

// previousSessionName preserves the earlier six-character alphanumeric handles.
func previousSessionName(agent, id string) string {
	shortAnimals := [...]string{"ant", "bat", "bee", "cat", "cod", "cow", "dog", "eel", "elk", "emu", "fox", "gnu", "hen", "hog", "jay", "koi", "owl", "pig", "pug", "ram", "rat", "yak"}
	const alphabet = "23456789abcdefghjkmnpqrstuvwxyz"
	sum := sha256.Sum256([]byte(agent + "\x00" + id))
	return shortAnimals[int(sum[0])%len(shortAnimals)] + string([]byte{
		alphabet[int(sum[1])%len(alphabet)],
		alphabet[int(sum[2])%len(alphabet)],
		alphabet[int(sum[3])%len(alphabet)],
	})
}

// legacySessionName preserves handles produced before short animal names.
func legacySessionName(agent, id string) string {
	dogs := [...]string{"akita", "beagle", "collie", "corgi", "dingo", "finn", "husky", "jindo", "labrador", "milo", "poodle", "pug", "retriever", "shiba", "terrier", "whippet"}
	sum := sha256.Sum256([]byte(agent + "\x00" + id))
	return dogs[int(sum[0])%len(dogs)] + "-" + hex.EncodeToString(sum[1:5])
}

// insideWorkspace accepts only the host workspace or a directory below it.
func insideWorkspace(dev, dir string) bool {
	if !filepath.IsAbs(dev) || !filepath.IsAbs(dir) {
		return false
	}
	rel, err := filepath.Rel(filepath.Clean(dev), filepath.Clean(dir))
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

// openCodeSessions lists saved root sessions and active drains on one backend.
func openCodeSessions(ctx context.Context, client *http.Client, base, host, dev string) ([]session, error) {
	var active struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := jsonGet(ctx, client, base+"/api/session/active", &active); err != nil {
		return nil, fmt.Errorf("active sessions: %w", err)
	}
	var sessions []session
	cursor := ""
	seen := make(map[string]bool)
	for {
		q := url.Values{"limit": {"100"}, "parentID": {"null"}, "archived": {"false"}}
		if cursor != "" {
			q.Set("cursor", cursor)
		}
		var page struct {
			Data []struct {
				ID    string `json:"id"`
				Title string `json:"title"`
				Time  struct {
					Updated int64 `json:"updated"`
				} `json:"time"`
				Location struct {
					Directory string `json:"directory"`
				} `json:"location"`
			} `json:"data"`
			Cursor struct {
				Next string `json:"next"`
			} `json:"cursor"`
		}
		if err := jsonGet(ctx, client, base+"/api/session?"+q.Encode(), &page); err != nil {
			return nil, err
		}
		for _, v := range page.Data {
			if v.ID == "" || !insideWorkspace(dev, v.Location.Directory) {
				continue
			}
			_, isActive := active.Data[v.ID]
			sessions = append(sessions, session{Name: sessionName("opencode", v.ID), Host: host, Agent: "opencode", ID: v.ID, Dir: v.Location.Directory, Title: v.Title, Active: isActive, Updated: v.Time.Updated / 1000})
		}
		if len(page.Data) == 0 || page.Cursor.Next == "" {
			break
		}
		if seen[page.Cursor.Next] {
			return nil, errors.New("OpenCode repeated a session cursor")
		}
		seen[page.Cursor.Next] = true
		cursor = page.Cursor.Next
	}
	return sessions, nil
}

// rpc sends a request and waits for its matching response, ignoring notifications.
func rpc(ctx context.Context, conn *websocket.Conn, id int, method string, params any, result any) error {
	if err := wsjson.Write(ctx, conn, map[string]any{"id": id, "method": method, "params": params}); err != nil {
		return err
	}
	for {
		var msg struct {
			ID     *int            `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := wsjson.Read(ctx, conn, &msg); err != nil {
			return err
		}
		if msg.ID == nil || *msg.ID != id {
			continue
		}
		if msg.Error != nil {
			return errors.New(msg.Error.Message)
		}
		return json.Unmarshal(msg.Result, result)
	}
}

// codexSessions lists saved Codex threads in the host workspace.
func codexSessions(ctx context.Context, address, host, dev string) ([]session, error) {
	conn, _, err := websocket.Dial(ctx, address, nil)
	if err != nil {
		return nil, err
	}
	defer conn.CloseNow()
	conn.SetReadLimit(8 << 20)
	var initialized json.RawMessage
	if err = rpc(ctx, conn, 1, "initialize", map[string]any{"clientInfo": map[string]string{"name": "rmt", "title": "rmt", "version": "0.1.0"}}, &initialized); err != nil {
		return nil, fmt.Errorf("initialize: %w", err)
	}
	if err = wsjson.Write(ctx, conn, map[string]any{"method": "initialized", "params": map[string]any{}}); err != nil {
		return nil, err
	}
	var sessions []session
	var cursor string
	seen := make(map[string]bool)
	for {
		params := map[string]any{"limit": 100, "sortKey": "updated_at", "modelProviders": []string{}, "sourceKinds": []string{"cli", "vscode", "appServer"}}
		if cursor != "" {
			params["cursor"] = cursor
		}
		var page struct {
			Data []struct {
				ID      string `json:"id"`
				Cwd     string `json:"cwd"`
				Name    string `json:"name"`
				Preview string `json:"preview"`
				Updated int64  `json:"updatedAt"`
				Status  struct {
					Type string `json:"type"`
				} `json:"status"`
			} `json:"data"`
			NextCursor string `json:"nextCursor"`
		}
		if err = rpc(ctx, conn, 2, "thread/list", params, &page); err != nil {
			return nil, fmt.Errorf("thread/list: %w", err)
		}
		for _, v := range page.Data {
			if v.ID == "" || !insideWorkspace(dev, v.Cwd) {
				continue
			}
			title := v.Name
			if title == "" {
				title = v.Preview
			}
			sessions = append(sessions, session{Name: sessionName("codex", v.ID), Host: host, Agent: "codex", ID: v.ID, Dir: v.Cwd, Title: title, Active: v.Status.Type == "active", Updated: v.Updated})
		}
		if page.NextCursor == "" {
			break
		}
		if seen[page.NextCursor] {
			return nil, errors.New("Codex repeated a thread cursor")
		}
		seen[page.NextCursor] = true
		cursor = page.NextCursor
	}
	return sessions, nil
}

// hostSessions queries both backends independently so one failure does not hide the other.
func hostSessions(ctx context.Context, host, ip string) ([]session, []error, bool) {
	client := &http.Client{Timeout: 3 * time.Second}
	var info hostInfo
	base := "http://" + net.JoinHostPort(ip, controlPort)
	if err := jsonGet(ctx, client, base+"/info", &info); err != nil {
		return nil, []error{fmt.Errorf("%s: host service unavailable: %w", host, err)}, false
	}
	if info.workspace() == "" {
		return nil, []error{fmt.Errorf("%s: invalid host workspace", host)}, false
	}
	var sessions []session
	var errs []error
	if (config{Agents: info.Agents}).enabled("opencode") {
		op, err := openCodeSessions(ctx, client, "http://"+net.JoinHostPort(ip, openCodePort), host, info.workspace())
		if err != nil {
			errs = append(errs, fmt.Errorf("%s opencode: %w", host, err))
		} else {
			sessions = append(sessions, op...)
		}
	}
	if (config{Agents: info.Agents}).enabled("codex") {
		codex, err := codexSessions(ctx, "ws://"+net.JoinHostPort(ip, codexPort), host, info.workspace())
		if err != nil {
			errs = append(errs, fmt.Errorf("%s codex: %w", host, err))
		} else {
			sessions = append(sessions, codex...)
		}
	}
	return sessions, errs, true
}

// discoverSessions scans supported devices and reports whether every eligible device was checked.
func discoverSessions(s status, hostFilter string) ([]session, []error, bool) {
	nodes := []node{s.Self}
	for _, n := range s.Peer {
		nodes = append(nodes, n)
	}
	var sessions []session
	var errs []error
	complete := true
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, n := range nodes {
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
		ip := ipv4(n)
		if ip == "" {
			mu.Lock()
			complete = false
			errs = append(errs, fmt.Errorf("%s: no Tailscale IPv4 address", n.HostName))
			mu.Unlock()
			continue
		}
		wg.Add(1)
		go func(n node, ip string) {
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
	s, err := tailnet()
	if err != nil {
		return err
	}
	if host != "" {
		if _, err = resolve(s, host); err != nil {
			return err
		}
	}
	sessions, problems, _ := discoverSessions(s, host)
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
	s, err := tailnet()
	if err != nil {
		return session{}, err
	}
	if host != "" {
		if _, err = resolve(s, host); err != nil {
			return session{}, err
		}
	}
	sessions, problems, complete := discoverSessions(s, host)
	for _, err := range problems {
		fmt.Fprintln(os.Stderr, "rmt:", err)
	}
	return selectScanned(sessions, complete, name, host, agent)
}
