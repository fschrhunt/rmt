package extensions

import (
	"context"
	"fmt"
	"regexp"

	lua "github.com/yuin/gopher-lua"
)

var nameRE = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)

var reserved = map[string]bool{
	"opencode": true, "codex": true, "exit": true,
	"if": true, "then": true, "else": true, "elif": true, "fi": true,
	"for": true, "while": true, "until": true, "do": true, "done": true,
	"case": true, "esac": true, "in": true, "function": true, "select": true,
	"time": true, "coproc": true, "eval": true, "command": true, "builtin": true,
	"unalias": true, "printf": true, "cd": true, "alias": true, "source": true,
	"exec": true, "read": true, "export": true, "set": true, "unset": true,
	"trap": true, "pwd": true, "shift": true,
}

// program holds the callbacks one extension registered for a program name.
type program struct {
	host, client *lua.LFunction
}

// Host runs the program's host callback and returns its table, which must
// contain a local URL or private Unix socket and only string values.
func (r *Registry) Host(ctx context.Context, name, directory string, run func(string, []string) (string, string, int, error)) (map[string]string, error) {
	in, err := r.instantiate(ctx)
	if err != nil {
		return nil, err
	}
	defer in.close()
	p := in.programs[name]
	if p == nil || p.host == nil {
		return nil, fmt.Errorf("extension program %q has no host callback", name)
	}
	v, err := in.call(p.host, map[string]lua.LValue{
		"program":   lua.LString(name),
		"directory": lua.LString(directory),
		"run":       in.runner(&directory, run),
	})
	if err != nil {
		return nil, fmt.Errorf("extension program %q host: %w", name, err)
	}
	t, ok := v.(*lua.LTable)
	if !ok {
		return nil, fmt.Errorf("extension program %q host must return a table, got %s", name, v.Type())
	}
	out := map[string]string{}
	var bad error
	t.ForEach(func(k, v lua.LValue) {
		ks, kok := k.(lua.LString)
		vs, vok := v.(lua.LString)
		if !kok || !vok {
			bad = fmt.Errorf("extension program %q host table must map strings to strings", name)
			return
		}
		out[string(ks)] = string(vs)
	})
	if bad != nil {
		return nil, bad
	}
	if (out["url"] == "") == (out["socket"] == "") {
		return nil, fmt.Errorf("extension program %q host table needs a URL or socket", name)
	}
	return out, nil
}

// Client runs the program's client callback and returns its non-empty argv.
func (r *Registry) Client(ctx context.Context, name, directory, endpoint string, args []string) ([]string, error) {
	in, err := r.instantiate(ctx)
	if err != nil {
		return nil, err
	}
	defer in.close()
	p := in.programs[name]
	if p == nil || p.client == nil {
		return nil, fmt.Errorf("extension program %q has no client callback", name)
	}
	v, err := in.call(p.client, map[string]lua.LValue{
		"program":   lua.LString(name),
		"directory": lua.LString(directory),
		"endpoint":  lua.LString(endpoint),
		"args":      stringArray(in.L, args),
	})
	if err != nil {
		return nil, fmt.Errorf("extension program %q client: %w", name, err)
	}
	argv, err := toStrings(v)
	if err != nil || len(argv) == 0 {
		return nil, fmt.Errorf("extension program %q client must return a non-empty array of strings", name)
	}
	return argv, nil
}

// registerProgram implements rmt.program.
func (in *instance) registerProgram(L *lua.LState) int {
	t := L.CheckTable(1)
	var name string
	p := &program{}
	t.ForEach(func(k, v lua.LValue) {
		switch k {
		case lua.LString("name"):
			s, ok := v.(lua.LString)
			if !ok {
				L.ArgError(1, "name must be a string")
			}
			name = string(s)
		case lua.LString("host"), lua.LString("client"):
			f, ok := v.(*lua.LFunction)
			if !ok {
				L.ArgError(1, fmt.Sprintf("%s must be a function", k))
			}
			if k == lua.LString("host") {
				p.host = f
			} else {
				p.client = f
			}
		default:
			L.ArgError(1, fmt.Sprintf("unknown field %s", k))
		}
	})
	if !nameRE.MatchString(name) {
		L.ArgError(1, fmt.Sprintf("invalid program name %q (use lowercase letters, digits, - and _)", name))
	}
	if reserved[name] {
		L.ArgError(1, fmt.Sprintf("program %q is reserved", name))
	}
	if p.host == nil && p.client == nil {
		L.ArgError(1, fmt.Sprintf("program %q needs a host or client function", name))
	}
	if in.programs[name] != nil {
		L.ArgError(1, fmt.Sprintf("program %q is already registered", name))
	}
	in.programs[name] = p
	return 0
}
