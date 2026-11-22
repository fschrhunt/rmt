package client

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/chzyer/readline"
	"github.com/coder/websocket/wsjson"
	"github.com/fschrhunt/rmt/src/shell"
	"golang.org/x/sys/unix"
)

// terminalSize gets the local dimensions, falling back only when the terminal cannot report them.
func terminalSize(input *os.File) shell.Size {
	cols, rows, err := readline.GetSize(int(input.Fd()))
	if err != nil || cols <= 0 || rows <= 0 {
		return shell.Size{Rows: 24, Cols: 80}
	}
	return shell.Size{Rows: uint16(rows), Cols: uint16(cols)}
}

// forwardTerminal starts a raw keyboard and resize pump and returns a synchronous cleanup.
func (s *Shell) forwardTerminal(input *os.File, turn uint64) (func(), error) {
	fd := int(input.Fd())
	state, err := readline.MakeRaw(fd)
	if err != nil {
		return nil, fmt.Errorf("enter local terminal raw mode: %w", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)
	var done = make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 4096)
		fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
		for ctx.Err() == nil {
			select {
			case <-winch:
				if s.sendTerminal(ctx, shell.Request{Type: "resize", Turn: turn, Size: terminalSize(input)}) != nil {
					_ = s.conn.CloseNow()
					return
				}
			default:
			}
			n, err := unix.Poll(fds, 15)
			if err == unix.EINTR || n == 0 && err == nil {
				continue
			}
			if err != nil || fds[0].Revents&unix.POLLIN == 0 {
				_ = s.conn.CloseNow()
				return
			}
			if ctx.Err() != nil {
				return
			}
			n, err = unix.Read(fd, buf)
			if err == unix.EINTR || err == unix.EAGAIN {
				continue
			}
			if err != nil || n == 0 || s.sendTerminal(ctx, shell.Request{Type: "input", Turn: turn, Data: buf[:n]}) != nil {
				_ = s.conn.CloseNow()
				return
			}
		}
	}()
	return func() {
		signal.Stop(winch)
		cancel()
		<-done
		_ = readline.Restore(fd, state)
	}, nil
}

// sendTerminal serializes input and size changes with the shell's other writes.
func (s *Shell) sendTerminal(ctx context.Context, req shell.Request) error {
	s.write.Lock()
	defer s.write.Unlock()
	if s.closing {
		return net.ErrClosed
	}
	// A turn can finish while a key is being forwarded. Don't cancel an in-flight websocket write
	// solely because the terminal pump stopped; the host ignores keys from a completed turn.
	if err := ctx.Err(); err != nil {
		return nil
	}
	writeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	return wsjson.Write(writeCtx, s.conn, req)
}
