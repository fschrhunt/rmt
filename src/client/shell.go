package client

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/fschrhunt/rmt/src/shell"
)

// ConnectionError means the command may have reached the host before the connection failed.
type ConnectionError struct{ Err error }

// ErrSessionExpired means a previous shell no longer exists on the host.
var ErrSessionExpired = errors.New("shell session expired or unavailable")

func (e *ConnectionError) Error() string {
	return fmt.Sprintf("connection lost; command outcome may be unknown: %v", e.Err)
}
func (e *ConnectionError) Unwrap() error { return e.Err }

// Shell keeps a local connection to one persistent command shell on the host.
type Shell struct {
	host      string
	conn      *websocket.Conn
	client    *http.Client
	directory string
	session   string
	sequence  uint64
	running   bool
	available uint64
	write     sync.Mutex
}

// OpenShell connects to the selected host and returns its home directory.
func OpenShell(host string) (*Shell, error) { return openShell(host, "", 0) }

// openShell attaches to a new or previously created remote shell.
func openShell(host, session string, after uint64) (*Shell, error) {
	target, err := resolveTarget(host)
	if err != nil {
		return nil, err
	}
	endpoint, err := url.Parse(target.base)
	if err != nil {
		return nil, err
	}
	switch endpoint.Scheme {
	case "https":
		endpoint.Scheme = "wss"
	case "http":
		endpoint.Scheme = "ws"
	default:
		return nil, errors.New("invalid host connection")
	}
	endpoint.Path = "/shell"
	if session != "" {
		q := endpoint.Query()
		q.Set("session", session)
		q.Set("after", fmt.Sprint(after))
		endpoint.RawQuery = q.Encode()
	}
	wsClient := *target.client
	wsClient.Timeout = 0
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	conn, resp, err := websocket.Dial(ctx, endpoint.String(), &websocket.DialOptions{HTTPClient: &wsClient})
	if err != nil {
		target.client.CloseIdleConnections()
		if session != "" && resp != nil && resp.StatusCode == http.StatusGone {
			return nil, ErrSessionExpired
		}
		if resp != nil && resp.StatusCode == http.StatusNotFound {
			return nil, errors.New("host does not support the new prompt; update its rmt service")
		}
		return nil, fmt.Errorf("cannot connect to %s: %w", host, err)
	}
	conn.SetReadLimit(1 << 20)
	var event shell.Event
	if err := wsjson.Read(ctx, conn, &event); err != nil {
		conn.CloseNow()
		target.client.CloseIdleConnections()
		return nil, fmt.Errorf("host shell did not start: %w", err)
	}
	if event.Type == "ready" && event.Version != shell.Version {
		conn.CloseNow()
		target.client.CloseIdleConnections()
		return nil, fmt.Errorf("incompatible rmt protocol (host %d, client %d); update both machines", event.Version, shell.Version)
	}
	if event.Type != "ready" || event.Directory == "" || event.Session == "" || (session != "" && (!event.Resumed || event.Session != session)) {
		conn.CloseNow()
		target.client.CloseIdleConnections()
		if event.Error != "" {
			return nil, errors.New(event.Error)
		}
		return nil, errors.New("invalid host shell response")
	}
	return &Shell{host: host, conn: conn, client: target.client, directory: event.Directory, session: event.Session, sequence: after, running: event.Running, available: event.Sequence}, nil
}

// ResumeShell reconnects a prompt to its existing shell and preserves its state.
func ResumeShell(host string, old *Shell) (*Shell, error) {
	return openShell(host, old.session, old.sequence)
}

// recover reconnects without replaying a command and swaps the active transport.
func (s *Shell) recover(host string, output io.Writer) error {
	s.write.Lock()
	_ = s.conn.CloseNow()
	s.client.CloseIdleConnections()
	s.write.Unlock()
	fmt.Fprintln(output, "[rmt reconnecting; command was not replayed]")
	var err error
	for i := 0; i < 3; i++ {
		if i > 0 {
			time.Sleep(time.Second)
		}
		var next *Shell
		next, err = openShell(host, s.session, s.sequence)
		if err != nil {
			continue
		}
		s.write.Lock()
		s.conn = next.conn
		s.client = next.client
		s.directory = next.directory
		s.running = next.running
		s.available = next.available
		s.write.Unlock()
		fmt.Fprintln(output, "[rmt reconnected]")
		return nil
	}
	return err
}

// Directory returns the host's current directory for native program launches.
func (s *Shell) Directory() string { return s.directory }

// Interrupt asks the host to stop its current shell command or launch preparation.
func (s *Shell) Interrupt() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	s.write.Lock()
	defer s.write.Unlock()
	_ = wsjson.Write(ctx, s.conn, shell.Request{Type: "interrupt"})
}

// Run streams one command's output and forwards Ctrl-C without replaying commands.
func (s *Shell) Run(command string, output io.Writer) (int, error) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.write.Lock()
	err := wsjson.Write(ctx, s.conn, shell.Request{Type: "run", Command: command})
	s.write.Unlock()
	if err != nil {
		if e := s.recover(s.host, output); e != nil {
			return 0, &ConnectionError{Err: e}
		}
		if !s.running && s.available <= s.sequence {
			return 0, &ConnectionError{Err: errors.New("command may not have reached the host")}
		}
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt)
	done := make(chan struct{})
	stopped := make(chan struct{})
	defer func() { signal.Stop(signals); close(done); cancel(); <-stopped }()
	go func() {
		defer close(stopped)
		for {
			select {
			case <-signals:
				s.write.Lock()
				_ = wsjson.Write(ctx, s.conn, shell.Request{Type: "interrupt"})
				s.write.Unlock()
			case <-done:
				return
			case <-ctx.Done():
				return
			}
		}
	}()
	for {
		var event shell.Event
		if err := wsjson.Read(ctx, s.conn, &event); err != nil {
			if e := s.recover(s.host, output); e != nil {
				return 0, &ConnectionError{Err: e}
			}
			if !s.running && s.available <= s.sequence {
				return 0, &ConnectionError{Err: errors.New("command outcome unavailable")}
			}
			continue
		}
		if event.Sequence > 0 && event.Sequence <= s.sequence {
			continue
		}
		if event.Sequence > 0 {
			s.sequence = event.Sequence
		}
		switch event.Type {
		case "gap":
			fmt.Fprintln(output, "[rmt: some output was lost while disconnected]")
		case "output":
			if _, err := output.Write(event.Data); err != nil {
				return 0, err
			}
		case "done":
			s.directory = event.Directory
			return event.Status, nil
		case "error":
			return 0, fmt.Errorf("host shell: %s", event.Error)
		default:
			return 0, errors.New("invalid host shell response")
		}
	}
}

// Close disconnects this prompt without stopping native program services.
func (s *Shell) Close() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	s.write.Lock()
	_ = wsjson.Write(ctx, s.conn, shell.Request{Type: "close"})
	s.write.Unlock()
	cancel()
	_ = s.conn.Close(websocket.StatusNormalClosure, "")
	s.client.CloseIdleConnections()
}

// Abort closes a lost connection without waiting for a WebSocket close handshake.
func (s *Shell) Abort() { _ = s.conn.CloseNow(); s.client.CloseIdleConnections() }
