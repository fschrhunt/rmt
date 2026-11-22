package hosts

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/fschrhunt/rmt/src/config"
	"github.com/fschrhunt/rmt/src/extensions"
	"github.com/fschrhunt/rmt/src/network"
	"github.com/fschrhunt/rmt/src/shell"
)

// shellSessions owns short-lived resumable shells for authenticated devices.
type shellSessions struct {
	home, shellPath, user, hostname string
	authorized                      func(string) bool
	enabled                         func(string) bool
	problems                        map[string]string
	extensions                      *extensions.Registry
	mu                              sync.Mutex
	sessions                        map[string]*shellSession
	grace                           time.Duration
}

var errSessionUnavailable = errors.New("shell session expired or unavailable")

// shellSession keeps a command and its bounded output alive during a network interruption.
type shellSession struct {
	token, owner, directory   string
	sh                        *commandShell
	program                   *programBackend
	starting                  bool
	preparedDirectory         string
	preparedName              string
	mu                        sync.Mutex
	changed                   chan struct{}
	events                    []shell.Event
	size                      int
	sequence                  uint64
	delivered                 uint64
	ctx                       context.Context
	cancel                    context.CancelFunc
	proxying                  int
	running, attached, closed bool
	orphaned                  bool
	timer                     *time.Timer
	generation                uint64
}

// shellHandler creates a session manager; callers close it when the host stops.
func shellHandler(home, shellPath string) *shellSessions {
	return &shellSessions{home: home, shellPath: shellPath, enabled: (config.Config{}).Enabled, sessions: make(map[string]*shellSession), grace: 15 * time.Second}
}

// identity sets the user and short hostname reported to connecting prompts.
func (m *shellSessions) identity(user, hostname string) {
	m.user, m.hostname = user, hostname
}

// add stores an event and wakes a connected client; old output is bounded.
func (s *shellSession) add(event shell.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.addLocked(event)
}

// addLocked keeps bounded replay history, waiting rather than evicting live unsent output.
func (s *shellSession) addLocked(event shell.Event) {
	for !s.closed && len(s.events) > 0 && (s.size+len(event.Data) > 1<<20 || len(s.events) >= 2048) {
		if s.attached && s.events[0].Sequence > s.delivered {
			changed := s.changed
			s.mu.Unlock()
			<-changed
			s.mu.Lock()
			continue
		}
		s.size -= len(s.events[0].Data)
		s.events[0] = shell.Event{}
		s.events = s.events[1:]
	}
	if s.closed {
		return
	}
	s.sequence++
	event.Sequence = s.sequence
	s.events = append(s.events, event)
	s.size += len(event.Data)
	s.notifyLocked()
}

// notifyLocked wakes the writer and any producer waiting on connection progress.
func (s *shellSession) notifyLocked() {
	close(s.changed)
	s.changed = make(chan struct{})
}

// stop removes a shell and lets commands already running in it finish.
func (m *shellSessions) stop(s *shellSession) {
	m.mu.Lock()
	if m.sessions[s.token] != s {
		m.mu.Unlock()
		return
	}
	delete(m.sessions, s.token)
	m.mu.Unlock()
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.cancel()
	backend := s.program
	s.program = nil
	if s.timer != nil {
		s.timer.Stop()
	}
	close(s.changed)
	s.mu.Unlock()
	if backend != nil {
		if closer, ok := backend.transport.(interface{ CloseIdleConnections() }); ok {
			closer.CloseIdleConnections()
		}
	}
	s.sh.Close()
}

// stopDetached expires only the disconnection that scheduled it.
func (m *shellSessions) stopDetached(s *shellSession, generation uint64) {
	m.mu.Lock()
	s.mu.Lock()
	if m.sessions[s.token] != s || s.closed || s.attached || s.generation != generation {
		s.mu.Unlock()
		m.mu.Unlock()
		return
	}
	if s.running {
		s.orphaned = true
		s.timer = nil
		s.mu.Unlock()
		m.mu.Unlock()
		return
	}
	if s.proxying > 0 {
		s.timer = time.AfterFunc(m.grace, func() { m.stopDetached(s, generation) })
		s.mu.Unlock()
		m.mu.Unlock()
		return
	}
	delete(m.sessions, s.token)
	s.closed = true
	s.cancel()
	backend := s.program
	s.program = nil
	if s.timer != nil {
		s.timer.Stop()
	}
	close(s.changed)
	s.mu.Unlock()
	m.mu.Unlock()
	if backend != nil {
		if closer, ok := backend.transport.(interface{ CloseIdleConnections() }); ok {
			closer.CloseIdleConnections()
		}
	}
	s.sh.Close()
}

// Close releases every hosted shell when the host service shuts down.
func (m *shellSessions) Close() {
	m.mu.Lock()
	all := make([]*shellSession, 0, len(m.sessions))
	for _, s := range m.sessions {
		all = append(all, s)
	}
	m.mu.Unlock()
	for _, s := range all {
		m.stop(s)
	}
}

// acquire creates a shell or resumes one belonging to the same paired device.
func (m *shellSessions) acquire(token, owner string) (*shellSession, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if token != "" {
		s := m.sessions[token]
		if s == nil || s.owner != owner {
			return nil, false, errSessionUnavailable
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.closed || s.attached || s.orphaned {
			return nil, false, errors.New("shell session is already connected")
		}
		s.attached = true
		s.delivered = 0
		if s.timer != nil {
			s.timer.Stop()
			s.timer = nil
		}
		return s, true, nil
	}
	if len(m.sessions) >= 64 {
		return nil, false, errors.New("too many shell sessions")
	}
	var secret [32]byte
	if _, err := rand.Read(secret[:]); err != nil {
		return nil, false, err
	}
	sh, err := newCommandShell(m.home, m.shellPath)
	if err != nil {
		return nil, false, err
	}
	token = hex.EncodeToString(secret[:])
	ctx, cancel := context.WithCancel(context.Background())
	s := &shellSession{token: token, owner: owner, directory: m.home, sh: sh, changed: make(chan struct{}), ctx: ctx, cancel: cancel, attached: true}
	m.sessions[token] = s
	return s, false, nil
}

// detach retains a disconnected shell briefly, but stops it if its device is revoked.
func (m *shellSessions) detach(s *shellSession) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.attached = false
	s.notifyLocked()
	s.generation++
	generation := s.generation
	s.timer = time.AfterFunc(m.grace, func() { m.stopDetached(s, generation) })
	s.mu.Unlock()
	if m.authorized != nil {
		go func() {
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()
			for range ticker.C {
				s.mu.Lock()
				done := s.closed || s.attached
				s.mu.Unlock()
				if done {
					return
				}
				if !m.authorized(s.owner) {
					m.stopDetached(s, generation)
					return
				}
			}
		}()
	}
}

// ServeHTTP attaches an authenticated device to its persistent command shell.
func (m *shellSessions) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	owner := network.DeviceKey(r.Context())
	token := r.URL.Query().Get("session")
	after, err := strconv.ParseUint(r.URL.Query().Get("after"), 10, 64)
	if err != nil && r.URL.Query().Get("after") != "" {
		http.Error(w, "invalid sequence", 400)
		return
	}
	s, resumed, err := m.acquire(token, owner)
	if err != nil {
		status := http.StatusConflict
		if errors.Is(err, errSessionUnavailable) {
			status = http.StatusGone
		}
		http.Error(w, err.Error(), status)
		return
	}
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		m.detach(s)
		return
	}
	defer c.CloseNow()
	c.SetReadLimit(128 << 10)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	defer context.AfterFunc(s.ctx, cancel)()
	s.mu.Lock()
	ready := shell.Event{Type: "ready", Version: shell.Version, Session: s.token, Directory: s.directory, Home: m.home, User: m.user, Host: m.hostname, Resumed: resumed, Running: s.running, Sequence: s.sequence}
	s.mu.Unlock()
	if wsjson.Write(ctx, c, ready) != nil {
		m.detach(s)
		return
	}
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		cursor := after
		for {
			s.mu.Lock()
			if s.closed {
				s.mu.Unlock()
				return
			}
			if len(s.events) > 0 && cursor+1 < s.events[0].Sequence {
				cursor = s.events[0].Sequence - 1
				s.mu.Unlock()
				if wsjson.Write(ctx, c, shell.Event{Type: "gap", Sequence: cursor}) != nil {
					cancel()
					return
				}
				continue
			}
			var next shell.Event
			if len(s.events) > 0 && cursor < s.sequence {
				next = s.events[cursor+1-s.events[0].Sequence]
			}
			changed := s.changed
			s.mu.Unlock()
			if next.Sequence != 0 {
				writeCtx, stop := context.WithTimeout(ctx, 15*time.Second)
				err := wsjson.Write(writeCtx, c, next)
				stop()
				if err != nil {
					cancel()
					return
				}
				cursor = next.Sequence
				s.mu.Lock()
				if !s.closed {
					s.delivered = cursor
					s.notifyLocked()
				}
				s.mu.Unlock()
				continue
			}
			select {
			case <-changed:
			case <-ctx.Done():
				return
			}
		}
	}()
	graceful := false
	defer func() {
		cancel()
		<-writerDone
		if graceful {
			m.stop(s)
		} else {
			m.detach(s)
		}
	}()
	for {
		var req shell.Request
		if err := wsjson.Read(ctx, c, &req); err != nil {
			switch websocket.CloseStatus(err) {
			case websocket.StatusNormalClosure, websocket.StatusGoingAway:
				graceful = true
			}
			return
		}
		switch req.Type {
		case "run":
			s.mu.Lock()
			if s.running || s.starting || s.closed {
				s.mu.Unlock()
				return
			}
			s.running = true
			s.mu.Unlock()
			go func(req shell.Request) {
				event, dir := m.run(s, req)
				s.mu.Lock()
				if dir != "" {
					s.directory = dir
				}
				s.addLocked(event)
				s.running = false
				orphaned := s.orphaned
				s.mu.Unlock()
				if orphaned {
					m.stop(s)
				}
			}(req)
		case "interrupt":
			s.mu.Lock()
			if s.running || s.starting {
				_ = s.sh.Interrupt()
			}
			s.mu.Unlock()
		case "close":
			graceful = true
			return
		default:
			return
		}
	}
}

// run resolves a requested alias locally on the host and runs only ordinary commands in the shell.
func (m *shellSessions) run(s *shellSession, req shell.Request) (shell.Event, string) {
	if len(req.Alias) > 0 && len(req.Alias) <= 256 && !strings.HasPrefix(req.Alias, "-") {
		var aliases bytes.Buffer
		lookup := "( __rmt_status=$?; \\alias " + quoteShell(req.Alias) + " 2>/dev/null; exit \"$__rmt_status\" )"
		_, _, err := s.sh.Run(lookup, func(data []byte) error {
			if remaining := 64<<10 - aliases.Len(); remaining > 0 {
				_, _ = aliases.Write(data[:min(len(data), remaining)])
			}
			return nil
		})
		if err != nil {
			return shell.Event{Type: "error", Error: err.Error()}, ""
		}
		var programs []string
		if m.extensions != nil {
			programs = m.extensions.Programs()
		}
		if cmd := shell.AliasCommand(aliases.String(), req.Alias, programs...); cmd.Program != "" || cmd.Unsupported != "" {
			return shell.Event{Type: "program", Program: cmd.Program, Error: cmd.Unsupported}, ""
		}
	}
	dir, status, err := s.sh.Run(req.Command, func(data []byte) error {
		s.add(shell.Event{Type: "output", Data: append([]byte(nil), data...)})
		return nil
	})
	if err != nil {
		return shell.Event{Type: "error", Error: err.Error()}, ""
	}
	return shell.Event{Type: "done", Directory: dir, Status: status}, dir
}
