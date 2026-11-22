// Package cli implements the local remote-host prompt.
package cli

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/fschrhunt/rmt/src/client"
	"github.com/fschrhunt/rmt/src/config"
	"github.com/fschrhunt/rmt/src/extensions"
	"github.com/fschrhunt/rmt/src/hosts"
	"github.com/fschrhunt/rmt/src/network"
	"github.com/fschrhunt/rmt/src/services"
)

// Main connects one host or runs the internal host service.
func Main(version string) {
	log.SetFlags(0)
	args := os.Args[1:]
	if len(args) == 1 && args[0] == "--version" {
		fmt.Fprintln(os.Stdout, "rmt version "+version)
		return
	}
	if len(args) == 1 && args[0] == "--serve" {
		if err := hosts.Run(); err != nil {
			log.Fatal("rmt: ", err)
		}
		return
	}
	if len(args) == 0 || (len(args) == 1 && (args[0] == "help" || args[0] == "--help" || args[0] == "-h")) {
		fmt.Fprintln(os.Stdout, "Usage: rmt <host>\n       rmt host enable|disable\n\nConnect to a host using the addresses in ~/.rmt/settings.toml.\nType exit or press Ctrl-D to disconnect.\n\nThe first connection attempt sets up background hosting on this machine.\nUse rmt host disable to opt out and disconnect inbound clients.\nUse rmt host enable to turn hosting back on.\n\nExtensions: ~/.rmt/extensions/\nGuide: https://github.com/fschrhunt/rmt/blob/main/docs/extensions.md")
		return
	}
	if len(args) == 2 && args[0] == "host" {
		action := args[1]
		if action != "enable" && action != "disable" {
			log.Fatal("usage: rmt host enable|disable")
		}
		if err := manageHost(action); err != nil {
			log.Fatal("rmt: ", err)
		}
		fmt.Fprintf(os.Stdout, "rmt host service %sd\n", action)
		return
	}
	if len(args) != 1 {
		log.Fatal("usage: rmt <host>")
	}
	name := args[0]
	if err := network.ValidateName(name); err != nil {
		log.Fatal("rmt: ", err)
	}
	root, err := config.Dir()
	if err != nil {
		log.Fatal("rmt: ", err)
	}
	cfg, err := config.Read(filepath.Join(root, "settings.toml"))
	if err != nil {
		log.Fatal("rmt: ", err)
	}
	if err := startLocalHost(root, cfg, services.EnableOnFirstUse); err != nil {
		fmt.Fprintln(os.Stderr, "rmt: local host:", err)
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
