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
	Alias       string
	Unsupported string
	Exit        bool
}

// interactive lists programs that need a full remote terminal, which rmt does not provide.
var interactive = map[string]bool{
	"vi": true, "vim": true, "nvim": true, "nano": true, "emacs": true, "less": true, "more": true,
	"top": true, "htop": true, "btop": true, "man": true, "tmux": true, "screen": true,
	"ssh": true, "mosh": true, "sftp": true,
	"claude": true, "pi": true,
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
func Parse(line string, extra ...string) (Command, error) {
	supported := func(name string) bool {
		if name == "opencode" || name == "codex" {
			return true
		}
		for _, program := range extra {
			if program == name {
				return true
			}
		}
		return false
	}
	parsed, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(line), "")
	if err != nil {
		// Let the host shell interpret syntax specific to its configured shell.
		// Direct interactive launches must still pass the local routing checks.
		words := strings.Fields(line)
		if len(words) > 0 {
			name := filepath.Base(words[0])
			if supported(name) || interactive[name] {
				return Command{}, fmt.Errorf("invalid application command: %w", err)
			}
		}
		return Command{}, nil
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
		// `command -v` and `command -V` inspect a program without starting it.
		if name == "command" {
			for _, word := range call.Args[1:] {
				option, ok := literal(word)
				if !ok || option == "--" || !strings.HasPrefix(option, "-") {
					break
				}
				if strings.ContainsAny(option[1:], "vV") {
					return true
				}
			}
		}
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
		if interactive[name] && !supported(name) {
			unsupported = name
		}
		if supported(name) {
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
		if !ok || name != programName {
			return Command{}, errors.New("run supported applications directly, without a shell wrapper")
		}
		return Command{Program: programName}, nil
	}
	if len(parsed.Stmts) == 1 {
		stmt := parsed.Stmts[0]
		if call, ok := stmt.Cmd.(*syntax.CallExpr); ok && len(call.Args) > 0 && len(call.Assigns) == 0 && len(stmt.Redirs) == 0 && !stmt.Background && !stmt.Negated {
			if name, ok := literal(call.Args[0]); ok && name != "exit" && !strings.ContainsAny(name, "/= \t\n") {
				return Command{Alias: name}, nil
			}
		}
		if call, ok := stmt.Cmd.(*syntax.CallExpr); ok && len(call.Args) == 1 && len(call.Assigns) == 0 && len(stmt.Redirs) == 0 && !stmt.Background {
			name, ok := literal(call.Args[0])
			if ok && name == "exit" {
				return Command{Exit: true}, nil
			}
		}
	}
	return Command{}, nil
}

// AliasCommand identifies an alias whose expansion directly launches an interactive program.
func AliasCommand(output, name string, extra ...string) Command {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimPrefix(line, "alias ")
		if !strings.HasPrefix(line, name+"=") {
			continue
		}
		parsed, err := syntax.NewParser().Parse(strings.NewReader(line), "")
		if err != nil || len(parsed.Stmts) != 1 {
			continue
		}
		call, ok := parsed.Stmts[0].Cmd.(*syntax.CallExpr)
		if !ok || len(call.Assigns) != 1 || len(call.Args) != 0 || call.Assigns[0].Value == nil {
			continue
		}
		value, ok := literal(call.Assigns[0].Value)
		if !ok {
			continue
		}
		command, err := Parse(value, extra...)
		if err == nil {
			return command
		}
	}
	return Command{}
}
