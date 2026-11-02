package hosts

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
)

// commandShell keeps shell builtins, environment and directory between commands.
type commandShell struct {
	cmd       *exec.Cmd
	input     io.WriteCloser
	output    *os.File
	marker    []byte
	pending   []byte
	status    int
	done      chan struct{}
	mu        sync.Mutex
	closeOnce sync.Once
}

// newCommandShell starts a host shell without a terminal or interactive stdin.
func newCommandShell(home, shellPath string) (*commandShell, error) {
	if shellPath == "" {
		shellPath = "/bin/sh"
	}
	cmd := exec.Command(shellPath)
	cmd.Dir = home
	cmd.Env = append(os.Environ(), "HOME="+home, "PWD="+home, "TERM=dumb", "PAGER=cat", "GIT_PAGER=cat")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	input, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	output, writer, err := os.Pipe()
	if err != nil {
		input.Close()
		return nil, err
	}
	cmd.Stdout, cmd.Stderr = writer, writer
	if err = cmd.Start(); err != nil {
		input.Close()
		output.Close()
		writer.Close()
		return nil, err
	}
	writer.Close()
	secret := make([]byte, 24)
	if _, err := rand.Read(secret); err != nil {
		input.Close()
		output.Close()
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		_ = cmd.Wait()
		return nil, err
	}
	sh := &commandShell{cmd: cmd, input: input, output: output, marker: append([]byte{0x1e}, []byte(hex.EncodeToString(secret))...), done: make(chan struct{})}
	go func() { _ = cmd.Wait(); close(sh.done) }()
	// The shell survives cancellation while foreground children receive SIGTERM.
	if _, err := io.WriteString(sh.input, "trap ':' INT TERM\n"); err != nil {
		sh.Close()
		return nil, err
	}
	return sh, nil
}

// quoteShell passes arbitrary command text as one literal argument to eval.
func quoteShell(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }

// Run evaluates one line in the persistent shell and streams output before returning its directory.
func (s *commandShell) Run(command string, output func([]byte) error) (string, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(command) > 64<<10 || strings.IndexByte(command, 0) >= 0 {
		return "", 0, errors.New("command is too long or contains a NUL byte")
	}
	// Eval's stdin is closed so a program cannot consume the shell's control stream.
	statusVar := "__rmt_" + string(s.marker[1:])
	script := fmt.Sprintf("(exit %d)\n", s.status) + "eval " + quoteShell(command) + " </dev/null\n" + statusVar + "=$?\ncommand printf '\\036" + string(s.marker[1:]) + "%s\\000%s\\000' \"$" + statusVar + "\" \"$(pwd -P)\"\n"
	if _, err := io.WriteString(s.input, script); err != nil {
		return "", 0, fmt.Errorf("host shell stopped: %w", err)
	}
	buf := append([]byte(nil), s.pending...)
	s.pending = nil
	tmp := make([]byte, 8192)
	for {
		if i := bytes.Index(buf, s.marker); i >= 0 {
			if i > 0 {
				if err := output(buf[:i]); err != nil {
					return "", 0, err
				}
			}
			buf = buf[i+len(s.marker):]
			for {
				first := bytes.IndexByte(buf, 0)
				if first >= 0 {
					second := bytes.IndexByte(buf[first+1:], 0)
					if second >= 0 {
						second += first + 1
						code, err := strconv.Atoi(string(buf[:first]))
						if err != nil {
							return "", 0, errors.New("invalid host shell status")
						}
						dir := string(buf[first+1 : second])
						if dir == "" {
							return "", 0, errors.New("host shell did not report its directory")
						}
						s.status = code
						s.pending = append(s.pending, buf[second+1:]...)
						return dir, code, nil
					}
				}
				if len(buf) > 1<<20 {
					return "", 0, errors.New("host shell response is too large")
				}
				n, err := s.output.Read(tmp)
				buf = append(buf, tmp[:n]...)
				if err != nil {
					return "", 0, fmt.Errorf("host shell stopped: %w", err)
				}
			}
		}
		keep := 0
		for n := 1; n < len(s.marker) && n <= len(buf); n++ {
			if bytes.Equal(buf[len(buf)-n:], s.marker[:n]) {
				keep = n
			}
		}
		if len(buf) > keep {
			n := len(buf) - keep
			if err := output(buf[:n]); err != nil {
				return "", 0, err
			}
			buf = append(buf[:0], buf[n:]...)
		}
		n, err := s.output.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if err != nil {
			if len(buf) > 0 {
				_ = output(buf)
			}
			return "", 0, fmt.Errorf("host shell stopped: %w", err)
		}
	}
}

// Interrupt cancels a remote command without ending its persistent shell.
func (s *commandShell) Interrupt() error { return syscall.Kill(-s.cmd.Process.Pid, syscall.SIGTERM) }

// Close releases the shell input without signaling commands it has already started.
func (s *commandShell) Close() {
	s.closeOnce.Do(func() {
		_ = s.input.Close()
		// Background jobs may still hold stdout; drain it until they finish.
		go func() { s.mu.Lock(); defer s.mu.Unlock(); _, _ = io.Copy(io.Discard, s.output); _ = s.output.Close() }()
	})
}
