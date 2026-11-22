package hosts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fschrhunt/rmt/src/client"
	"github.com/fschrhunt/rmt/src/network"
)

// loopbackHost uses the production pairing, authentication, shell and program handlers.
// It never runs hosts.Run, which probes installed programs and opens a service listener.
type loopbackHost struct {
	home     string
	manager  *shellSessions
	server   *httptest.Server
	hijacked chan net.Conn
	detached chan struct{}
	mu       sync.Mutex
	conns    []net.Conn
}

func newLoopbackHost(t testing.TB) *loopbackHost {
	t.Helper()
	local := t.TempDir()
	t.Setenv("HOME", local)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(local, ".config"))
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := &loopbackHost{home: home, manager: shellHandler(home, "/bin/sh"), hijacked: make(chan net.Conn, 16), detached: make(chan struct{}, 16)}
	identityDir := filepath.Join(t.TempDir(), "identity")
	paired, err := network.NewHost(identityDir)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/identity", paired.Pairing)
	mux.HandleFunc("/pair", paired.Pairing)
	mux.Handle("/shell", paired.Auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.manager.ServeHTTP(w, r)
		select {
		case h.detached <- struct{}{}:
		default:
		}
	})))
	mux.Handle("/program/", paired.Auth(http.HandlerFunc(h.manager.serveProgram)))
	mux.Handle("/info", paired.Auth(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(network.Info{Home: home, Programs: []string{"opencode", "codex"}})
	})))
	h.server = httptest.NewUnstartedServer(mux)
	h.server.TLS = paired.TLS()
	h.server.Config.ConnState = func(conn net.Conn, state http.ConnState) {
		if state == http.StateHijacked {
			h.mu.Lock()
			h.conns = append(h.conns, conn)
			h.mu.Unlock()
			select {
			case h.hijacked <- conn:
			default:
			}
		}
	}
	h.server.StartTLS()
	t.Cleanup(func() { h.stop(); h.manager.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, fingerprint, err := network.Inspect(ctx, h.server.URL)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(identityDir, "pairing.json"))
	if err != nil {
		t.Fatal(err)
	}
	var code struct{ Code, Fingerprint string }
	if err := json.Unmarshal(data, &code); err != nil {
		t.Fatal(err)
	}
	if fingerprint != code.Fingerprint {
		t.Fatal("presented identity does not match the owner's pairing file")
	}
	profile, err := network.Pair(ctx, h.server.URL, fingerprint, code.Code, "loopback-test")
	if err != nil {
		t.Fatal(err)
	}
	if err := network.SaveProfile(filepath.Join(local, ".rmt"), "loopback", profile); err != nil {
		t.Fatal(err)
	}
	return h
}

func TestClientRejectsChangedHostIdentity(t *testing.T) {
	newLoopbackHost(t)
	var reached atomic.Bool
	replacement := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		reached.Store(true)
	}))
	defer replacement.Close()
	// Change only the configured address; the saved pairing identity must still
	// be checked before a client sends its credential to the replacement host.
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	settings := fmt.Sprintf("[hosts]\nloopback = %q\n", replacement.URL)
	if err := os.WriteFile(filepath.Join(home, ".rmt", "settings.toml"), []byte(settings), 0600); err != nil {
		t.Fatal(err)
	}
	remote, err := client.OpenShell("loopback")
	if remote != nil {
		remote.Abort()
	}
	if err == nil || !strings.Contains(err.Error(), "host identity changed") || reached.Load() {
		t.Fatalf("client did not reject the changed identity before HTTP: %v, handler reached %v", err, reached.Load())
	}
}

func (h *loopbackHost) stop() {
	// Force active HTTP streams down too: httptest.Server.Close alone waits
	// for them to finish and would simulate graceful shutdown, not host loss.
	_ = h.server.Config.Close()
	h.mu.Lock()
	// HTTP shutdown does not close hijacked WebSocket connections.
	for _, conn := range h.conns {
		_ = conn.Close()
	}
	h.mu.Unlock()
	h.server.Close()
}

type commandResult struct {
	status int
	err    error
}

// TestClientRoutesAliasOnSubmission checks that native aliases are not executed by the shell.
func TestClientRoutesAliasOnSubmission(t *testing.T) {
	newLoopbackHost(t)
	remote, err := client.OpenShell("loopback")
	if err != nil {
		t.Fatal(err)
	}
	defer remote.Close()
	if status, err := remote.Run("alias oc='opencode --help'; alias say='printf hello'", io.Discard); status != 0 || err != nil {
		t.Fatalf("define aliases: %d, %v", status, err)
	}
	var output bytes.Buffer
	_, err = remote.RunPrompt("oc", "oc", &output)
	var route *client.ProgramRoute
	if !errors.As(err, &route) || route.Program != "opencode" || output.Len() != 0 {
		t.Fatalf("route = %v, output = %q", err, &output)
	}
	if status, err := remote.RunPrompt("say", "say", &output); status != 0 || err != nil || output.String() != "hello" {
		t.Fatalf("ordinary alias: %d, %v, %q", status, err, &output)
	}
}

// TestClientStreamsBeyondReplayLimit verifies that the live stream is not truncated by the replay limit.
func TestClientStreamsBeyondReplayLimit(t *testing.T) {
	newLoopbackHost(t)
	remote, err := client.OpenShell("loopback")
	if err != nil {
		t.Fatal(err)
	}
	defer remote.Close()
	var output bytes.Buffer
	result := runClientCommand(t, remote, "/bin/dd if=/dev/zero bs=65536 count=64 2>/dev/null", &output)
	if result.status != 0 || result.err != nil || output.Len() != 4<<20 || bytes.Count(output.Bytes(), []byte{0}) != 4<<20 {
		t.Fatalf("streamed %d bytes, status %d, error %v", output.Len(), result.status, result.err)
	}
}

// TestClientCloseReleasesRunningSession checks that a deliberate close leaves nothing to resume.
func TestClientCloseReleasesRunningSession(t *testing.T) {
	h := newLoopbackHost(t)
	remote, err := client.OpenShell("loopback")
	if err != nil {
		t.Fatal(err)
	}
	defer remote.Close()
	output := &startedOutput{onStarted: func() error {
		remote.Close()
		return nil
	}}
	runClientCommand(t, remote, "printf started; /bin/sleep 1; printf done", output)
	select {
	case <-h.detached:
	case <-time.After(3 * time.Second):
		t.Fatal("host did not release the connection")
	}
	if recovered, err := client.ResumeShell("loopback", remote); !errors.Is(err, client.ErrSessionExpired) {
		if recovered != nil {
			recovered.Close()
		}
		t.Fatalf("closed session could still be resumed: %v", err)
	}
}

func runClientCommand(t *testing.T, remote *client.Shell, command string, output io.Writer) commandResult {
	t.Helper()
	done := make(chan commandResult, 1)
	go func() {
		status, err := remote.Run(command, output)
		done <- commandResult{status, err}
	}()
	select {
	case result := <-done:
		return result
	case <-time.After(10 * time.Second):
		t.Fatal("client command did not finish")
		return commandResult{}
	}
}

type startedOutput struct {
	bytes.Buffer
	once      sync.Once
	onStarted func() error
	err       error
}

func (w *startedOutput) Write(data []byte) (int, error) {
	n, _ := w.Buffer.Write(data)
	if strings.Contains(w.String(), "started") {
		w.once.Do(func() { w.err = w.onStarted() })
	}
	return n, w.err
}

// The drop happens only after the actual client receives output. A file gate keeps
// the command in flight without relying on a sleep to guess when to disconnect.
func TestClientCommandDisconnect(t *testing.T) {
	for _, failure := range []bool{false, true} {
		name := "reconnect"
		if failure {
			name = "host-unavailable"
		}
		t.Run(name, func(t *testing.T) {
			h := newLoopbackHost(t)
			remote, err := client.OpenShell("loopback")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(remote.Abort)
			var conn net.Conn
			select {
			case conn = <-h.hijacked:
			case <-time.After(3 * time.Second):
				t.Fatal("host did not record the shell connection")
			}
			release := filepath.Join(h.home, "release")
			t.Cleanup(func() { _ = os.WriteFile(release, nil, 0600) })
			output := &startedOutput{onStarted: func() error {
				if failure {
					h.stop()
				} else {
					_ = conn.Close()
				}
				select {
				case <-h.detached:
				case <-time.After(3 * time.Second):
					return errors.New("host did not detach the dropped connection")
				}
				return os.WriteFile(release, nil, 0600)
			}}
			// Bound the gate as well as releasing it in cleanup, so even a
			// failed test cannot leave an indefinitely running host command.
			command := `printf 'once\n' >> calls; export RMT_CLIENT_STATE=alive; printf started; i=0; while [ ! -e release ] && [ "$i" -lt 500 ]; do i=$((i + 1)); /bin/sleep 0.01; done; printf '%s' "$RMT_CLIENT_STATE"; (exit 23)`
			result := runClientCommand(t, remote, command, output)
			if failure {
				var lost *client.ConnectionError
				if !errors.As(result.err, &lost) {
					t.Fatalf("host failure must report an unknown command outcome: %v", result.err)
				}
			} else {
				if result.err != nil || result.status != 23 || strings.Count(output.String(), "started") != 1 || strings.Count(output.String(), "alive") != 1 {
					t.Fatalf("recovered command: status %d, error %v, output %q", result.status, result.err, output.String())
				}
				var state bytes.Buffer
				result = runClientCommand(t, remote, `printf '%s' "$RMT_CLIENT_STATE"`, &state)
				if result.err != nil || result.status != 0 || state.String() != "alive" {
					t.Fatalf("client did not retain its shell: %+v, %q", result, state.String())
				}
			}
			calls, err := os.ReadFile(filepath.Join(h.home, "calls"))
			if err != nil || string(calls) != "once\n" {
				t.Fatalf("command was lost or replayed: %q, %v", calls, err)
			}
		})
	}
}

func TestClientInterruptPreservesShell(t *testing.T) {
	newLoopbackHost(t)
	remote, err := client.OpenShell("loopback")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(remote.Close)
	output := &startedOutput{onStarted: func() error {
		remote.Interrupt()
		return nil
	}}
	// The child announces readiness after installing its trap. This avoids
	// guessing when it is safe to interrupt and pins the resulting exit status.
	command := `export RMT_INTERRUPT_STATE=alive; /bin/sh -c 'trap "exit 42" TERM; printf started; i=0; while [ "$i" -lt 5 ]; do i=$((i + 1)); /bin/sleep 1; done; exit 99'`
	result := runClientCommand(t, remote, command, output)
	if result.err != nil || result.status != 42 || !strings.Contains(output.String(), "started") {
		t.Fatalf("interrupted command: %+v, output %q", result, output.String())
	}
	var state bytes.Buffer
	result = runClientCommand(t, remote, `printf '%s' "$RMT_INTERRUPT_STATE"`, &state)
	if result.err != nil || result.status != 0 || state.String() != "alive" {
		t.Fatalf("interrupt broke the shell: %+v, output %q", result, state.String())
	}
}

func TestClientCloseExpiresSession(t *testing.T) {
	newLoopbackHost(t)
	remote, err := client.OpenShell("loopback")
	if err != nil {
		t.Fatal(err)
	}
	remote.Close()
	resumed, err := client.ResumeShell("loopback", remote)
	if resumed != nil {
		resumed.Abort()
	}
	if !errors.Is(err, client.ErrSessionExpired) {
		t.Fatalf("closed client session resumed: %v", err)
	}
}

// BenchmarkPairedShellStartup measures OpenShell through receipt of the ready
// event, including config/profile reads, a fresh TLS connection and shell spawn.
func BenchmarkPairedShellStartup(b *testing.B) {
	newLoopbackHost(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		remote, err := client.OpenShell("loopback")
		if err != nil {
			b.Fatal(err)
		}
		b.StopTimer()
		remote.Close()
		b.StartTimer()
	}
	b.StopTimer()
}

// BenchmarkPairedCommandRoundTrip measures command submission through the done
// event on an already connected shell, including delivery of a fixed output.
func BenchmarkPairedCommandRoundTrip(b *testing.B) {
	newLoopbackHost(b)
	remote, err := client.OpenShell("loopback")
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(remote.Close)
	// Exclude first-command shell initialization from the steady-state sample.
	if _, err := remote.Run("printf probe", io.Discard); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		status, err := remote.Run("printf probe", io.Discard)
		if err != nil || status != 0 {
			b.Fatalf("command: status %d, error %v", status, err)
		}
	}
	b.StopTimer()
}
