package hosts

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/fschrhunt/rmt/src/shell"
)

// TestShellReconnectPreservesStateAndRunningCommand checks a dropped connection without replay.
func TestShellReconnectPreservesStateAndRunningCommand(t *testing.T) {
	manager := shellHandler(t.TempDir(), "")
	defer manager.Close()
	server := httptest.NewServer(manager)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	endpoint := "ws" + strings.TrimPrefix(server.URL, "http")
	connect := func(address string) (*websocket.Conn, shell.Event) {
		t.Helper()
		var c *websocket.Conn
		var err error
		for i := 0; i < 30; i++ {
			c, _, err = websocket.Dial(ctx, address, nil)
			if err == nil {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		if err != nil {
			t.Fatal(err)
		}
		var ready shell.Event
		if err = wsjson.Read(ctx, c, &ready); err != nil {
			t.Fatal(err)
		}
		return c, ready
	}
	c, ready := connect(endpoint)
	token := ready.Session
	if err := wsjson.Write(ctx, c, shell.Request{Type: "run", Command: "export RMT_RECONNECT=alive"}); err != nil {
		t.Fatal(err)
	}
	var event shell.Event
	for {
		if err := wsjson.Read(ctx, c, &event); err != nil {
			t.Fatal(err)
		}
		if event.Type == "done" {
			break
		}
	}
	after := event.Sequence
	if err := wsjson.Write(ctx, c, shell.Request{Type: "run", Command: `sleep 0.3; printf "%s" "$RMT_RECONNECT"`}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(80 * time.Millisecond)
	c.CloseNow()
	query := url.Values{"session": {token}, "after": {strconv.FormatUint(after, 10)}}
	c, ready = connect(endpoint + "?" + query.Encode())
	defer c.CloseNow()
	if !ready.Resumed {
		t.Fatal("did not resume")
	}
	var output strings.Builder
	for {
		if err := wsjson.Read(ctx, c, &event); err != nil {
			t.Fatal(err)
		}
		if event.Type == "output" {
			output.Write(event.Data)
		}
		if event.Type == "done" {
			break
		}
	}
	if output.String() != "alive" {
		t.Fatalf("lost state or output: %q", output.String())
	}
}

// TestResumeDistinguishesBusyFromExpired lets clients preserve a live shell on reconnect.
func TestResumeDistinguishesBusyFromExpired(t *testing.T) {
	manager := shellHandler(t.TempDir(), "")
	defer manager.Close()
	server := httptest.NewServer(manager)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	endpoint := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.Dial(ctx, endpoint, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	var ready shell.Event
	if err := wsjson.Read(ctx, conn, &ready); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		token string
		want  int
	}{{ready.Session, http.StatusConflict}, {"missing", http.StatusGone}} {
		_, response, err := websocket.Dial(ctx, endpoint+"?session="+tc.token, nil)
		if err == nil || response == nil || response.StatusCode != tc.want {
			t.Fatalf("resume: response %v, error %v; want %d", response, err, tc.want)
		}
		response.Body.Close()
	}
}

// TestShellSessionIsBoundToDevice prevents a paired device from attaching another device's shell.
func TestShellSessionIsBoundToDevice(t *testing.T) {
	manager := shellHandler(t.TempDir(), "")
	defer manager.Close()
	session, _, err := manager.acquire("", "first-device")
	if err != nil {
		t.Fatal(err)
	}
	manager.detach(session)
	if _, _, err := manager.acquire(session.token, "second-device"); err == nil {
		t.Fatal("other device resumed shell")
	}
	if _, resumed, err := manager.acquire(session.token, "first-device"); err != nil || !resumed {
		t.Fatalf("owner could not resume: %v", err)
	}
}

// TestDisconnectedShellExpires closes an idle shell after the reconnect grace period.
func TestDisconnectedShellExpires(t *testing.T) {
	manager := shellHandler(t.TempDir(), "")
	manager.grace = 50 * time.Millisecond
	defer manager.Close()
	session, _, err := manager.acquire("", "")
	if err != nil {
		t.Fatal(err)
	}
	manager.detach(session)
	select {
	case <-session.sh.done:
	case <-time.After(3 * time.Second):
		t.Fatal("abandoned shell remained running")
	}
}

// TestWindowExitLetsRunningCommandFinish protects work already started on the host.
func TestWindowExitLetsRunningCommandFinish(t *testing.T) {
	dir := t.TempDir()
	manager := shellHandler(dir, "")
	defer manager.Close()
	server := httptest.NewServer(manager)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	var event shell.Event
	if err = wsjson.Read(ctx, c, &event); err != nil {
		t.Fatal(err)
	}
	if err = wsjson.Write(ctx, c, shell.Request{Type: "run", Command: "printf started; sleep 0.2; printf done > result"}); err != nil {
		t.Fatal(err)
	}
	for {
		if err = wsjson.Read(ctx, c, &event); err != nil {
			t.Fatal(err)
		}
		if event.Type == "output" {
			break
		}
	}
	if err = wsjson.Write(ctx, c, shell.Request{Type: "close"}); err != nil {
		t.Fatal(err)
	}
	_ = c.Close(websocket.StatusNormalClosure, "")
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(filepath.Join(dir, "result"))
		if err == nil && string(data) == "done" {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("running command was interrupted when the window exited")
}

// TestNetworkTimeoutLetsRunningCommandFinish keeps work alive after the reconnect window expires.
func TestNetworkTimeoutLetsRunningCommandFinish(t *testing.T) {
	dir := t.TempDir()
	manager := shellHandler(dir, "")
	manager.grace = 50 * time.Millisecond
	defer manager.Close()
	server := httptest.NewServer(manager)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	var event shell.Event
	if err = wsjson.Read(ctx, c, &event); err != nil {
		t.Fatal(err)
	}
	if err = wsjson.Write(ctx, c, shell.Request{Type: "run", Command: "printf started; sleep 0.2; printf done > result"}); err != nil {
		t.Fatal(err)
	}
	for {
		if err = wsjson.Read(ctx, c, &event); err != nil {
			t.Fatal(err)
		}
		if event.Type == "output" {
			break
		}
	}
	c.CloseNow()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(filepath.Join(dir, "result"))
		if err == nil && string(data) == "done" {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("running command was interrupted after the reconnect window expired")
}

// TestWindowExitDoesNotKillBackgroundJobs preserves work launched by the host shell.
func TestWindowExitDoesNotKillBackgroundJobs(t *testing.T) {
	dir := t.TempDir()
	manager := shellHandler(dir, "")
	defer manager.Close()
	server := httptest.NewServer(manager)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	var event shell.Event
	if err = wsjson.Read(ctx, c, &event); err != nil {
		t.Fatal(err)
	}
	if err = wsjson.Write(ctx, c, shell.Request{Type: "run", Command: "(sleep 0.2; printf done > result) &"}); err != nil {
		t.Fatal(err)
	}
	for {
		if err = wsjson.Read(ctx, c, &event); err != nil {
			t.Fatal(err)
		}
		if event.Type == "done" {
			break
		}
	}
	if err = wsjson.Write(ctx, c, shell.Request{Type: "close"}); err != nil {
		t.Fatal(err)
	}
	_ = c.Close(websocket.StatusNormalClosure, "")
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(filepath.Join(dir, "result"))
		if err == nil && string(data) == "done" {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("background job was interrupted when the window exited")
}
