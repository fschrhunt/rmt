package network

import (
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Host owns the TLS identity, single-use code and paired-device registry.
type Host struct {
	dir          string
	cert         tls.Certificate
	fingerprint  string
	mu           sync.Mutex
	code         string
	expires      time.Time
	attempts     map[string]pairAttempts
	devices      []Device
	registryErr  error
	invalidSince time.Time
	active       map[string]map[*access]struct{}
	local        net.Listener
	server       *http.Server
	watchStop    chan struct{}
}

// NewHost loads a private identity and existing device registry.
func NewHost(dir string) (*Host, error) {
	cert, fingerprint, err := identity(dir)
	if err != nil {
		return nil, err
	}
	h := &Host{dir: dir, cert: cert, fingerprint: fingerprint, active: make(map[string]map[*access]struct{})}
	data, err := readPrivate(filepath.Join(dir, "devices.json"))
	if err == nil {
		if json.Unmarshal(data, &h.devices) != nil {
			return nil, errors.New("invalid paired devices file")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	h.refreshDevices()
	if h.registryErr != nil {
		return nil, fmt.Errorf("invalid paired devices file: %w", h.registryErr)
	}
	return h, nil
}

// TLS returns this host's persistent TLS configuration.
func (h *Host) TLS() *tls.Config {
	return &tls.Config{Certificates: []tls.Certificate{h.cert}, MinVersion: tls.VersionTLS12}
}
