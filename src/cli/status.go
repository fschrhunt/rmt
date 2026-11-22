package cli

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/fschrhunt/rmt/src/config"
	"github.com/fschrhunt/rmt/src/network"
	"github.com/fschrhunt/rmt/src/services"
)

// status summarizes local hosting and known hosts without contacting or pairing with remote machines.
func status(version string, output io.Writer) error {
	root, err := config.StatusDir()
	if err != nil {
		return err
	}
	cfg, err := config.Read(filepath.Join(root, "settings.toml"))
	if err != nil {
		return err
	}
	profiles, err := network.Profiles(root)
	if err != nil {
		return err
	}
	registered, serviceErr := services.Registrations()
	var running network.HostStatus
	var probeErr error
	var legacy []network.Device
	live := false
	if _, err := os.Lstat(filepath.Join(root, "host.sock")); err == nil {
		probeErr = network.Control(root, http.MethodGet, "/status", nil, &running)
		live = probeErr == nil
		if !live &&
			network.Control(root, http.MethodGet, "/devices", nil, &legacy) == nil {
			live = true
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		probeErr = err
	}
	fmt.Fprintf(output, "rmt %s\n", version)
	switch {
	case live && running.Listen != "":
		fmt.Fprintf(output, "Local host: listening on %s (rmt %s)\n", promptText(running.Listen), promptText(running.Version))
		if running.Version != "" && running.Version != version {
			fmt.Fprintln(output, "  Running version differs; restart the host service to load the installed version.")
		}
	case live:
		fmt.Fprintln(output, "Local host: running (version and listen address unavailable)")
	case probeErr != nil:
		fmt.Fprintln(output, "Local host: not responding on its control socket")
	default:
		fmt.Fprintln(output, "Local host: not running")
	}
	if _, err := os.Lstat(filepath.Join(root, hostDisabled)); err == nil || (cfg.Serve != nil && !*cfg.Serve) {
		fmt.Fprintln(output, "  Automatic setup: disabled")
	}
	if serviceErr != nil {
		fmt.Fprintf(output, "  Service registration: unavailable (%s)\n", promptText(serviceErr.Error()))
	} else if len(registered) > 0 {
		fmt.Fprintf(output, "  Registered: %s\n", strings.Join(registered, ", "))
		if len(registered) > 1 {
			fmt.Fprintln(output, "  More than one rmt service is registered; keep only one enabled.")
		}
	} else {
		fmt.Fprintln(output, "  Registered: none")
	}
	printKnownHosts(output, cfg.Hosts, profiles)
	return nil
}

// printKnownHosts lists addresses and local pairing state, never credentials.
func printKnownHosts(output io.Writer, configured map[string]string, profiles map[string]network.Profile) {
	type entry struct{ address, state string }
	known := make(map[string]entry)
	pairedAddresses := make(map[string]bool)
	for name, profile := range profiles {
		if address, err := network.NormalizeURL(profile.URL); err == nil {
			known[strings.ToLower(name)] = entry{address, "paired"}
			pairedAddresses[strings.ToLower(address)] = true
		}
	}
	for name, raw := range configured {
		name = strings.ToLower(name)
		address, err := network.NormalizeURL(raw)
		state := "not paired"
		if err != nil {
			address, state = "(invalid address)", "not checked"
		} else if known[name].state == "paired" || pairedAddresses[strings.ToLower(address)] {
			state = "paired"
		}
		known[name] = entry{address, state}
	}
	if len(known) == 0 {
		fmt.Fprintln(output, "Known hosts: none")
		return
	}
	fmt.Fprintln(output, "Known hosts (reachability not checked):")
	names := make([]string, 0, len(known))
	for name := range known {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		host := known[name]
		fmt.Fprintf(output, "  %s  %s  (%s)\n", promptText(name), promptText(host.address), host.state)
	}
}
