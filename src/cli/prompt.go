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

	"github.com/ergochat/readline"
	"github.com/fschrhunt/rmt/src/client"
	"github.com/fschrhunt/rmt/src/extensions"
	"github.com/fschrhunt/rmt/src/shell"
)

const (
	colorUser        = "\x1b[38;5;242m"
	colorPath        = "\x1b[34m"
	colorGitBranch   = "\x1b[38;5;242m"
	colorGitDirty    = "\x1b[38;5;218m"
	colorGitAction   = "\x1b[33m"
	colorGitArrow    = "\x1b[36m"
	colorExecution   = "\x1b[33m"
	colorPromptOK    = "\x1b[35m"
	colorPromptError = "\x1b[31m"
	colorReset       = "\x1b[0m"
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

// paint applies one SGR color when the terminal supports it.
func paint(color, text string, enabled bool) string {
	if !enabled || text == "" {
		return text
	}
	return color + text + colorReset
}

// abbreviateHome shortens the host home directory to Pure's tilde form.
func abbreviateHome(home, dir string) string {
	if home != "" {
		if dir == home {
			return "~"
		}
		if strings.HasPrefix(dir, home+"/") {
			return "~" + strings.TrimPrefix(dir, home)
		}
	}
	return path.Clean(dir)
}

// humanDuration formats an elapsed time the way Pure's precmd does.
func humanDuration(d time.Duration) string {
	total := int64(d / time.Second)
	days, hours := total/86400, total/3600%24
	minutes, seconds := total/60%60, total%60
	var result strings.Builder
	if days > 0 {
		fmt.Fprintf(&result, "%dd ", days)
	}
	if hours > 0 {
		fmt.Fprintf(&result, "%dh ", hours)
	}
	if minutes > 0 {
		fmt.Fprintf(&result, "%dm ", minutes)
	}
	fmt.Fprintf(&result, "%ds", seconds)
	return result.String()
}

// renderPreprompt builds Pure's first line: machine, path, git state and slow command time.
func renderPreprompt(name, user, host, home, dir string, git shell.Git, hasGit bool, duration time.Duration, color bool) string {
	machine := host
	if machine == "" {
		machine = name
	}
	if user != "" && host != "" {
		machine = user + "@" + host
	}
	parts := make([]string, 0, 8)
	if machine != "" {
		parts = append(parts, paint(colorUser, machine, color))
	}
	parts = append(parts, paint(colorPath, abbreviateHome(home, dir), color))
	if hasGit && git.Branch != "" {
		branch := paint(colorGitBranch, git.Branch, color)
		if git.Dirty {
			branch += paint(colorGitDirty, "*", color)
		}
		parts = append(parts, branch)
		if git.Action != "" {
			parts = append(parts, paint(colorGitAction, git.Action, color))
		}
		if git.Behind > 0 || git.Ahead > 0 {
			arrows := ""
			if git.Behind > 0 {
				arrows += "⇣"
			}
			if git.Ahead > 0 {
				arrows += "⇡"
			}
			parts = append(parts, paint(colorGitArrow, arrows, color))
		}
		if git.Stash > 0 {
			parts = append(parts, paint(colorGitArrow, "≡", color))
		}
	}
	if duration > 5*time.Second {
		parts = append(parts, paint(colorExecution, humanDuration(duration), color))
	}
	return strings.Join(parts, " ")
}

// renderPrompt builds Pure's editable line, red after a failed command.
func renderPrompt(status int, color bool) string {
	symbol := "❯"
	if !color {
		return symbol + " "
	}
	c := colorPromptOK
	if status != 0 {
		c = colorPromptError
	}
	return c + symbol + colorReset + " "
}

// isTerminal reports whether rmt may use terminal editing and ANSI colors.
func isTerminal(file *os.File) bool {
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
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

// remoteCompleter offers host paths as suffixes relative to the word at the cursor.
type remoteCompleter struct {
	name      string
	directory func() string
}

// Do implements readline.AutoCompleter.
func (c remoteCompleter) Do(line []rune, pos int) ([][]rune, int) {
	if pos < 0 || pos > len(line) || pos != len(line) {
		return nil, 0
	}
	head := line[:pos]
	start := len(head)
	for start > 0 && head[start-1] != ' ' && head[start-1] != '\t' {
		start--
	}
	prefix := string(head[start:])
	if strings.ContainsAny(prefix, "\\\"'`$*?[]{};|&<>()") {
		return nil, 0
	}
	matches, err := client.Complete(c.name, c.directory(), prefix)
	if err != nil {
		return nil, 0
	}
	var candidates [][]rune
	for _, match := range matches {
		escaped := escapeCompletion(match)
		if escaped == "" || !strings.HasPrefix(escaped, prefix) {
			continue
		}
		candidates = append(candidates, []rune(escaped[len(prefix):]))
	}
	if len(candidates) == 0 {
		return nil, 0
	}
	return candidates, len([]rune(prefix))
}

// gitCache keeps the last repository state and refreshes it without delaying input.
type gitCache struct {
	mu         sync.Mutex
	directory  string
	info       shell.Git
	ready      bool
	generation uint64
}

// refresh starts a bounded host query; stale results from an older directory are ignored.
func (c *gitCache) refresh(host, directory string) {
	c.mu.Lock()
	if c.directory != directory {
		c.directory = directory
		c.info = shell.Git{}
		c.ready = false
	}
	c.generation++
	generation := c.generation
	c.mu.Unlock()
	go func() {
		info, err := client.Git(host, directory)
		if err != nil {
			return
		}
		c.mu.Lock()
		if c.generation == generation && c.directory == directory {
			c.info = info
			c.ready = true
		}
		c.mu.Unlock()
	}()
}

// get returns repository state only when it describes the requested directory.
func (c *gitCache) get(directory string) (shell.Git, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.directory != directory || !c.ready {
		return shell.Git{}, false
	}
	return c.info, true
}

// prompt edits input locally and sends ordinary commands to the connected shell.
func prompt(name string, remote *client.Shell, input *bufio.Reader, output io.Writer, registry *extensions.Registry) error {
	home := remote.Home()
	if home == "" {
		home = remote.Directory()
	}
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

	var editor *readline.Instance
	if isTerminal(os.Stdin) && isTerminal(os.Stdout) {
		editor, _ = readline.NewFromConfig(&readline.Config{
			Prompt:          renderPrompt(0, true),
			AutoComplete:    remoteCompleter{name: name, directory: func() string { return remote.Directory() }},
			InterruptPrompt: "^C",
			EOFPrompt:       "",
		})
		if editor != nil {
			defer editor.Close()
		}
	}

	var git gitCache
	git.refresh(name, remote.Directory())
	status := 0
	var duration time.Duration
	for {
		dir := remote.Directory()
		var line string
		var err error
		if editor != nil {
			fmt.Fprint(output, "\n")
			info, ready := git.get(dir)
			fmt.Fprint(output, renderPreprompt(name, remote.User(), remote.Host(), home, dir, info, ready, duration, true))
			fmt.Fprintln(output)
			editor.SetPrompt(renderPrompt(status, true))
			line, err = editor.ReadLine()
			if errors.Is(err, readline.ErrInterrupt) {
				continue
			}
		} else {
			fmt.Fprint(output, promptLabel(name, home, dir))
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
				git.refresh(name, remote.Directory())
				continue
			}
			resolved := shell.AliasCommand(aliases.String(), cmd.Alias, programs...)
			cmd.Program, cmd.Unsupported = resolved.Program, resolved.Unsupported
			if cmd.Unsupported != "" {
				fmt.Fprintf(output, "rmt: %s is not supported. Supported applications: %s.\n", cmd.Unsupported, supported)
				continue
			}
		}

		start := time.Now()
		if cmd.Program != "" {
			signals := make(chan os.Signal, 1)
			signal.Notify(signals, os.Interrupt)
			err = client.Launch(ctx, name, cmd.Program, line, remote, registry)
			signal.Stop(signals)
			status = 0
			if err != nil {
				status = 1
			}
		} else {
			status, err = remote.Run(line, output)
		}
		duration = time.Since(start)

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
				git.refresh(name, remote.Directory())
				continue
			}
			if cmd.Program == "" {
				return err
			}
		}
		git.refresh(name, remote.Directory())
	}
}
