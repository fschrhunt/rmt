package network

import (
	"bufio"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"
)

// Device identifies a paired client; token hashes stay in the private registry.
type Device struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	TokenHash string `json:"token_hash,omitempty"`
}

// pairAttempts bounds guesses from one network address during a short window.
type pairAttempts struct {
	count int
	since time.Time
}

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

// TLS returns this host's persistent TLS configuration.
func (h *Host) TLS() *tls.Config {
	return &tls.Config{Certificates: []tls.Certificate{h.cert}, MinVersion: tls.VersionTLS12}
}

// codeForOwner creates an expiring one-time code for the local owner.
func (h *Host) codeForOwner() (map[string]string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.newCodeLocked()
}

// newCodeLocked creates a short-lived pairing file readable only by the host user.
func (h *Host) newCodeLocked() (map[string]string, error) {
	code, err := randomToken(12)
	if err != nil {
		return nil, err
	}
	expires := time.Now().Add(15 * time.Minute)
	result := map[string]string{"code": code, "fingerprint": h.fingerprint, "verify": h.fingerprint[:32], "expires": expires.Format(time.RFC3339)}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := writePrivate(filepath.Join(h.dir, "pairing.json"), append(data, '\n')); err != nil {
		return nil, err
	}
	h.code, h.expires = code, expires
	return result, nil
}

// ensureCode creates a pairing file on the first connection, or after it expires.
func (h *Host) ensureCode() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.code != "" && time.Now().Before(h.expires) {
		return nil
	}
	_, err := h.newCodeLocked()
	return err
}

// enroll exchanges a one-time code for a unique, persistent device token.
func (h *Host) enroll(code, name string) (string, error) {
	h.refreshDevices()
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.registryErr != nil {
		return "", errors.New("invalid paired devices file")
	}
	if h.code == "" || time.Now().After(h.expires) {
		return "", errors.New("pairing code expired or unavailable")
	}
	if subtle.ConstantTimeCompare([]byte(code), []byte(h.code)) != 1 {
		return "", errors.New("invalid pairing code")
	}
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 80 || strings.IndexFunc(name, unicode.IsControl) >= 0 || len(h.devices) >= 64 {
		return "", errors.New("invalid device name or device limit reached")
	}
	token, err := randomToken(32)
	if err != nil {
		return "", err
	}
	id, err := randomToken(9)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(token))
	devices := append(append([]Device(nil), h.devices...), Device{ID: id, Name: name, TokenHash: hex.EncodeToString(sum[:])})
	data, err := json.MarshalIndent(devices, "", "  ")
	if err != nil {
		return "", err
	}
	if err := writePrivate(filepath.Join(h.dir, "devices.json"), append(data, '\n')); err != nil {
		return "", err
	}
	h.devices, h.code = devices, ""
	_ = os.Remove(filepath.Join(h.dir, "pairing.json"))
	return token, nil
}

// access tracks a request and any upgraded connection until it ends or is revoked.
type access struct {
	cancel context.CancelFunc
	conn   net.Conn
}

// upgradeWriter records upgraded connections so revocation can close them immediately.
type upgradeWriter struct {
	http.ResponseWriter
	host   *Host
	hash   string
	access *access
}

// Unwrap lets HTTP helpers access optional features of the original writer.
func (w *upgradeWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// Hijack registers an upgraded connection while the device is still authorized.
func (w *upgradeWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, rw, err := http.NewResponseController(w.ResponseWriter).Hijack()
	if err != nil {
		return nil, nil, err
	}
	w.host.mu.Lock()
	_, ok := w.host.active[w.hash][w.access]
	if ok {
		w.access.conn = conn
	}
	w.host.mu.Unlock()
	if !ok {
		conn.Close()
		return nil, nil, errors.New("device revoked")
	}
	return conn, rw, nil
}

// deviceContext identifies the authenticated device without exposing its token.
type deviceContext struct{}

// DeviceKey returns the paired device's token hash for binding host sessions.
func DeviceKey(ctx context.Context) string { key, _ := ctx.Value(deviceContext{}).(string); return key }

// Authorized reports whether the paired device still has access.
func (h *Host) Authorized(key string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.registryErr != nil {
		return false
	}
	for _, d := range h.devices {
		if subtle.ConstantTimeCompare([]byte(key), []byte(d.TokenHash)) == 1 {
			return true
		}
	}
	return false
}

// Auth restricts each HTTPS request to a currently paired device.
func (h *Host) Auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if token == r.Header.Get("Authorization") || token == "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		sum := sha256.Sum256([]byte(token))
		hash := hex.EncodeToString(sum[:])
		ctx, cancel := context.WithCancel(r.Context())
		a := &access{cancel: cancel}
		h.mu.Lock()
		allowed := false
		for _, d := range h.devices {
			if h.registryErr != nil {
				break
			}
			if subtle.ConstantTimeCompare([]byte(hash), []byte(d.TokenHash)) == 1 {
				allowed = true
				break
			}
		}
		if allowed {
			if h.active[hash] == nil {
				h.active[hash] = make(map[*access]struct{})
			}
			h.active[hash][a] = struct{}{}
		}
		h.mu.Unlock()
		if !allowed {
			cancel()
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		defer func() {
			h.mu.Lock()
			delete(h.active[hash], a)
			if len(h.active[hash]) == 0 {
				delete(h.active, hash)
			}
			h.mu.Unlock()
			cancel()
		}()
		r.Header.Del("Authorization")
		next.ServeHTTP(&upgradeWriter{ResponseWriter: w, host: h, hash: hash, access: a}, r.WithContext(context.WithValue(ctx, deviceContext{}, hash)))
	})
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

// allowPairAttempt limits unauthenticated guesses without invalidating another user's code.
func (h *Host) allowPairAttempt(address string) bool {
	ip, _, err := net.SplitHostPort(address)
	if err != nil {
		ip = address
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	now := time.Now()
	if h.attempts == nil {
		h.attempts = make(map[string]pairAttempts)
	}
	for key, attempt := range h.attempts {
		if now.Sub(attempt.since) > time.Minute {
			delete(h.attempts, key)
		}
	}
	attempt, ok := h.attempts[ip]
	if !ok {
		if len(h.attempts) >= 1024 {
			return false
		}
		attempt.since = now
	}
	if attempt.count >= 5 {
		return false
	}
	attempt.count++
	h.attempts[ip] = attempt
	return true
}

// Pairing exposes identity and one-time pairing without exposing other endpoints.
func (h *Host) Pairing(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/identity":
		if err := h.ensureCode(); err != nil {
			http.Error(w, "pairing unavailable", http.StatusServiceUnavailable)
			return
		}
		name, _ := os.Hostname()
		_ = json.NewEncoder(w).Encode(map[string]string{"name": name})
	case r.Method == http.MethodPost && r.URL.Path == "/pair":
		if !h.allowPairAttempt(r.RemoteAddr) {
			http.Error(w, "too many attempts", http.StatusTooManyRequests)
			return
		}
		_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(5 * time.Second))
		defer http.NewResponseController(w).SetReadDeadline(time.Time{})
		var req struct {
			Code string `json:"code"`
			Name string `json:"name"`
		}
		if json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req) != nil {
			http.Error(w, "invalid request", 400)
			return
		}
		token, err := h.enroll(req.Code, req.Name)
		if err != nil {
			http.Error(w, "pairing refused", 403)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"token": token})
	default:
		http.NotFound(w, r)
	}
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
