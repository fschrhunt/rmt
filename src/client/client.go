package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/fschrhunt/rmt/src/config"
	"github.com/fschrhunt/rmt/src/network"
	"github.com/fschrhunt/rmt/src/programs"
)

// target holds a paired HTTPS connection.
type target struct {
	base    string
	client  *http.Client
	profile *network.Profile
}

// configuredAddress finds a host's optional address without changing its saved credential.
func configuredAddress(cfg config.Config, name string) string {
	for key, address := range cfg.Hosts {
		if strings.EqualFold(key, name) {
			return address
		}
	}
	return ""
}

// findProfile keeps a saved identity when its configured host name changes.
func findProfile(profiles map[string]network.Profile, name, address string) (network.Profile, bool, error) {
	if p, ok := profiles[strings.ToLower(name)]; ok {
		return p, true, nil
	}
	if address == "" {
		return network.Profile{}, false, nil
	}
	normalized, err := network.NormalizeURL(address)
	if err != nil {
		return network.Profile{}, false, err
	}
	var found network.Profile
	count := 0
	for _, p := range profiles {
		url, err := network.NormalizeURL(p.URL)
		if err == nil && strings.EqualFold(url, normalized) {
			found = p
			count++
		}
	}
	if count > 1 {
		return network.Profile{}, false, fmt.Errorf("multiple saved identities match %s", name)
	}
	return found, count == 1, nil
}

// PairAddress returns an HTTPS address only when a host needs first-time pairing.
func PairAddress(name string) (string, error) {
	cfg, err := config.Load()
	if err != nil {
		return "", err
	}
	dir, err := config.Dir()
	if err != nil {
		return "", err
	}
	profiles, err := network.Profiles(dir)
	if err != nil {
		return "", err
	}
	if _, ok, err := findProfile(profiles, name, configuredAddress(cfg, name)); err != nil {
		return "", err
	} else if ok {
		return "", nil
	}
	if address := configuredAddress(cfg, name); address != "" {
		return address, nil
	}
	if err := network.ValidateName(name); err != nil {
		return "", err
	}
	return "https://" + net.JoinHostPort(name, "47473"), nil
}

// resolveTarget connects to a paired host.
func resolveTarget(name string) (target, error) {
	cfg, err := config.Load()
	if err != nil {
		return target{}, err
	}
	dir, err := config.Dir()
	if err != nil {
		return target{}, err
	}
	profiles, err := network.Profiles(dir)
	if err != nil {
		return target{}, err
	}
	profile, ok, err := findProfile(profiles, name, configuredAddress(cfg, name))
	if err != nil {
		return target{}, err
	}
	if ok {
		if address := configuredAddress(cfg, name); address != "" {
			profile.URL, err = network.NormalizeURL(address)
			if err != nil {
				return target{}, err
			}
		}
		client, err := profile.Client()
		if err != nil {
			return target{}, err
		}
		return target{base: profile.URL, client: client, profile: &profile}, nil
	}
	return target{}, fmt.Errorf("host %q has not been paired", name)
}

// Launch opens a native program interface in the selected remote directory.
func Launch(ctx context.Context, host, programName, line string, remote *Shell) error {
	dir := remote.Directory()
	if programName != "opencode" && programName != "codex" {
		return fmt.Errorf("unknown program %q (choose opencode or codex)", programName)
	}
	target, e := resolveTarget(host)
	if e != nil {
		return e
	}
	defer target.client.CloseIdleConnections()
	res, e := target.client.Get(target.base + "/info")
	if e != nil {
		return fmt.Errorf("cannot reach %s; check that its host service is running: %w", host, e)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return fmt.Errorf("host refused connection: %s", res.Status)
	}
	var info network.Info
	if e = json.NewDecoder(io.LimitReader(res.Body, 4096)).Decode(&info); e != nil || info.Home == "" {
		return errors.New("invalid host response")
	}
	if !(config.Config{Programs: info.Programs}).Enabled(programName) {
		return fmt.Errorf("%s is not enabled on %s; check its config", programName, host)
	}
	if problem := info.Problems[programName]; problem != "" {
		return fmt.Errorf("%s: %s", host, problem)
	}
	if err := programs.Check(programName, false); err != nil {
		return err
	}
	if local, remote := programs.Version(programName), info.Versions[programName]; local != "" && remote != "" && local != remote {
		fmt.Fprintf(os.Stderr, "rmt: %s CLI versions differ (local %s, host %s); update them if the connection fails\n", programName, local, remote)
	}
	if !filepath.IsAbs(dir) {
		return errors.New("host did not report an absolute current directory")
	}
	base := target.base + "/program/" + remote.session + "/" + programName
	payload, err := json.Marshal(struct {
		Command string `json:"command"`
	}{line})
	if err != nil {
		return err
	}
	prepare, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/prepare", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	preparer := *target.client
	preparer.Timeout = 2 * time.Minute
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)
	finished := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		select {
		case <-interrupt:
			remote.Interrupt()
		case <-finished:
		}
	}()
	prepared, err := preparer.Do(prepare)
	signal.Stop(interrupt)
	close(finished)
	<-stopped
	if err != nil {
		return fmt.Errorf("cannot prepare %s on %s: %w", programName, host, err)
	}
	var launch struct {
		Directory string   `json:"directory"`
		Args      []string `json:"args"`
		Output    string   `json:"output"`
		Error     string   `json:"error"`
	}
	err = json.NewDecoder(io.LimitReader(prepared.Body, 2<<20)).Decode(&launch)
	prepared.Body.Close()
	if launch.Output != "" {
		fmt.Fprint(os.Stderr, launch.Output)
	}
	if err != nil || prepared.StatusCode != http.StatusOK || !filepath.IsAbs(launch.Directory) {
		if launch.Error != "" {
			return fmt.Errorf("cannot prepare %s on %s: %s", programName, host, launch.Error)
		}
		return fmt.Errorf("cannot prepare %s on %s: invalid host response (%s)", programName, host, prepared.Status)
	}
	dir, extra := launch.Directory, launch.Args
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/start", nil)
	if err != nil {
		return err
	}
	starter := *target.client
	starter.Timeout = 25 * time.Second
	response, err := starter.Do(request)
	if err != nil {
		return fmt.Errorf("cannot start %s on %s: %w", programName, host, err)
	}
	if response.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 1024))
		response.Body.Close()
		return fmt.Errorf("cannot start %s on %s: %s", programName, host, strings.TrimSpace(string(body)))
	}
	response.Body.Close()

	if programName == "opencode" {
		transport, err := target.profile.Transport()
		if err != nil {
			return err
		}
		endpoint, err := url.Parse(base + "/proxy")
		if err != nil {
			return err
		}
		return programs.OpenCodeAt(endpoint, transport, dir, extra)
	}
	return programs.CodexDirect(*target.profile, base+"/proxy", dir, extra)
}
