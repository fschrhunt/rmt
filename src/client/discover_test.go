package client

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/fschrhunt/rmt/src/network"
)

// TestPairedSessionsDoNotRequireTailscale scans multiple pinned hosts without a VPN.
func TestPairedSessionsDoNotRequireTailscale(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("PATH", t.TempDir())
	dir := filepath.Join(home, "config", "rmt")
	for _, name := range []string{"one", "two"} {
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer test-token" {
				http.Error(w, "unauthorized", 401)
				return
			}
			switch r.URL.Path {
			case "/info":
				fmt.Fprint(w, `{"workspace":"/projects","agents":["opencode"]}`)
			case "/opencode/api/session/active":
				fmt.Fprint(w, `{"data":{}}`)
			case "/opencode/api/session":
				fmt.Fprint(w, `{"data":[{"id":"session","location":{"directory":"/projects"}}]}`)
			default:
				http.NotFound(w, r)
			}
		}))
		t.Cleanup(srv.Close)
		sum := sha256.Sum256(srv.Certificate().Raw)
		profile := network.Profile{URL: srv.URL, Fingerprint: hex.EncodeToString(sum[:]), Token: "test-token"}
		if err := network.SaveProfile(dir, name, profile); err != nil {
			t.Fatal(err)
		}
	}
	sessions, problems, complete, err := ScanSessions("")
	if err != nil || !complete || len(problems) != 0 || len(sessions) != 2 {
		t.Fatalf("sessions=%v problems=%v complete=%v err=%v", sessions, problems, complete, err)
	}
	filtered, problems, complete, err := ScanSessions("one")
	if err != nil || !complete || len(problems) != 0 || len(filtered) != 1 || filtered[0].Host != "one" {
		t.Fatalf("filtered=%v problems=%v complete=%v err=%v", filtered, problems, complete, err)
	}
}
