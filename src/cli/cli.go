// Package cli implements the local remote-host prompt.
package cli

import (
	"bufio"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"

	"github.com/fschrhunt/rmt/src/client"
	"github.com/fschrhunt/rmt/src/config"
	"github.com/fschrhunt/rmt/src/extensions"
	"github.com/fschrhunt/rmt/src/hosts"
	"github.com/fschrhunt/rmt/src/network"
	"github.com/fschrhunt/rmt/src/services"
)

// ensureHost starts this machine's user service once without replacing an existing service.
func ensureHost() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "rmt: local host:", err)
		return
	}
	if cfg.Serve != nil && !*cfg.Serve {
		return
	}
	address := cfg.Listen
	if address == "" {
		address = "127.0.0.1:47473"
	} else if host, port, e := net.SplitHostPort(address); e == nil {
		if host == "" || (net.ParseIP(host) != nil && net.ParseIP(host).IsUnspecified()) {
			host = "127.0.0.1"
		}
		address = net.JoinHostPort(host, port)
	}
	if err := services.Install(address); err != nil {
		fmt.Fprintln(os.Stderr, "rmt: local host:", err)
	}
}

// Main connects one host or runs the internal host service.
func Main() {
	log.SetFlags(0)
	args := os.Args[1:]
	if len(args) == 1 && args[0] == "--serve" {
		if err := hosts.Run(); err != nil {
			log.Fatal("rmt: ", err)
		}
		return
	}
	if len(args) == 0 {
		ensureHost()
	}
	if len(args) == 0 || (len(args) == 1 && (args[0] == "help" || args[0] == "--help" || args[0] == "-h")) {
		fmt.Fprintln(os.Stdout, "Usage: rmt <host>\n\nConnect to a host using the addresses in ~/.rmt/settings.toml.\nType exit or press Ctrl-D to disconnect.\n\nExtensions: ~/.rmt/extensions/\nGuide: https://github.com/fschrhunt/rmt/blob/main/docs/extensions.md")
		return
	}
	if len(args) != 1 {
		log.Fatal("usage: rmt <host>")
	}
	name := args[0]
	if err := network.ValidateName(name); err != nil {
		log.Fatal("rmt: ", err)
	}
	ensureHost()
	root, err := config.Dir()
	if err != nil {
		log.Fatal("rmt: ", err)
	}
	registry, err := extensions.Load(filepath.Join(root, "extensions"))
	if err != nil {
		log.Fatal("rmt: load extensions: ", err)
	}
	input := bufio.NewReader(os.Stdin)
	address, err := client.PairAddress(name)
	if err != nil {
		log.Fatal("rmt: ", err)
	}
	if address != "" {
		if err = pairHost(name, address, input, os.Stdout); err != nil {
			log.Fatal("rmt: ", err)
		}
	}
	remote, err := client.OpenShell(name)
	if err != nil {
		log.Fatal("rmt: ", err)
	}
	if err = prompt(name, remote, input, os.Stdout, registry); err != nil {
		log.Fatal("rmt: ", err)
	}
}
