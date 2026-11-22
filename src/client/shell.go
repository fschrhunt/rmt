package client

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
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

// ProgramRoute means the host did not execute the command: it resolved to a native or unsupported application.
type ProgramRoute struct {
	Program string
}

func (r *ProgramRoute) Error() string { return "host routed command to an application" }

func (e *ConnectionError) Error() string {
	return fmt.Sprintf("connection lost; command outcome may be unknown: %v", e.Err)
}
func (e *ConnectionError) Unwrap() error { return e.Err }

// Shell keeps a local connection to one persistent command shell on the host.
type Shell struct {
	host      string
	base      string
	home      string
	user      string
	hostname  string
	conn      *websocket.Conn
	client    *http.Client
	directory string
	session   string
	sequence  uint64
	running   bool
	available uint64
	write     sync.Mutex
	closing   bool
	terminal  bool
}

// OpenShell connects to the selected host and returns its home directory.
func OpenShell(host string) (*Shell, error) { return openShell(host, "", 0) }

// OpenTerminalShell opens the usual prompt session with a PTY for submitted commands.
func OpenTerminalShell(host, terminal string) (*Shell, error) {
	return dialShell(host, "", 0, terminal)
}

func openShell(host, session string, after uint64) (*Shell, error) {
	return dialShell(host, session, after, "")
}

// dialShell opens a fresh transport, optionally requesting a PTY for a new session.
func dialShell(host, session string, after uint64, terminal string) (*Shell, error) {
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
	q := endpoint.Query()
	if session != "" {
		q.Set("session", session)
		q.Set("after", fmt.Sprint(after))
	} else if terminal != "" {
		q.Set("terminal", terminal)
	}
	endpoint.RawQuery = q.Encode()
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
		return nil, dialError(host, target.base, err)
	}
	conn.SetReadLimit(1 << 20)
	var event shell.Event
	if err := wsjson.Read(ctx, conn, &event); err != nil {
		conn.CloseNow()
		target.client.CloseIdleConnections()
		return nil, fmt.Errorf("connected to %s, but its shell did not start: %w", host, err)
	}
	if event.Type == "ready" && event.Version != shell.Version {
		conn.CloseNow()
		target.client.CloseIdleConnections()
		return nil, fmt.Errorf("incompatible rmt protocol (host %d, client %d); update both machines and restart the host service", event.Version, shell.Version)
	}
	if event.Type != "ready" || event.Directory == "" || event.Session == "" || (session != "" && (!event.Resumed || event.Session != session)) {
		conn.CloseNow()
		target.client.CloseIdleConnections()
		if event.Error != "" {
			return nil, errors.New(event.Error)
		}
		return nil, errors.New("invalid host shell response")
	}
	return &Shell{host: host, base: target.base, home: event.Home, user: event.User, hostname: event.Host, conn: conn, client: target.client, directory: event.Directory, session: event.Session, sequence: after, running: event.Running, available: event.Sequence, terminal: event.Terminal}, nil
}

// ResumeShell reconnects a prompt to its existing shell and preserves its state.
func ResumeShell(host string, old *Shell) (*Shell, error) {
	return openShell(host, old.session, old.sequence)
}

// recover reconnects without replaying a command and swaps the active transport.
func (s *Shell) recover(host string, output io.Writer) error {
	s.write.Lock()
	if s.closing {
		s.write.Unlock()
		return net.ErrClosed
	}
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
		if s.closing {
			s.write.Unlock()
			next.Close()
			return net.ErrClosed
		}
		s.home = next.home
		s.user = next.user
		s.hostname = next.hostname
		s.conn = next.conn
		s.client = next.client
		s.base = next.base
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

// Home returns the host user's home directory for prompt abbreviation.
func (s *Shell) Home() string { return s.home }

// User returns the host user's name for the prompt.
func (s *Shell) User() string { return s.user }

// Host returns the host's short name for the prompt.
func (s *Shell) Host() string { return s.hostname }

// Interrupt asks the host to stop its current shell command or launch preparation.
func (s *Shell) Interrupt() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	s.write.Lock()
	defer s.write.Unlock()
	if !s.closing {
		_ = wsjson.Write(ctx, s.conn, shell.Request{Type: "interrupt"})
	}
}

// Run streams one command's output and forwards Ctrl-C without replaying commands.
func (s *Shell) Run(command string, output io.Writer) (int, error) {
	return s.RunPrompt(command, "", nil, output, os.Stderr)
}

// RunPrompt optionally resolves an alias using the client's registered programs.
func (s *Shell) RunPrompt(command, alias string, programs []string, output, diagnostics io.Writer) (int, error) {
	return s.runPrompt(command, alias, programs, output, diagnostics, nil)
}

// RunTerminal hands the local terminal to a remote command until it finishes.
func (s *Shell) RunTerminal(command, alias string, programs []string, output, diagnostics io.Writer, input *os.File) (int, error) {
	if !s.terminal {
		return 0, errors.New("host did not open a terminal")
	}
	return s.runPrompt(command, alias, programs, output, diagnostics, input)
}

// runPrompt streams one command, enabling keyboard forwarding only after the host accepts it.
func (s *Shell) runPrompt(command, alias string, programs []string, output, diagnostics io.Writer, input *os.File) (int, error) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := shell.Request{Type: "run", Command: command, Alias: alias, Programs: programs}
	if input != nil {
		req.Turn = s.sequence + 1
		req.Size = terminalSize(input)
	}
	var stopInput func()
	finishedTerminal := false
	defer func() {
		if stopInput != nil {
			stopInput()
			if !finishedTerminal {
				// The app cannot restore its screen after losing the connection.
				_, _ = io.WriteString(output, "\x1b[?1000l\x1b[?1002l\x1b[?1003l\x1b[?1006l\x1b[?2004l\x1b[?1l\x1b[?1049l\x1b[?25h\x1b[0m")
			}
		}
	}()
	s.write.Lock()
	if s.closing {
		s.write.Unlock()
		return 0, net.ErrClosed
	}
	submitCtx, stopSubmit := context.WithTimeout(ctx, 8*time.Second)
	err := wsjson.Write(submitCtx, s.conn, req)
	stopSubmit()
	s.write.Unlock()
	if err != nil {
		if input != nil {
			return 0, fmt.Errorf("terminal connection lost; command was not replayed: %w", err)
		}
		if e := s.recover(s.host, diagnostics); e != nil {
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
				s.Interrupt()
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
			if input != nil {
				return 0, fmt.Errorf("terminal connection lost; command was not replayed: %w", err)
			}
			if e := s.recover(s.host, diagnostics); e != nil {
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
		case "terminal":
			if input == nil || stopInput != nil {
				return 0, errors.New("unexpected terminal handoff")
			}
			stopInput, err = s.forwardTerminal(input, req.Turn)
			if err != nil {
				return 0, err
			}
		case "gap":
			fmt.Fprintln(diagnostics, "[rmt: some output was lost while disconnected]")
		case "output":
			if _, err := output.Write(event.Data); err != nil {
				return 0, err
			}
		case "done":
			finishedTerminal = event.Status < 128
			s.directory = event.Directory
			return event.Status, nil
		case "error":
			return 0, fmt.Errorf("host shell: %s", event.Error)
		case "program":
			return 0, &ProgramRoute{Program: event.Program}
		default:
			return 0, errors.New("invalid host shell response")
		}
	}
}

// Close disconnects this prompt without stopping native program services.
func (s *Shell) Close() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	s.write.Lock()
	if s.closing {
		s.write.Unlock()
		cancel()
		return
	}
	s.closing = true
	conn, httpClient := s.conn, s.client
	_ = wsjson.Write(ctx, conn, shell.Request{Type: "close"})
	s.write.Unlock()
	cancel()
	_ = conn.Close(websocket.StatusNormalClosure, "")
	httpClient.CloseIdleConnections()
}

// Abort closes a lost connection without waiting for a WebSocket close handshake.
func (s *Shell) Abort() { _ = s.conn.CloseNow(); s.client.CloseIdleConnections() }
