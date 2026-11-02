package network

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"time"
)

type Status struct {
	Self Node
	Peer map[string]Node
}
type Node struct {
	HostName     string
	OS           string
	UserID       int64
	Online       bool
	TailscaleIPs []string
}
type whois struct{ UserProfile struct{ ID int64 } }

// Tailscale reads the local Tailscale daemon's view of devices.
func Tailscale() (Status, error) {
	var s Status
	b, e := exec.Command("tailscale", "status", "--json").Output()
	if e != nil {
		return s, fmt.Errorf("tailscale status: %w", e)
	}
	e = json.Unmarshal(b, &s)
	return s, e
}

// IPv4 returns the device's first IPv4 address.
func IPv4(n Node) string {
	for _, v := range n.TailscaleIPs {
		if ip := net.ParseIP(v); ip != nil && ip.To4() != nil {
			return v
		}
	}
	return ""
}

// Resolve finds one online device owned by the current Tailscale user.
func Resolve(s Status, name string) (string, error) {
	var nodes []Node
	if strings.EqualFold(s.Self.HostName, name) {
		nodes = append(nodes, s.Self)
	}
	for _, n := range s.Peer {
		if strings.EqualFold(n.HostName, name) {
			nodes = append(nodes, n)
		}
	}
	if len(nodes) != 1 {
		return "", fmt.Errorf("host %q not found uniquely on your tailnet", name)
	}
	n := nodes[0]
	if n.UserID != s.Self.UserID {
		return "", errors.New("host belongs to another Tailscale user")
	}
	if !n.Online && !strings.EqualFold(s.Self.HostName, name) {
		return "", fmt.Errorf("host %q is offline", name)
	}
	if ip := IPv4(n); ip != "" {
		return ip, nil
	}
	return "", errors.New("host has no Tailscale IPv4 address")
}

// SameUser verifies each request against Tailscale's authenticated user identity.
func SameUser(owner int64, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip, _, e := net.SplitHostPort(r.RemoteAddr)
		if e != nil {
			http.Error(w, "forbidden", 403)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		b, e := exec.CommandContext(ctx, "tailscale", "whois", "--json", ip).Output()
		var id whois
		if e != nil || json.Unmarshal(b, &id) != nil || id.UserProfile.ID == 0 || id.UserProfile.ID != owner {
			http.Error(w, "forbidden", 403)
			return
		}
		h.ServeHTTP(w, r)
	})
}
