package network

import (
	"context"
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// TestPairingPinsIdentityAndConsumesCode protects the pairing and credential boundary.
func TestPairingPinsIdentityAndConsumesCode(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "rmt")
	h, err := NewHost(dir)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/identity", h.Pairing)
	mux.HandleFunc("/pair", h.Pairing)
	mux.Handle("/info", h.Auth(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })))
	server := httptest.NewUnstartedServer(mux)
	server.TLS = &tls.Config{Certificates: []tls.Certificate{h.cert}}
	server.StartTLS()
	defer server.Close()
	_, fingerprint, err := Inspect(context.Background(), server.URL)
	if err != nil || fingerprint != h.fingerprint {
		t.Fatalf("inspect: %s %v", fingerprint, err)
	}
	code, err := h.codeForOwner()
	if err != nil {
		t.Fatal(err)
	}
	wrong := strings.Repeat("0", 64)
	if wrong == fingerprint {
		wrong = strings.Repeat("1", 64)
	}
	if _, err := Pair(context.Background(), server.URL, wrong, code["code"], "client"); err == nil {
		t.Fatal("wrong fingerprint accepted")
	}
	profile, err := Pair(context.Background(), server.URL, fingerprint, code["code"], "client")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Pair(context.Background(), server.URL, fingerprint, code["code"], "other"); err == nil {
		t.Fatal("pairing code reused")
	}
	client, err := profile.Client()
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Get(server.URL + "/info")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("authorized status %d", resp.StatusCode)
	}
	if len(h.devices) != 1 {
		t.Fatalf("devices: %d", len(h.devices))
	}
	restarted, err := NewHost(dir)
	if err != nil || restarted.fingerprint != fingerprint || len(restarted.devices) != 1 {
		t.Fatalf("identity or device not preserved across restart: %v", err)
	}
	if err := h.revoke(h.devices[0].ID); err != nil {
		t.Fatal(err)
	}
	resp, err = client.Get(server.URL + "/info")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("revoked status %d", resp.StatusCode)
	}
}

// TestRevocationClosesWebSocket confirms a paired device cannot keep an existing session open.
func TestRevocationClosesWebSocket(t *testing.T) {
	h, err := NewHost(filepath.Join(t.TempDir(), "rmt"))
	if err != nil {
		t.Fatal(err)
	}
	code, err := h.codeForOwner()
	if err != nil {
		t.Fatal(err)
	}
	token, err := h.enroll(code["code"], "client")
	if err != nil {
		t.Fatal(err)
	}
	accepted := make(chan struct{})
	handler := h.Auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		close(accepted)
		_, _, _ = conn.Read(r.Context())
	}))
	server := httptest.NewUnstartedServer(handler)
	server.TLS = &tls.Config{Certificates: []tls.Certificate{h.cert}}
	server.StartTLS()
	defer server.Close()
	profile := Profile{URL: server.URL, Fingerprint: h.fingerprint, Token: token}
	client, err := profile.Client()
	if err != nil {
		t.Fatal(err)
	}
	client.Timeout = 0
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "wss://"+strings.TrimPrefix(server.URL, "https://"), &websocket.DialOptions{HTTPClient: client})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	<-accepted
	if err := h.revoke(h.devices[0].ID); err != nil {
		t.Fatal(err)
	}
	_, _, err = conn.Read(ctx)
	if err == nil || ctx.Err() != nil {
		t.Fatalf("connection not promptly closed: %v", err)
	}
}

// TestProfilesArePrivateAndDoNotOverwrite protects stored device credentials.
func TestProfilesArePrivateAndDoNotOverwrite(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "rmt")
	profile := Profile{URL: "https://example.test:47473", Fingerprint: strings.Repeat("a", 64), Token: "test"}
	if err := SaveProfile(dir, "mini", profile); err != nil {
		t.Fatal(err)
	}
	if err := SaveProfile(dir, "mini", profile); err == nil {
		t.Fatal("replaced existing profile")
	}
	info, err := os.Stat(filepath.Join(dir, "hosts.json"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("credentials permissions: %v %v", info, err)
	}
	if err := RemoveProfile(dir, "mini"); err != nil {
		t.Fatal(err)
	}
	profiles, err := Profiles(dir)
	if err != nil || len(profiles) != 0 {
		t.Fatalf("profiles: %v, %v", profiles, err)
	}
}

// TestLocalControlIsPrivate creates and rotates pairing codes over an owner-only socket.
func TestLocalControlIsPrivate(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "rmt-control-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	h, err := NewHost(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.StartLocal(); err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	info, err := os.Stat(filepath.Join(dir, "host.sock"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("socket permissions: %v %v", info, err)
	}
	var first, second map[string]string
	if err := Control(dir, http.MethodPost, "/code", nil, &first); err != nil {
		t.Fatal(err)
	}
	if err := Control(dir, http.MethodPost, "/code", nil, &second); err != nil {
		t.Fatal(err)
	}
	if first["code"] == "" || first["code"] == second["code"] {
		t.Fatal("code was not rotated")
	}
	if _, err := h.enroll(first["code"], "client"); err == nil {
		t.Fatal("replaced code remained usable")
	}
	if _, err := h.enroll(second["code"], "client"); err != nil {
		t.Fatal(err)
	}
}

// TestPairingCodeCanOnlyBeUsedOnceConcurrently prevents multiple devices claiming one code.
func TestPairingCodeCanOnlyBeUsedOnceConcurrently(t *testing.T) {
	h, err := NewHost(filepath.Join(t.TempDir(), "rmt"))
	if err != nil {
		t.Fatal(err)
	}
	code, err := h.codeForOwner()
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var successes atomic.Int32
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := h.enroll(code["code"], "client"); err == nil {
				successes.Add(1)
			}
		}()
	}
	wg.Wait()
	if successes.Load() != 1 {
		t.Fatalf("successful pairings: %d", successes.Load())
	}
}
