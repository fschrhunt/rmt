package programs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/fschrhunt/rmt/src/extensions"
	"github.com/fschrhunt/rmt/src/network"
)

// TestCustomClientUsesAuthenticatedBridge verifies the native client reaches the paired host.
func TestCustomClientUsesAuthenticatedBridge(t *testing.T) {
	reached := make(chan bool, 1)
	host := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached <- r.URL.Path == "/program/test/demo/proxy/hello" && r.Header.Get("Authorization") == "Bearer host-token"
		_, _ = io.WriteString(w, "remote-ok")
	}))
	defer host.Close()
	sum := sha256.Sum256(host.Certificate().Raw)
	profile := network.Profile{URL: host.URL, Fingerprint: hex.EncodeToString(sum[:]), Token: "host-token"}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	code := fmt.Sprintf(`rmt.program({name="demo", client=function(c) return {%q, "-test.run=TestCustomHelper"} end})`, executable)
	if err := os.WriteFile(filepath.Join(dir, "demo.lua"), []byte(code), 0600); err != nil {
		t.Fatal(err)
	}
	registry, err := extensions.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("RMT_TEST_HELPER", "1")
	if err := Custom(context.Background(), registry, profile, host.URL+"/program/test/demo/proxy", "demo", "/remote/project", nil); err != nil {
		t.Fatal(err)
	}
	if !<-reached {
		t.Fatal("request did not reach the authenticated host path")
	}
}

// TestCustomHelper is the test client launched by TestCustomClientUsesAuthenticatedBridge.
func TestCustomHelper(t *testing.T) {
	if os.Getenv("RMT_TEST_HELPER") != "1" {
		return
	}
	endpoint := os.Getenv("RMT_ENDPOINT")
	u, err := url.Parse(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	for _, address := range []string{u.Scheme + "://" + u.Host + "/hello", endpoint + "/%2e%2e/shell"} {
		response, err := http.Get(address)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusNotFound {
			t.Fatalf("unsafe bridge path accepted: %d", response.StatusCode)
		}
	}
	response, err := http.Get(endpoint + "/hello")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || string(body) != "remote-ok" {
		t.Fatalf("response %q %v", body, err)
	}
}
