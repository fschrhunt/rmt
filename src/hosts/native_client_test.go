package hosts

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/fschrhunt/rmt/src/client"
)

// These are protocol fixtures, not native applications: Launch still performs
// real preparation, service discovery and both authenticated proxy hops.
func TestClientNativeBridges(t *testing.T) {
	for _, tc := range []struct {
		program string
		failure bool
	}{{"opencode", false}, {"codex", false}, {"opencode", true}, {"codex", true}} {
		name := tc.program + "/exit"
		if tc.failure {
			name = tc.program + "/host-loss"
		}
		t.Run(name, func(t *testing.T) {
			program := tc.program
			h := newLoopbackHost(t)
			canceled := make(chan struct{}, 1)
			fail := make(chan struct{}, 1)
			backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if program == "opencode" {
					user, password, ok := r.BasicAuth()
					if !ok || user != "opencode" || password != "fixture-password" {
						http.Error(w, "wrong backend credentials", http.StatusUnauthorized)
						return
					}
					if r.URL.Path == "/api/info" {
						w.WriteHeader(http.StatusOK)
						return
					}
					if tc.failure && r.URL.Path == "/fail" {
						fail <- struct{}{}
						<-r.Context().Done()
						return
					}
					if r.URL.Path != "/events" || r.URL.Query().Get("location[directory]") != h.home {
						http.Error(w, "wrong backend location", http.StatusBadRequest)
						return
					}
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, "data: from-host\n\n")
					w.(http.Flusher).Flush()
					// Keep the stream active until native disconnect or host loss
					// cancels the host-side request.
					<-r.Context().Done()
					canceled <- struct{}{}
					return
				}
				if r.Header.Get("Authorization") != "" {
					http.Error(w, "device credential reached native daemon", http.StatusUnauthorized)
					return
				}
				conn, err := websocket.Accept(w, r, nil)
				if err != nil {
					return
				}
				defer conn.CloseNow()
				ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
				defer cancel()
				kind, data, err := conn.Read(ctx)
				if err == nil {
					_ = conn.Write(ctx, kind, data)
				}
				if tc.failure {
					// The fixture acknowledges the echo before the host is dropped.
					_, data, err = conn.Read(ctx)
					if err != nil || string(data) != "received" {
						return
					}
					fail <- struct{}{}
				}
				_, _, err = conn.Read(ctx)
				if err != nil && ctx.Err() == nil {
					canceled <- struct{}{}
				}
			})
			bin := t.TempDir()
			var service string
			if program == "opencode" {
				server := httptest.NewServer(backend)
				t.Cleanup(server.Close)
				t.Setenv("RMT_TEST_BACKEND", server.URL)
				service = `case "$2:$3" in
 get:disabled) printf false ;;
 get:password) printf fixture-password ;;
 status:) printf '%s' "$RMT_TEST_BACKEND" ;;
 *) exit 91 ;;
esac`
			} else {
				// Keep the Unix socket below the macOS/Linux path length limit.
				dir, err := os.MkdirTemp("/tmp", "rmt-native-")
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.RemoveAll(dir) })
				t.Setenv("CODEX_HOME", dir)
				socketDir := filepath.Join(dir, "app-server-control")
				if err := os.Mkdir(socketDir, 0700); err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(socketDir, "app-server-control.sock")
				listener, err := net.Listen("unix", path)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(path, 0600); err != nil {
					listener.Close()
					t.Fatal(err)
				}
				server := &http.Server{Handler: backend, ReadHeaderTimeout: time.Second}
				go func() { _ = server.Serve(listener) }()
				t.Cleanup(func() { _ = server.Close() })
				service = `[ "$2:$3" = daemon:start ] || exit 91`
			}
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			script := fmt.Sprintf(`#!/bin/sh
case "$1" in
 --help) printf '%%s\n' '--server --remote --remote-auth-token-env'; exit ;;
 --version) printf fixture; exit ;;
 service|app-server) %s; exit ;;
esac
RMT_NATIVE_FIXTURE=%s exec %s -test.run='^TestNativeBridgeHelper$' -- "$@"
`, service, program, quoteShell(executable))
			if err := os.WriteFile(filepath.Join(bin, program), []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", bin)
			t.Setenv("RMT_TEST_DIRECTORY", h.home)
			t.Setenv("RMT_TEST_HOST_FAILURE", fmt.Sprint(tc.failure))
			report := filepath.Join(bin, "bridge-address")
			t.Setenv("RMT_TEST_BRIDGE_ADDRESS", report)
			remote, err := client.OpenShell("loopback")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(remote.Close)
			line := `opencode --title 'two words'`
			if program == "codex" {
				line = `codex resume --name 'two words'`
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			launched := make(chan error, 1)
			go func() { launched <- client.Launch(ctx, "loopback", program, line, remote, nil) }()
			if tc.failure {
				select {
				case <-fail:
					h.stop()
				case err := <-launched:
					t.Fatalf("native fixture exited before host loss: %v", err)
				case <-ctx.Done():
					t.Fatal("native fixture did not acknowledge bridge traffic")
				}
			}
			select {
			case err = <-launched:
			case <-ctx.Done():
				t.Fatal("native fixture did not exit")
			}
			// A nonzero native exit must reach the caller without being hidden by
			// successful preparation or proxy startup.
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 17 {
				t.Fatalf("native exit status was lost: %v", err)
			}
			select {
			case <-canceled:
			case <-ctx.Done():
				t.Fatal("disconnect did not end the upstream stream")
			}
			address, err := os.ReadFile(report)
			if err != nil {
				t.Fatal(err)
			}
			if conn, err := net.DialTimeout("tcp", string(address), time.Second); err == nil {
				conn.Close()
				t.Fatal("local bridge listener survived native exit")
			}
			if !tc.failure {
				result := runClientCommand(t, remote, "printf prompt-alive", io.Discard)
				if result.err != nil || result.status != 0 {
					t.Fatalf("native exit broke the prompt: %+v", result)
				}
			}
		})
	}
}

// TestNativeBridgeHelper substitutes only the native executable. It exercises
// HTTP streaming or WebSocket traffic using the arguments and credentials rmt set.
func TestNativeBridgeHelper(t *testing.T) {
	program := os.Getenv("RMT_NATIVE_FIXTURE")
	if program == "" {
		return
	}
	var args []string
	for i, arg := range os.Args {
		if arg == "--" {
			args = os.Args[i+1:]
			break
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	failure := os.Getenv("RMT_TEST_HOST_FAILURE") == "true"
	var endpoint string
	if program == "opencode" {
		if len(args) != 5 || args[0] != "--server" || !reflect.DeepEqual(args[3:], []string{"--title", "two words"}) {
			t.Fatalf("unexpected native arguments: %q", args)
		}
		endpoint = args[1]
		query := url.Values{"location[directory]": {args[2]}}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"/events?"+query.Encode(), nil)
		if err != nil {
			t.Fatal(err)
		}
		req.SetBasicAuth("opencode", os.Getenv("OPENCODE_PASSWORD"))
		httpClient := &http.Client{Timeout: 5 * time.Second}
		response, err := httpClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		reader := bufio.NewReader(response.Body)
		line, err := reader.ReadString('\n')
		if err != nil || response.StatusCode != http.StatusOK || line != "data: from-host\n" {
			t.Fatalf("native stream: status %d, line %q, error %v", response.StatusCode, line, err)
		}
		if failure {
			ack, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+"/fail", nil)
			if err != nil {
				t.Fatal(err)
			}
			ack.SetBasicAuth("opencode", os.Getenv("OPENCODE_PASSWORD"))
			r, err := httpClient.Do(ack)
			if err == nil {
				r.Body.Close()
				if r.StatusCode != http.StatusBadGateway {
					t.Fatalf("host loss returned status %d", r.StatusCode)
				}
			}
			_, err = io.ReadAll(reader)
			if err == nil || ctx.Err() != nil {
				t.Fatalf("host loss did not promptly break the HTTP stream: %v", err)
			}
		}
		response.Body.Close()
		httpClient.CloseIdleConnections()
	} else {
		if len(args) != 9 || args[0] != "resume" || args[1] != "--remote" || !reflect.DeepEqual(args[3:], []string{"--remote-auth-token-env", "RMT_REMOTE_TOKEN", "-C", os.Getenv("RMT_TEST_DIRECTORY"), "--name", "two words"}) {
			t.Fatalf("unexpected native arguments: %q", args)
		}
		endpoint = args[2]
		_, response, err := websocket.Dial(ctx, endpoint, nil)
		if err == nil || response == nil || response.StatusCode != http.StatusUnauthorized {
			t.Fatal("Codex bridge accepted a client without its attachment credential")
		}
		conn, _, err := websocket.Dial(ctx, endpoint, &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer " + os.Getenv("RMT_REMOTE_TOKEN")}}})
		if err != nil {
			t.Fatal(err)
		}
		defer conn.CloseNow()
		if err := conn.Write(ctx, websocket.MessageText, []byte("native-probe")); err != nil {
			t.Fatal(err)
		}
		kind, data, err := conn.Read(ctx)
		if err != nil || kind != websocket.MessageText || string(data) != "native-probe" {
			t.Fatalf("native WebSocket round trip: type %v, error %v", kind, err)
		}
		if failure {
			if err := conn.Write(ctx, websocket.MessageText, []byte("received")); err != nil {
				t.Fatal(err)
			}
			_, _, err = conn.Read(ctx)
			if err == nil || ctx.Err() != nil {
				t.Fatalf("host loss did not promptly break the WebSocket: %v", err)
			}
		}
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(os.Getenv("RMT_TEST_BRIDGE_ADDRESS"), []byte(u.Host), 0600); err != nil {
		t.Fatal(err)
	}
	os.Exit(17)
}
