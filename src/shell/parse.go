package shell

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

// Command describes a local native application, a host command, or an unsupported interactive app.
type Command struct {
	Program     string
	Args        []string
	Unsupported string
	Exit        bool
}

// interactive lists programs that need a full remote terminal, which rmt does not provide.
var interactive = map[string]bool{
	"vi": true, "vim": true, "nvim": true, "nano": true, "emacs": true, "less": true, "more": true,
	"top": true, "htop": true, "btop": true, "man": true, "tmux": true, "screen": true,
	"ssh": true, "mosh": true, "sftp": true,
}

// literal returns a shell word only when it does not need host-side expansion.
func literal(w *syntax.Word) (string, bool) {
	dynamic := false
	syntax.Walk(w, func(n syntax.Node) bool {
		switch x := n.(type) {
		case *syntax.ParamExp, *syntax.CmdSubst, *syntax.ArithmExp, *syntax.ProcSubst, *syntax.ExtGlob, *syntax.BraceExp:
			dynamic = true
		case *syntax.Lit:
			if strings.ContainsAny(x.Value, "*?[") {
				dynamic = true
			}
		}
		return true
	})
	if dynamic {
		return "", false
	}
	value, err := expand.Literal(nil, w)
	return value, err == nil
}

// Parse intercepts supported native clients and refuses known interactive programs.
func Parse(line string) (Command, error) {
	parsed, err := syntax.NewParser(syntax.Variant(syntax.LangPOSIX)).Parse(strings.NewReader(line), "")
	if err != nil {
		return Command{}, fmt.Errorf("invalid shell command: %w", err)
	}
	if len(parsed.Stmts) == 0 {
		return Command{}, nil
	}
	var unsupported, programName string
	syntax.Walk(parsed, func(node syntax.Node) bool {
		call, ok := node.(*syntax.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		value, ok := literal(call.Args[0])
		if !ok {
			return true
		}
		name := filepath.Base(value)
		// Common launch wrappers do not turn an interactive program into a supported one.
		if name == "sudo" || name == "command" || name == "env" || name == "exec" {
			for _, word := range call.Args[1:] {
				v, ok := literal(word)
				if !ok {
					break
				}
				if strings.HasPrefix(v, "-") || strings.Contains(v, "=") {
					continue
				}
				name = filepath.Base(v)
				break
			}
		}
		if interactive[name] {
			unsupported = name
		}
		if name == "opencode" || name == "codex" {
			programName = name
		}
		if (name == "python" || name == "python3" || name == "node" || name == "irb") && len(call.Args) == 1 {
			unsupported = name
		}
		return true
	})
	if unsupported != "" {
		return Command{Unsupported: unsupported}, nil
	}
	if programName != "" {
		if len(parsed.Stmts) != 1 {
			return Command{}, errors.New("run supported applications directly, not inside a compound command")
		}
		stmt := parsed.Stmts[0]
		call, ok := stmt.Cmd.(*syntax.CallExpr)
		if !ok || len(stmt.Redirs) > 0 || stmt.Background || stmt.Negated || len(call.Assigns) > 0 || len(call.Args) == 0 {
			return Command{}, errors.New("run supported applications directly, without pipes or redirection")
		}
		name, ok := literal(call.Args[0])
		if !ok || filepath.Base(name) != programName {
			return Command{}, errors.New("run supported applications directly, without a shell wrapper")
		}
		args := make([]string, 0, len(call.Args)-1)
		for _, word := range call.Args[1:] {
			value, ok := literal(word)
			if !ok {
				return Command{}, errors.New("application arguments cannot contain shell expansion")
			}
			args = append(args, value)
		}
		return Command{Program: programName, Args: args}, nil
	}
	if len(parsed.Stmts) == 1 {
		stmt := parsed.Stmts[0]
		if call, ok := stmt.Cmd.(*syntax.CallExpr); ok && len(call.Args) == 1 && len(call.Assigns) == 0 && len(stmt.Redirs) == 0 && !stmt.Background {
			name, ok := literal(call.Args[0])
			if ok && name == "exit" {
				return Command{Exit: true}, nil
			}
		}
	}
	return Command{}, nil
}
