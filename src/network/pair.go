package network

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
)

// pairAttempts bounds guesses from one network address during a short window.
type pairAttempts struct {
	count int
	since time.Time
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
