package cli

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/fschrhunt/rmt/src/config"
	"github.com/fschrhunt/rmt/src/network"
	"github.com/fschrhunt/rmt/src/services"
)

const hostDisabled = "host-disabled"

// startLocalHost respects explicit and legacy opt-outs on the first connection.
func startLocalHost(root string, cfg config.Config, setup func() error) error {
	if cfg.Serve != nil && !*cfg.Serve {
		return nil
	}
	if _, err := os.Lstat(filepath.Join(root, hostDisabled)); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return setup()
}

// rememberHostDisabled records an opt-out even when a service has never been installed.
func rememberHostDisabled(root string) error {
	if err := network.PrepareStorage(root); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(root, hostDisabled), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if errors.Is(err, os.ErrExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return f.Close()
}

// manageHost applies an explicit service change without removing pairing state.
func manageHost(action string) error {
	root, err := config.Dir()
	if err != nil {
		return err
	}
	if action == "disable" {
		if err := rememberHostDisabled(root); err != nil {
			return err
		}
		return services.Disable()
	}
	if _, err := config.Read(filepath.Join(root, "settings.toml")); err != nil {
		return err
	}
	if err := services.Enable(); err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(root, hostDisabled)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
