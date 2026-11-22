package hosts

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"
)

// commandShell keeps shell builtins, environment and directory between commands.
type commandShell struct {
	cmd       *exec.Cmd
	input     io.WriteCloser
	output    *os.File
	control   *os.File
	result    *bufio.Reader
	settings  *unix.Termios
	marker    []byte
	pending   []byte
	status    int
	done      chan struct{}
	mu        sync.Mutex
	closeOnce sync.Once
}

// newCommandShell starts the user's shell without a terminal or interactive stdin.
func newCommandShell(home, shellPath string) (*commandShell, error) {
	return openCommandShell(home, shellPath, "")
}

// openCommandShell gives an interactive client a controlling terminal, keeping the command pipe private.
func openCommandShell(home, shellPath, terminal string) (*commandShell, error) {
	if shellPath == "" {
		shellPath = os.Getenv("SHELL")
		if !filepath.IsAbs(shellPath) {
			shellPath = "/bin/sh"
		}
		if filepath.Base(shellPath) == "fish" {
			shellPath = "/bin/sh"
		}
	}
	secret := make([]byte, 24)
	if _, err := rand.Read(secret); err != nil {
		return nil, err
	}
	cmd := exec.Command(shellPath)
	cmd.Dir = home
	cmd.Env = append(os.Environ(), "HOME="+home, "PWD="+home, "TERM=dumb", "PAGER=cat", "GIT_PAGER=cat")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	input, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	var output, writer *os.File
	if terminal == "" {
		output, writer, err = os.Pipe()
	} else {
		output, writer, err = pty.Open()
		if err == nil {
			// Rewrap a nonblocking descriptor so Go can interrupt reads on close.
			var fd int
			fd, err = unix.FcntlInt(output.Fd(), unix.F_DUPFD_CLOEXEC, 0)
			_ = output.Close()
			if err == nil {
				err = unix.SetNonblock(fd, true)
				if err == nil {
					output = os.NewFile(uintptr(fd), "rmt-pty")
				} else {
					_ = unix.Close(fd)
				}
			}
			if err != nil {
				_ = writer.Close()
			}
		}
	}
	if err != nil {
		input.Close()
		return nil, err
	}
	var control, result *os.File
	var settings *unix.Termios
	if terminal != "" {
		settings, err = unix.IoctlGetTermios(int(writer.Fd()), terminalGet)
		if err == nil {
			// Match the editor's UTF-8 input when the terminal handles canonical erase.
			settings.Iflag |= unix.IUTF8
			disableTerminalSuspend(settings)
			err = unix.IoctlSetTermios(int(writer.Fd()), terminalReset, settings)
		}
		if err == nil {
			err = pty.Setsize(writer, &pty.Winsize{Rows: 24, Cols: 80})
		}
		if err == nil {
			control, result, err = os.Pipe()
		}
		if err != nil {
			input.Close()
			output.Close()
			writer.Close()
			return nil, err
		}
		cmd.ExtraFiles = []*os.File{writer, result}
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 3}
		cmd.Env = append(os.Environ(), "HOME="+home, "PWD="+home, "TERM="+terminal)
	}
	cmd.Stdout, cmd.Stderr = writer, writer
	if err = cmd.Start(); err != nil {
		input.Close()
		output.Close()
		writer.Close()
		if control != nil {
			control.Close()
			result.Close()
		}
		return nil, err
	}
	writer.Close()
	sh := &commandShell{cmd: cmd, input: input, output: output, marker: append([]byte{0x1e}, []byte(hex.EncodeToString(secret))...), done: make(chan struct{})}
	if control != nil {
		result.Close()
		sh.control = control
		sh.result = bufio.NewReaderSize(control, 64<<10)
		sh.settings = settings
	}
	go func() { _ = cmd.Wait(); close(sh.done) }()
	// Load the normal interactive configuration without making stdin interactive.
	// Interactive shells print prompts. Startup files read EOF instead of the control stream.
	startup := ""
	switch filepath.Base(shellPath) {
	case "zsh":
		startup = `[[ -r "${ZDOTDIR:-$HOME}/.zshrc" ]] && source "${ZDOTDIR:-$HOME}/.zshrc" </dev/null` + "\n"
	case "bash":
		startup = `shopt -s expand_aliases; [ ! -r "$HOME/.bashrc" ] || . "$HOME/.bashrc" </dev/null` + "\n"
	}
	if startup != "" {
		startup += "builtin cd -- " + quoteShell(home) + "\n"
	}
	// The shell survives cancellation while foreground children receive SIGTERM.
	if _, err := io.WriteString(sh.input, startup+"trap ':' INT TERM\n"); err != nil {
		sh.Close()
		return nil, err
	}
	return sh, nil
}

// resetTerminal discards keys the previous command did not consume and restores terminal settings.
func (s *commandShell) resetTerminal() error {
	raw, err := s.output.SyscallConn()
	if err != nil {
		return err
	}
	var resetErr error
	if err := raw.Control(func(fd uintptr) {
		resetErr = unix.IoctlSetTermios(int(fd), terminalReset, s.settings)
	}); err != nil {
		return err
	}
	return resetErr
}

// quoteShell passes arbitrary command text as one literal argument to eval.
func quoteShell(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }

// Run evaluates one line in the persistent shell and streams output before returning its directory.
func (s *commandShell) Run(command string, output func([]byte) error) (string, int, error) {
	return s.run(command, false, output)
}

// run evaluates a line, forwarding input from the PTY only for user commands.
func (s *commandShell) run(command string, interactive bool, output func([]byte) error) (string, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(command) > 64<<10 || strings.IndexByte(command, 0) >= 0 {
		return "", 0, errors.New("command is too long or contains a NUL byte")
	}
	// Eval's stdin is closed so a program cannot consume the shell's control stream.
	statusVar := "__rmt_" + string(s.marker[1:])
	// The suffix protects trailing newlines in the directory from command substitution.
	stdin := " </dev/null"
	if interactive && s.control != nil {
		stdin = " <&3"
	}
	printResult := "command printf '\\036" + string(s.marker[1:]) + "%s\\000%s\\000'"
	resultArgs := " \"$" + statusVar + "\" \"$(pwd -P && command printf .)\"\n"
	if s.control != nil {
		printResult = "command printf '%s\\000%s\\000' >&4"
		resultArgs += "command printf '\\036" + string(s.marker[1:]) + "' >&3\n"
	}
	script := fmt.Sprintf("(exit %d)\n", s.status) + "eval " + quoteShell(command) + stdin + "\n" + statusVar + "=$?\n" + printResult + resultArgs
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
			if s.result != nil {
				s.pending = append(s.pending, buf...)
				code, err := s.result.ReadSlice(0)
				if err != nil {
					return "", 0, fmt.Errorf("read terminal command status: %w", err)
				}
				status, err := strconv.Atoi(string(code[:len(code)-1]))
				if err != nil {
					return "", 0, errors.New("invalid terminal command status")
				}
				dir, err := s.result.ReadSlice(0)
				if err != nil {
					return "", 0, fmt.Errorf("read terminal command directory: %w", err)
				}
				s.status = status
				return strings.TrimSuffix(string(dir[:len(dir)-1]), "\n."), status, nil
			}
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
						dir := strings.TrimSuffix(string(buf[first+1:second]), "\n.")
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

// Close hangs up the terminal, or drains a non-terminal command without interrupting it.
func (s *commandShell) Close() {
	s.closeOnce.Do(func() {
		_ = s.input.Close()
		if s.control != nil {
			_ = s.output.Close()
			_ = s.control.Close()
			return
		}
		// Background jobs may still hold stdout; drain it until they finish.
		go func() { s.mu.Lock(); defer s.mu.Unlock(); _, _ = io.Copy(io.Discard, s.output); _ = s.output.Close() }()
	})
}
