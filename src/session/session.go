// Package session identifies, names and selects native agent sessions.
package session

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// Session identifies a native conversation and the host workspace it belongs to.
type Session struct {
	Name    string
	Host    string
	Agent   string
	ID      string
	Dir     string
	Title   string
	Active  bool
	Updated int64
}

// InsideWorkspace accepts only the host workspace or a directory below it.
func InsideWorkspace(workspace, dir string) bool {
	if !filepath.IsAbs(workspace) || !filepath.IsAbs(dir) {
		return false
	}
	rel, err := filepath.Rel(filepath.Clean(workspace), filepath.Clean(dir))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// ParseReference separates an optional host from an animal handle or collision fallback.
func ParseReference(ref string) (string, string, error) {
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
func matchesSession(v Session, name string) bool {
	if name == "" || name == v.Name {
		return true
	}
	if v.ID == "" {
		return false
	}
	if name == previousName(v.Agent, v.ID) || name == legacyName(v.Agent, v.ID) {
		return true
	}
	parts, words := strings.Split(name, "/"), words(v.Agent, v.ID)
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

// CollisionReference produces a host-qualified animal path that identifies one session.
func CollisionReference(target Session, sessions []Session) string {
	words := words(target.Agent, target.ID)
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

// Select rejects missing or ambiguous handles instead of selecting arbitrarily.
func Select(sessions []Session, name, agent string) (Session, error) {
	var matches []Session
	for _, v := range sessions {
		if matchesSession(v, name) && (agent == "" || v.Agent == agent) {
			matches = append(matches, v)
		}
	}
	if len(matches) == 0 {
		return Session{}, errors.New("no matching session found on reachable hosts; run 'rmt ls' to see available sessions")
	}
	if len(matches) > 1 {
		return Session{}, fmt.Errorf("%d sessions match; run 'rmt ls' and use the indicated name", len(matches))
	}
	return matches[0], nil
}

// SelectScanned refuses to identify a session from an incomplete discovery result.
func SelectScanned(sessions []Session, complete bool, name, host, agent string) (Session, error) {
	if !complete {
		if host == "" {
			return Session{}, errors.New("cannot safely identify a session while some devices could not be checked; use 'rmt connect <host>/<name>'")
		}
		return Session{}, errors.New("cannot safely identify a session while this host could not be fully checked")
	}
	return Select(sessions, name, agent)
}
