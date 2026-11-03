package client

import (
	"fmt"
	"net"
	"net/http"
	"strings"

	"github.com/fschrhunt/rmt/src/config"
	"github.com/fschrhunt/rmt/src/network"
)

type target struct {
	base    string
	client  *http.Client
	profile *network.Profile
}

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
