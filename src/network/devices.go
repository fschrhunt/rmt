package network

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// Device identifies a paired client; token hashes stay in the private registry.
type Device struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	TokenHash string `json:"token_hash,omitempty"`
}

// refreshDevices applies owner edits and disconnects devices removed from the registry.
func (h *Host) refreshDevices() {
	h.mu.Lock()
	data, err := readPrivate(filepath.Join(h.dir, "devices.json"))
	var devices []Device
	if errors.Is(err, os.ErrNotExist) {
		err = nil
	} else if err == nil {
		err = json.Unmarshal(data, &devices)
	}
	if len(devices) > 64 {
		err = errors.New("too many devices")
	}
	seen := map[string]bool{}
	for _, d := range devices {
		hash, e := hex.DecodeString(d.TokenHash)
		if d.ID == "" || seen[d.ID] || e != nil || len(hash) != sha256.Size {
			err = errors.New("invalid device")
			break
		}
		seen[d.ID] = true
	}
	h.registryErr = err
	if err != nil {
		if h.invalidSince.IsZero() {
			h.invalidSince = time.Now()
		}
		if time.Since(h.invalidSince) < 2*time.Second {
			h.mu.Unlock()
			return
		}
		devices = nil
	} else {
		h.invalidSince = time.Time{}
	}
	allowed := map[string]bool{}
	for _, d := range devices {
		allowed[d.TokenHash] = true
	}
	h.devices = devices
	h.registryErr = err
	var revoked []*access
	for hash, entries := range h.active {
		if !allowed[hash] {
			for a := range entries {
				revoked = append(revoked, a)
			}
			delete(h.active, hash)
		}
	}
	h.mu.Unlock()
	for _, a := range revoked {
		a.cancel()
		if a.conn != nil {
			_ = a.conn.Close()
		}
	}
}

// watchDevices reloads the owner-managed registry and applies revocations.
func (h *Host) watchDevices(stop <-chan struct{}) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			h.refreshDevices()
		case <-stop:
			return
		}
	}
}

// revoke removes a device and terminates its active requests and upgraded connections.
func (h *Host) revoke(id string) error {
	h.mu.Lock()
	devices := make([]Device, 0, len(h.devices))
	hash := ""
	for _, d := range h.devices {
		if d.ID == id {
			hash = d.TokenHash
		} else {
			devices = append(devices, d)
		}
	}
	if hash == "" {
		h.mu.Unlock()
		return errors.New("device not found")
	}
	data, err := json.MarshalIndent(devices, "", "  ")
	if err == nil {
		err = writePrivate(filepath.Join(h.dir, "devices.json"), append(data, '\n'))
	}
	if err != nil {
		h.mu.Unlock()
		return err
	}
	h.devices = devices
	var active []*access
	for a := range h.active[hash] {
		active = append(active, a)
	}
	delete(h.active, hash)
	h.mu.Unlock()
	for _, a := range active {
		a.cancel()
		if a.conn != nil {
			_ = a.conn.Close()
		}
	}
	return nil
}

// StartLocal opens an owner-only Unix socket for generating codes and managing devices.
func (h *Host) StartLocal() error {
	if err := privateDir(h.dir); err != nil {
		return err
	}
	path := filepath.Join(h.dir, "host.sock")
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return errors.New("host control path is not a socket")
		}
		c, err := net.DialTimeout("unix", path, 200*time.Millisecond)
		if err == nil {
			c.Close()
			return errors.New("another host is already running")
		}
		if err := os.Remove(path); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	l, err := net.Listen("unix", path)
	if err != nil {
		return err
	}
	if err = os.Chmod(path, 0600); err != nil {
		l.Close()
		return err
	}
	h.local = l
	h.watchStop = make(chan struct{})
	go h.watchDevices(h.watchStop)
	h.server = &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(h.localControl)}
	go func() { _ = h.server.Serve(l) }()
	return nil
}

// Close stops the local control socket and removes it.
func (h *Host) Close() {
	if h.watchStop != nil {
		close(h.watchStop)
		h.watchStop = nil
	}
	if h.server != nil {
		_ = h.server.Close()
	}
	if h.local != nil {
		_ = h.local.Close()
		_ = os.Remove(filepath.Join(h.dir, "host.sock"))
	}
}

// localControl serves device management only through the private Unix socket.
func (h *Host) localControl(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/code":
		result, err := h.codeForOwner()
		if err != nil {
			http.Error(w, "cannot create code", 500)
			return
		}
		_ = json.NewEncoder(w).Encode(result)
	case r.Method == http.MethodGet && r.URL.Path == "/devices":
		h.mu.Lock()
		devices := make([]Device, len(h.devices))
		copy(devices, h.devices)
		h.mu.Unlock()
		for i := range devices {
			devices[i].TokenHash = ""
		}
		_ = json.NewEncoder(w).Encode(devices)
	case r.Method == http.MethodPost && r.URL.Path == "/revoke":
		var req struct {
			ID string `json:"id"`
		}
		if json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req) != nil {
			http.Error(w, "invalid request", 400)
			return
		}
		if err := h.revoke(req.ID); err != nil {
			http.Error(w, "cannot revoke device", 400)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.NotFound(w, r)
	}
}

// Control sends a management request over the owner's Unix socket.
func Control(dir, method, path string, body io.Reader, out any) error {
	if err := privateDir(dir); err != nil {
		return err
	}
	tr := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", filepath.Join(dir, "host.sock"))
	}}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, Timeout: 5 * time.Second}
	req, err := http.NewRequest(method, "http://localhost"+path, body)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("cannot reach local host service: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("host control: %s", resp.Status)
	}
	if out != nil {
		return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out)
	}
	return nil
}
