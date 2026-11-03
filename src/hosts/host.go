// Package hosts runs program backends and their network listeners.
package hosts

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/fschrhunt/rmt/src/config"
	"github.com/fschrhunt/rmt/src/extensions"
	"github.com/fschrhunt/rmt/src/network"
	"github.com/fschrhunt/rmt/src/programs"
)

// Run starts the configured host and its native program backends.
func Run() error {
	cfg, e := config.Load()
	if e != nil {
		return e
	}
	if cfg.Listen == "" {
		cfg.Listen = "0.0.0.0:47473"
	}
	root, err := config.Dir()
	if err != nil {
		return err
	}
	registry, err := extensions.Load(filepath.Join(root, "extensions"))
	if err != nil {
		return fmt.Errorf("load extensions: %w", err)
	}
	home, e := os.UserHomeDir()
	if e != nil || home == "" {
		return errors.New("cannot determine host home directory")
	}
	if resolved, err := filepath.EvalSymlinks(home); err == nil {
		home = resolved
	}
	if st, e := os.Stat(home); e != nil || !st.IsDir() {
		return fmt.Errorf("host home %s is not a directory", home)
	}
	var direct net.Listener
	var paired *network.Host
	if cfg.Listen != "" {
		path, _, err := config.Path()
		if err != nil {
			return err
		}
		paired, err = network.NewHost(filepath.Dir(path))
		if err != nil {
			return fmt.Errorf("paired host identity: %w", err)
		}
		direct, err = net.Listen("tcp", cfg.Listen)
		if err != nil {
			return fmt.Errorf("listen on %s: %w", cfg.Listen, err)
		}
		defer direct.Close()
		if err := paired.StartLocal(); err != nil {
			return fmt.Errorf("paired host control: %w", err)
		}
		defer paired.Close()
	}
	versions := map[string]string{}
	problems := map[string]string{}
	for _, name := range []string{"opencode", "codex"} {
		if cfg.Enabled(name) {
			versions[name] = programs.Version(name)
			if err := programs.Check(name, true); err != nil {
				problems[name] = err.Error()
				fmt.Fprintln(os.Stderr, "rmt:", err)
			}
		}
	}
	for _, name := range cfg.Programs {
		if name != "opencode" && name != "codex" && !registry.Has(name) {
			problems[name] = "program extension is not installed on this host"
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	available := cfg.Programs
	if available == nil {
		available = append([]string{"opencode", "codex"}, registry.Programs()...)
	}
	control := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/info" || r.Method != "GET" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(network.Info{Home: home, Programs: available, Problems: problems, Versions: versions})
	})
	commands := shellHandler(home, cfg.Shell)
	commands.authorized = paired.Authorized
	commands.enabled = func(name string) bool {
		return (cfg.Programs == nil && (name == "opencode" || name == "codex" || registry.Has(name))) || cfg.Enabled(name)
	}
	commands.extensions = registry
	commands.problems = problems
	defer commands.Close()
	failed := make(chan error, 1)
	if paired != nil {
		mux := http.NewServeMux()
		mux.HandleFunc("/identity", paired.Pairing)
		mux.HandleFunc("/pair", paired.Pairing)
		mux.Handle("/info", paired.Auth(control))
		mux.Handle("/shell", paired.Auth(commands))
		mux.Handle("/complete", paired.Auth(completionHandler(home)))
		mux.Handle("/program/", paired.Auth(http.HandlerFunc(commands.serveProgram)))
		srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
		go func() { failed <- srv.Serve(tls.NewListener(direct, paired.TLS())) }()
		fmt.Fprintf(os.Stderr, "rmt: paired HTTPS listening on %s\n", cfg.Listen)
	}
	name, _ := os.Hostname()
	fmt.Fprintf(os.Stderr, "rmt: serving %s on %s\n", home, name)
	select {
	case <-ctx.Done():
		return nil
	case e := <-failed:
		return e
	}
}
