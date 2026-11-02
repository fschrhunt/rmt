package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"unicode"

	"github.com/fschrhunt/rmt/src/client"
	"github.com/fschrhunt/rmt/src/session"
)

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

// listSessions prints native sessions and warns when some hosts could not be scanned.
func listSessions(host string) error {
	sessions, problems, _, err := client.ScanSessions(host)
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
				title += fmt.Sprintf(" (connect: %s)", session.CollisionReference(v, sessions))
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

// findSession resolves an exact handle without choosing arbitrarily among collisions.
func findSession(name, host, agent string) (session.Session, error) {
	sessions, problems, complete, err := client.ScanSessions(host)
	if err != nil {
		return session.Session{}, err
	}
	for _, err := range problems {
		fmt.Fprintln(os.Stderr, "rmt:", err)
	}
	return session.SelectScanned(sessions, complete, name, host, agent)
}
