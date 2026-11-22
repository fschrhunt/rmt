package cli

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fschrhunt/rmt/src/network"
)

// pairingFixture starts a host offering HTTPS pairing in root/host, sets HOME to root/client,
// and installs a fake ssh that records its arguments in root/ssh-args and then runs script.
func pairingFixture(t *testing.T, script string) (root, url string, host *network.Host) {
	t.Helper()
	root, err := os.MkdirTemp("/tmp", "rmt-pair-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	host, err = network.NewHost(filepath.Join(root, "host"))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(host.Pairing))
	server.TLS = host.TLS()
	server.StartTLS()
	t.Cleanup(server.Close)
	bin := filepath.Join(root, "bin")
	if err := os.Mkdir(bin, 0700); err != nil {
		t.Fatal(err)
	}
	fake := "#!/bin/sh\nprintf '%s\\n' \"$@\" > '" + filepath.Join(root, "ssh-args") + "'\n" + script + "\n"
	if err := os.WriteFile(filepath.Join(bin, "ssh"), []byte(fake), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	t.Setenv("HOME", filepath.Join(root, "client"))
	return root, server.URL, host
}

func TestPairOverSSHNeedsNoPrompts(t *testing.T) {
	// The fake ssh plays the host: shell startup noise, then `rmt --pair-code` output.
	root, url, host := pairingFixture(t, `echo welcome; cat "$(dirname "$HOME")/code.json"`)
	if err := host.StartLocal(network.HostStatus{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(host.Close)
	var code map[string]string
	if err := network.Control(filepath.Join(root, "host"), http.MethodPost, "/code", nil, &code); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(code)
	if err := os.WriteFile(filepath.Join(root, "code.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := pairHost("fat", url, bufio.NewReader(strings.NewReader("")), &out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	profiles, err := network.Profiles(filepath.Join(root, "client", ".rmt"))
	if err != nil || profiles["fat"].Token == "" || profiles["fat"].Fingerprint != code["fingerprint"] {
		t.Fatalf("profile not saved with pinned identity: %v %+v", err, profiles)
	}
	args, _ := os.ReadFile(filepath.Join(root, "ssh-args"))
	if !strings.Contains(string(args), "--\nfat\n") {
		t.Fatalf("ssh must not parse the host name as an option: %q", args)
	}
}

func TestPairFallsBackToManualWhenSSHFails(t *testing.T) {
	_, url, _ := pairingFixture(t, "exit 255")
	var out strings.Builder
	err := pairHost("fat", url, bufio.NewReader(strings.NewReader("n\n")), &out)
	if err == nil || err.Error() != "pairing cancelled" || !strings.Contains(out.String(), "Verification: ") {
		t.Fatalf("manual pairing not offered: %v\n%s", err, out.String())
	}
}
