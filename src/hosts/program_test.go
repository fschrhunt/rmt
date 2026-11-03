package hosts

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestSharedProgramRemainsAvailable verifies detaching one window never stops a native service.
func TestSharedProgramRemainsAvailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "opencode" || pass != "test-password" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	dir := t.TempDir()
	script := filepath.Join(dir, "opencode")
	content := `#!/bin/sh
case "$2" in
 status) printf '%s\n' "$RMT_TEST_URL" ;;
 get) printf '%s\n' '"test-password"' ;;
 *) printf '%s\n' "$2" >> "$RMT_TEST_CALLS" ;;
esac
`
	if err := os.WriteFile(script, []byte(content), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("RMT_TEST_URL", server.URL)
	calls := filepath.Join(dir, "calls")
	t.Setenv("RMT_TEST_CALLS", calls)
	manager := shellHandler(t.TempDir(), "")
	defer manager.Close()
	first, _, err := manager.acquire("", "")
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := manager.acquire("", "")
	if err != nil {
		t.Fatal(err)
	}
	start := func(s *shellSession) {
		t.Helper()
		w := httptest.NewRecorder()
		manager.serveProgram(w, httptest.NewRequest(http.MethodPost, fmt.Sprintf("/program/%s/opencode/start", s.token), nil))
		if w.Code != http.StatusNoContent {
			t.Fatalf("start: %d %s", w.Code, w.Body.String())
		}
	}
	start(first)
	start(second)
	manager.stop(first)
	second.mu.Lock()
	attached := second.program != nil
	second.mu.Unlock()
	if !attached {
		t.Fatal("other window detached")
	}
	manager.stop(second)

	if data, err := os.ReadFile(calls); err == nil && len(data) > 0 {
		t.Fatal("native service was started or stopped unnecessarily")
	}
}

// TestActiveProgramSurvivesControlDisconnect retains the proxy while its native client is using it.
func TestActiveProgramSurvivesControlDisconnect(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
		w.WriteHeader(http.StatusNoContent)
	}))
	defer backend.Close()
	manager := shellHandler(t.TempDir(), "/bin/sh")
	manager.grace = 40 * time.Millisecond
	defer manager.Close()
	session, _, err := manager.acquire("", "")
	if err != nil {
		t.Fatal(err)
	}
	target, err := url.Parse(backend.URL)
	if err != nil {
		t.Fatal(err)
	}
	session.program = &programBackend{name: "opencode", target: target}
	done := make(chan struct{})
	go func() {
		defer close(done)
		manager.serveProgram(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/program/"+session.token+"/opencode/proxy/events", nil))
	}()
	<-started
	manager.detach(session)
	time.Sleep(3 * manager.grace)
	manager.mu.Lock()
	retained := manager.sessions[session.token] == session
	manager.mu.Unlock()
	close(release)
	<-done
	if !retained {
		t.Fatal("active native client lost its proxy")
	}
	select {
	case <-session.sh.done:
	case <-time.After(3 * time.Second):
		t.Fatal("idle shell remained after the native client disconnected")
	}
}

// TestProgramEndpointRejectsOtherDevices protects session-owned connections.
func TestProgramEndpointRejectsOtherDevices(t *testing.T) {
	manager := shellHandler(t.TempDir(), "")
	defer manager.Close()
	s, _, err := manager.acquire("", "owner")
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	manager.serveProgram(w, httptest.NewRequest(http.MethodPost, "/program/"+s.token+"/opencode/start", strings.NewReader("")))
	if w.Code != http.StatusNotFound {
		t.Fatalf("unauthorized status %d", w.Code)
	}
}

// TestLocalServiceURL refuses to send native credentials to a remote address.
func TestLocalServiceURL(t *testing.T) {
	for _, addr := range []string{"http://example.com:1234", "https://127.0.0.1:1234", "http://127.0.0.1:1234/path"} {
		if _, err := localServiceURL(addr); err == nil {
			t.Errorf("accepted %s", addr)
		}
	}
	if _, err := localServiceURL("http://127.0.0.1:1234"); err != nil {
		t.Fatal(err)
	}
	got, err := localServiceURL("http://[::]:1234")
	if err != nil || got.Host != "[::1]:1234" {
		t.Fatalf("IPv6 listener: %v %v", got, err)
	}
}

// TestPrivateCodexSocketSupportsNativeSymlinks verifies the socket remains owner-only.
func TestPrivateCodexSocketSupportsNativeSymlinks(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "rmt-socket-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "control.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.sock")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := privateCodexSocket(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0666); err != nil {
		t.Fatal(err)
	}
	if _, err := privateCodexSocket(link); err == nil {
		t.Fatal("accepted a publicly accessible socket")
	}
}

// TestNativeStartupContinuesAfterDisconnect never kills a service command on timeout.
func TestNativeStartupContinuesAfterDisconnect(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "native-service")
	marker := filepath.Join(dir, "done")
	script := "#!/bin/sh\n/bin/sleep 0.2\nprintf done > \"$RMT_TEST_MARKER\"\n"
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RMT_TEST_MARKER", marker)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := startNative(ctx, binary); err == nil {
		t.Fatal("expected timeout")
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(marker); err == nil && string(data) == "done" {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("native startup was killed after disconnect")
}

// TestOpenCodeDisabledRespectsNativePreference does not enable an explicitly disabled service.
func TestOpenCodeDisabledRespectsNativePreference(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "opencode")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nif [ \"$2\" = get ]; then printf true; else printf invoked > \"$RMT_TEST_MARKER\"; fi\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	marker := filepath.Join(dir, "called")
	t.Setenv("RMT_TEST_MARKER", marker)
	if _, err := openCodeService(context.Background()); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("expected disabled error: %v", err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("disabled service was started")
	}
}
