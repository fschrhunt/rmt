package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/fschrhunt/rmt/src/config"
	"github.com/fschrhunt/rmt/src/extensions"
	"github.com/fschrhunt/rmt/src/network"
	"github.com/fschrhunt/rmt/src/programs"
)

// Launch opens a native program interface in the selected remote directory.
func Launch(ctx context.Context, host, programName, line string, remote *Shell, registry *extensions.Registry) error {
	dir := remote.Directory()
	if programName != "opencode" && programName != "codex" && !registry.Has(programName) {
		return fmt.Errorf("unknown program %q", programName)
	}
	target, e := resolveTarget(host)
	if e != nil {
		return e
	}
	defer target.client.CloseIdleConnections()
	infoRequest, e := http.NewRequestWithContext(ctx, http.MethodGet, target.base+"/info", nil)
	if e != nil {
		return e
	}
	res, e := target.client.Do(infoRequest)
	if e != nil {
		return fmt.Errorf("check %s on %s: %w", programName, host, dialError(host, target.base, e))
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return fmt.Errorf("check %s on %s: host returned %s", programName, host, res.Status)
	}
	var info network.Info
	if e = json.NewDecoder(io.LimitReader(res.Body, 4096)).Decode(&info); e != nil || info.Home == "" {
		return fmt.Errorf("check %s on %s: invalid host response", programName, host)
	}
	if !(config.Config{Programs: info.Programs}).Enabled(programName) {
		return fmt.Errorf("%s is not enabled on %s; check its config", programName, host)
	}
	if problem := info.Problems[programName]; problem != "" {
		return fmt.Errorf("%s: %s", host, problem)
	}
	if programName == "opencode" || programName == "codex" {
		if err := programs.Check(programName, false); err != nil {
			return fmt.Errorf("check local %s client: %w", programName, err)
		}
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
	startContext, stopStart := signal.NotifyContext(ctx, os.Interrupt)
	defer stopStart()
	request, err := http.NewRequestWithContext(startContext, http.MethodPost, base+"/start", nil)
	if err != nil {
		return err
	}
	starter := *target.client
	starter.Timeout = 25 * time.Second
	response, err := starter.Do(request)
	stopStart()
	if err != nil {
		return fmt.Errorf("cannot start %s backend on %s: %w", programName, host, err)
	}
	if response.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 1024))
		response.Body.Close()
		detail := strings.TrimSpace(string(body))
		if detail == "" {
			detail = response.Status
		}
		return fmt.Errorf("cannot start %s backend on %s: %s", programName, host, detail)
	}
	response.Body.Close()

	if programName == "opencode" {
		transport, bridgeErr := target.profile.Transport()
		if bridgeErr != nil {
			return fmt.Errorf("configure local OpenCode bridge: %w", bridgeErr)
		}
		endpoint, bridgeErr := url.Parse(base + "/proxy")
		if bridgeErr != nil {
			return fmt.Errorf("configure local OpenCode bridge: %w", bridgeErr)
		}
		err = programs.OpenCodeAt(ctx, endpoint, transport, dir, extra)
	} else if programName == "codex" {
		err = programs.CodexDirect(ctx, *target.profile, base+"/proxy", dir, extra)
	} else {
		err = programs.Custom(ctx, registry, *target.profile, base+"/proxy", programName, dir, extra)
	}
	if err != nil {
		if hostVersion := info.Versions[programName]; ctx.Err() == nil && hostVersion != "" {
			if local := programs.Version(programName); local != "" && local != hostVersion {
				return fmt.Errorf("local %s UI: %w (local version %s, host version %s)", programName, err, local, hostVersion)
			}
		}
		return fmt.Errorf("local %s UI: %w", programName, err)
	}
	return nil
}
