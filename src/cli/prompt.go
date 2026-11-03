package cli

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"

	"github.com/fschrhunt/rmt/src/client"
	"github.com/fschrhunt/rmt/src/extensions"
	"github.com/fschrhunt/rmt/src/shell"
	"github.com/peterh/liner"
)

func promptLabel(name, home, directory string) string {
	dir := directory
	if dir == home {
		dir = "~"
	} else if strings.HasPrefix(dir, home+"/") {
		dir = "~" + strings.TrimPrefix(dir, home)
	}
	return name + " " + path.Clean(dir) + ": "
}

// reconnect replaces a lost shell without repeating the interrupted command.
func reconnect(name string, old *client.Shell, output io.Writer) (*client.Shell, error) {
	dir := old.Directory()
	old.Abort()
	fmt.Fprintln(output, "[rmt reconnecting; the last command was not replayed]")
	var err error
	for i := 0; i < 3; i++ {
		if i > 0 {
			time.Sleep(time.Second)
		}
		var next *client.Shell
		next, err = client.ResumeShell(name, old)
		if err == nil {
			fmt.Fprintln(output, "[rmt reconnected]")
			return next, nil
		}
		if !errors.Is(err, client.ErrSessionExpired) {
			continue
		}
		next, err = client.OpenShell(name)
		if err != nil {
			continue
		}
		if dir != "" && dir != next.Directory() {
			quoted := "'" + strings.ReplaceAll(dir, "'", "'\\''") + "'"
			status, e := next.Run("cd "+quoted, io.Discard)
			if e != nil || status != 0 {
				fmt.Fprintln(output, "[rmt could not restore the previous directory]")
			}
		}
		fmt.Fprintln(output, "[rmt reconnected; shell variables were reset]")
		return next, nil
	}
	return nil, err
}

// escapeCompletion makes a remote filename safe to insert into a shell command.
func escapeCompletion(value string) string {
	var result strings.Builder
	for i, r := range value {
		if unicode.IsControl(r) {
			return ""
		}
		if !(unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("_./-", r) || (i == 0 && r == '~')) {
			result.WriteByte('\\')
		}
		result.WriteRune(r)
	}
	return result.String()
}

// prompt edits input locally and sends ordinary commands to the connected shell.
func prompt(name string, remote *client.Shell, input *bufio.Reader, output io.Writer, registry *extensions.Registry) error {
	home := remote.Directory()
	programs := registry.Programs()
	supported := strings.Join(append([]string{"opencode", "codex"}, programs...), ", ")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var remoteMu sync.Mutex
	terminated := make(chan os.Signal, 1)
	signal.Notify(terminated, syscall.SIGHUP, syscall.SIGTERM)
	defer signal.Stop(terminated)
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-terminated:
			cancel()
			finished := make(chan struct{})
			go func() { remoteMu.Lock(); remote.Close(); remoteMu.Unlock(); close(finished) }()
			select {
			case <-finished:
			case <-time.After(2 * time.Second):
			}
			os.Exit(0)
		case <-done:
			return
		}
	}()
	defer func() { remote.Close() }()
	original, terminalErr := liner.TerminalMode()
	var editor *liner.State
	var editing liner.ModeApplier
	if terminalErr == nil {
		editor = liner.NewLiner()
		defer editor.Close()
		editor.SetCtrlCAborts(true)
		editor.SetWordCompleter(func(line string, pos int) (string, []string, string) {
			chars := []rune(line)
			if pos < 0 || pos > len(chars) {
				return line, nil, ""
			}
			head, tail := string(chars[:pos]), string(chars[pos:])
			start := strings.LastIndexAny(head, " \t") + 1
			prefix := head[start:]
			if strings.ContainsAny(prefix, "\\\"'`$*?[]{};|&<>()") {
				return head, nil, tail
			}
			matches, err := client.Complete(name, remote.Directory(), prefix)
			if err != nil {
				return head, nil, tail
			}
			safe := matches[:0]
			for _, match := range matches {
				if escaped := escapeCompletion(match); escaped != "" {
					safe = append(safe, escaped)
				}
			}
			return head[:start], safe, tail
		})
		editing, _ = liner.TerminalMode()
		if original != nil {
			_ = original.ApplyMode()
		}
	}
	for {
		var line string
		var err error
		if editor != nil {
			if editing != nil {
				_ = editing.ApplyMode()
			}
			line, err = editor.Prompt(promptLabel(name, home, remote.Directory()))
			if original != nil {
				_ = original.ApplyMode()
			}
			if errors.Is(err, liner.ErrPromptAborted) {
				fmt.Fprintln(output)
				continue
			}
		} else {
			fmt.Fprint(output, promptLabel(name, home, remote.Directory()))
			line, err = input.ReadString('\n')
			if errors.Is(err, io.EOF) && line != "" {
				err = nil
			}
		}
		if errors.Is(err, io.EOF) {
			fmt.Fprintln(output, "[rmt is exiting]")
			return nil
		}
		if err != nil {
			return err
		}
		line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		if editor != nil {
			editor.AppendHistory(line)
		}
		cmd, err := shell.Parse(line, programs...)
		if err != nil {
			fmt.Fprintln(output, "rmt:", err)
			continue
		}
		if cmd.Exit {
			fmt.Fprintln(output, "[rmt is exiting]")
			return nil
		}
		if cmd.Unsupported != "" {
			fmt.Fprintf(output, "rmt: %s is not supported. Supported applications: %s.\n", cmd.Unsupported, supported)
			continue
		}
		if cmd.Alias != "" {
			var aliases bytes.Buffer
			quoted := "'" + strings.ReplaceAll(cmd.Alias, "'", "'\\''") + "'"
			lookup := "( __rmt_status=$?; builtin alias -- " + quoted + " 2>/dev/null; exit \"$__rmt_status\" )"
			if _, err = remote.Run(lookup, &aliases); err != nil {
				fmt.Fprintln(output, "rmt:", err)
				next, e := reconnect(name, remote, output)
				if e != nil {
					return e
				}
				remoteMu.Lock()
				remote = next
				remoteMu.Unlock()
				continue
			}
			resolved := shell.AliasCommand(aliases.String(), cmd.Alias, programs...)
			cmd.Program, cmd.Unsupported = resolved.Program, resolved.Unsupported
			if cmd.Unsupported != "" {
				fmt.Fprintf(output, "rmt: %s is not supported. Supported applications: %s.\n", cmd.Unsupported, supported)
				continue
			}
		}
		if cmd.Program != "" {
			signals := make(chan os.Signal, 1)
			signal.Notify(signals, os.Interrupt)
			err = client.Launch(ctx, name, cmd.Program, line, remote, registry)
			signal.Stop(signals)
		} else {
			_, err = remote.Run(line, output)
		}
		if err != nil {
			fmt.Fprintln(output, "rmt:", err)
			var lost *client.ConnectionError
			if cmd.Program == "" && errors.As(err, &lost) {
				next, e := reconnect(name, remote, output)
				if e != nil {
					return e
				}
				remoteMu.Lock()
				remote = next
				remoteMu.Unlock()
				continue
			}
			if cmd.Program == "" {
				return err
			}
		}
	}
}
