// Package cli handles rmt commands and interactive input.
package cli

import (
	"fmt"
	"log"
	"os"

	"github.com/fschrhunt/rmt/src/client"
	"github.com/fschrhunt/rmt/src/host"
	"github.com/fschrhunt/rmt/src/session"
)

// Main dispatches host, session discovery, and native-client commands.
func Main() {
	log.SetFlags(0)
	a := os.Args[1:]
	if len(a) > 0 && (a[0] == "code" || a[0] == "devices" || a[0] == "revoke" || a[0] == "hosts" || a[0] == "unpair") {
		if err := hostControl(a[0], a[1:]); err != nil {
			log.Fatal("rmt: ", err)
		}
		return
	}
	if len(a) > 0 && a[0] == "pair" {
		if len(a) != 3 {
			log.Fatal("usage: rmt pair <name> <https://host:port>")
		}
		if err := pairHost(a[1], a[2], os.Stdin, os.Stdout); err != nil {
			log.Fatal("rmt: ", err)
		}
		return
	}
	if len(a) >= 1 && a[0] == "setup" {
		if len(a) != 1 {
			log.Fatal("usage: rmt setup")
		}
		if err := setup(os.Stdin, os.Stdout); err != nil {
			log.Fatal("rmt: ", err)
		}
		return
	}
	if len(a) >= 1 && a[0] == "config" {
		if err := configCommand(a[1:]); err != nil {
			log.Fatal("rmt: ", err)
		}
		return
	}
	if len(a) == 1 && a[0] == "serve" {
		if err := host.Run(); err != nil {
			log.Fatal("rmt: ", err)
		}
		return
	}
	if len(a) >= 1 && a[0] == "ls" {
		if len(a) > 2 {
			log.Fatal("usage: rmt ls [host]")
		}
		host := ""
		if len(a) == 2 {
			host = a[1]
		}
		if err := listSessions(host); err != nil {
			log.Fatal("rmt: ", err)
		}
		return
	}
	if len(a) >= 1 && a[0] == "connect" {
		if len(a) < 2 {
			log.Fatal("usage: rmt connect [host/]<name> [agent options...]")
		}
		host, name, err := session.ParseReference(a[1])
		if err != nil {
			log.Fatal("rmt: ", err)
		}
		saved, err := findSession(name, host, "")
		if err != nil {
			log.Fatal("rmt: ", err)
		}
		if err = client.Connect(saved.Host, saved.Agent, a[2:], &saved); err != nil {
			log.Fatal("rmt: ", err)
		}
		return
	}
	if len(a) < 2 || a[0] == "--help" || a[0] == "help" {
		fmt.Fprint(os.Stderr, `Usage:
  rmt <host> <opencode|codex> [--connect] [agent options...]
  rmt ls [host]                    List sessions
  rmt connect <name>               Reconnect to a session
  rmt setup                        Configure this host
  rmt serve                        Run this host
  rmt config [set <workspace>]     Show or change settings
  rmt code                         Generate a one-time pairing code on the host
  rmt pair <name> <https://host:port>  Pair a client with a host
  rmt hosts | rmt unpair <host>    Manage paired hosts on this client
  rmt devices | rmt revoke <id>    Manage paired devices on the host
`)
		if len(a) == 0 || a[0] == "help" || a[0] == "--help" {
			return
		}
		os.Exit(2)
	}
	if len(a) > 2 && a[2] == "--connect" {
		saved, err := findSession("", a[0], a[1])
		if err != nil {
			log.Fatal("rmt: ", err)
		}
		if err = client.Connect(saved.Host, saved.Agent, a[3:], &saved); err != nil {
			log.Fatal("rmt: ", err)
		}
		return
	}
	if err := client.Connect(a[0], a[1], a[2:], nil); err != nil {
		log.Fatal("rmt: ", err)
	}
}
