package network

import (
	"bufio"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"strings"
)

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
