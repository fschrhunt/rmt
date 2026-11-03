// Package extensions loads trusted, user-local Lua files that register custom
// native programs and host launch preparation callbacks.
//
// An extension file calls:
//
//	rmt.program({name = "x", host = function(ctx) return {url = "..."} end,
//	             client = function(ctx) return {"argv", ...} end})
//	rmt.on("prepare", function(ctx) return {directory = "...", args = {...}} end)
//
// Files are read once by Load and executed in lexical order. Every invocation
// runs them again in a fresh Lua state, so no Lua state is shared between
// calls and a Registry is safe for concurrent use. Extensions are trusted code
// from the user's own configuration; the Lua state only receives the base,
// table, string, and math libraries, and callbacks are bounded by the caller's
// context and a fixed timeout.
package extensions

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"syscall"
	"time"

	lua "github.com/yuin/gopher-lua"
)

// callTimeout bounds loading and each callback invocation.
const callTimeout = 30 * time.Second

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

type source struct {
	path string
	code []byte
}

// Registry is the immutable set of extensions found by Load.
type Registry struct {
	sources  []source
	programs []string
}

// program holds the callbacks one extension registered for a program name.
type program struct {
	host, client *lua.LFunction
}

// instance is one fresh Lua state with every extension file executed in it.
type instance struct {
	L        *lua.LState
	ctx      context.Context
	parent   context.Context
	cancel   context.CancelFunc
	programs map[string]*program
	prepares []*lua.LFunction
}

// Load reads dir/*.lua in lexical order and validates every registration.
// A missing or empty directory yields an empty Registry.
func Load(dir string) (*Registry, error) {
	info, err := os.Lstat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return &Registry{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("inspect extensions directory: %w", err)
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !info.IsDir() || !ok || owner.Uid != uint32(os.Getuid()) || info.Mode().Perm()&0022 != 0 {
		return nil, fmt.Errorf("extensions directory %s must be owned by this user and not writable by others", dir)
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return &Registry{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read extensions directory: %w", err)
	}
	r := &Registry{}
	for _, e := range entries { // ReadDir returns entries sorted by name
		if e.IsDir() || filepath.Ext(e.Name()) != ".lua" {
			continue
		}
		path := filepath.Join(dir, e.Name())
		info, err := os.Lstat(path)
		if err != nil {
			return nil, fmt.Errorf("inspect extension %s: %w", e.Name(), err)
		}
		owner, ok := info.Sys().(*syscall.Stat_t)
		if !info.Mode().IsRegular() || !ok || owner.Uid != uint32(os.Getuid()) || info.Mode().Perm()&0022 != 0 {
			return nil, fmt.Errorf("extension %s must be a regular file owned by this user and not writable by others", e.Name())
		}
		code, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read extension %s: %w", e.Name(), err)
		}
		r.sources = append(r.sources, source{path: path, code: code})
	}
	in, err := r.instantiate(context.Background())
	if err != nil {
		return nil, err
	}
	defer in.close()
	for name := range in.programs {
		r.programs = append(r.programs, name)
	}
	sort.Strings(r.programs)
	return r, nil
}

// Programs returns the registered program names in sorted order.
func (r *Registry) Programs() []string { return append([]string(nil), r.programs...) }

// Has reports whether an extension registered the program name.
func (r *Registry) Has(name string) bool {
	i := sort.SearchStrings(r.programs, name)
	return i < len(r.programs) && r.programs[i] == name
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

// Prepare runs every "prepare" callback in registration order. A callback may
// return nil or {directory=, args=} to replace the launch directory and
// arguments seen by later callbacks. ctx.run(argv) executes a command through
// run and returns stdout, stderr, status; a run error aborts the callback.
func (r *Registry) Prepare(ctx context.Context, program, directory string, args []string, run func(string, []string) (stdout, stderr string, status int, err error)) (string, []string, error) {
	in, err := r.instantiate(ctx)
	if err != nil {
		return "", nil, err
	}
	defer in.close()
	runFn := in.runner(&directory, run)
	for i, fn := range in.prepares {
		v, err := in.call(fn, map[string]lua.LValue{
			"program":   lua.LString(program),
			"directory": lua.LString(directory),
			"args":      stringArray(in.L, args),
			"run":       runFn,
		})
		if err != nil {
			return "", nil, fmt.Errorf("extension prepare callback %d: %w", i+1, err)
		}
		if v == lua.LNil {
			continue
		}
		t, ok := v.(*lua.LTable)
		if !ok {
			return "", nil, fmt.Errorf("extension prepare callback %d must return nil or a table, got %s", i+1, v.Type())
		}
		var bad error
		t.ForEach(func(k, v lua.LValue) {
			switch k {
			case lua.LString("directory"):
				s, ok := v.(lua.LString)
				if !ok || s == "" {
					bad = errors.New("directory must be a non-empty string")
					return
				}
				directory = string(s)
			case lua.LString("args"):
				a, err := toStrings(v)
				if err != nil {
					bad = errors.New("args must be an array of strings")
					return
				}
				args = a
			default:
				bad = fmt.Errorf("unknown field %s", k)
			}
		})
		if bad != nil {
			return "", nil, fmt.Errorf("extension prepare callback %d: %w", i+1, bad)
		}
	}
	return directory, args, nil
}

// runner adapts the host command runner to an extension callback.
func (in *instance) runner(directory *string, run func(string, []string) (string, string, int, error)) *lua.LFunction {
	return in.L.NewFunction(func(L *lua.LState) int {
		argv, err := toStrings(L.Get(1))
		if err != nil || len(argv) == 0 {
			L.ArgError(1, "argv must be a non-empty array of strings")
		}
		if run == nil {
			L.RaiseError("ctx.run is unavailable")
		}
		// A slow external command must not spend the Lua execution time budget.
		in.cancel()
		stdout, stderr, status, err := run(*directory, argv)
		in.ctx, in.cancel = context.WithTimeout(in.parent, callTimeout)
		L.SetContext(in.ctx)
		if err != nil {
			L.RaiseError("run %s: %s", argv[0], err)
		}
		L.Push(lua.LString(stdout))
		L.Push(lua.LString(stderr))
		L.Push(lua.LNumber(status))
		return 3
	})
}

// instantiate creates a fresh state and executes every extension file in it.
func (r *Registry) instantiate(ctx context.Context) (*instance, error) {
	parent := ctx
	ctx, cancel := context.WithTimeout(parent, callTimeout)
	L := lua.NewState(lua.Options{SkipOpenLibs: true})
	in := &instance{L: L, ctx: ctx, parent: parent, cancel: cancel, programs: map[string]*program{}}
	L.SetContext(ctx)
	for _, lib := range []struct {
		name string
		fn   lua.LGFunction
	}{
		{lua.BaseLibName, lua.OpenBase},
		{lua.TabLibName, lua.OpenTable},
		{lua.StringLibName, lua.OpenString},
		{lua.MathLibName, lua.OpenMath},
	} {
		L.Push(L.NewFunction(lib.fn))
		L.Push(lua.LString(lib.name))
		L.Call(1, 0)
	}
	rmt := L.NewTable()
	L.SetField(rmt, "program", L.NewFunction(in.registerProgram))
	L.SetField(rmt, "on", L.NewFunction(in.registerOn))
	L.SetField(rmt, "getenv", L.NewFunction(func(L *lua.LState) int {
		name := L.CheckString(1)
		if value, ok := os.LookupEnv(name); ok {
			L.Push(lua.LString(value))
		} else {
			L.Push(lua.LNil)
		}
		return 1
	}))
	L.SetGlobal("rmt", rmt)
	for _, s := range r.sources {
		fn, err := L.Load(bytes.NewReader(s.code), s.path)
		if err == nil {
			L.Push(fn)
			err = L.PCall(0, 0, nil)
		}
		if err != nil {
			in.close()
			return nil, fmt.Errorf("extension %s: %w", filepath.Base(s.path), in.wrap(err))
		}
	}
	return in, nil
}

func (in *instance) close() {
	in.L.Close()
	in.cancel()
}

// wrap reports context cancellation or timeout in place of Lua's abort error.
func (in *instance) wrap(err error) error {
	if cerr := in.ctx.Err(); cerr != nil {
		return cerr
	}
	return err
}

// call invokes fn with a context table built from fields and returns its
// single result.
func (in *instance) call(fn *lua.LFunction, fields map[string]lua.LValue) (lua.LValue, error) {
	t := in.L.NewTable()
	for k, v := range fields {
		in.L.SetField(t, k, v)
	}
	if err := in.L.CallByParam(lua.P{Fn: fn, NRet: 1, Protect: true}, t); err != nil {
		return nil, in.wrap(err)
	}
	v := in.L.Get(-1)
	in.L.Pop(1)
	return v, nil
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

// registerOn implements rmt.on; "prepare" is the only supported event.
func (in *instance) registerOn(L *lua.LState) int {
	event := L.CheckString(1)
	fn := L.CheckFunction(2)
	if event != "prepare" {
		L.ArgError(1, fmt.Sprintf("unknown event %q (supported: prepare)", event))
	}
	in.prepares = append(in.prepares, fn)
	return 0
}

func stringArray(L *lua.LState, s []string) *lua.LTable {
	t := L.CreateTable(len(s), 0)
	for _, v := range s {
		t.Append(lua.LString(v))
	}
	return t
}

// toStrings converts a pure Lua array of strings; any other shape is an error.
func toStrings(v lua.LValue) ([]string, error) {
	t, ok := v.(*lua.LTable)
	if !ok {
		return nil, errors.New("not an array")
	}
	n := t.Len()
	out := make([]string, 0, n)
	for i := 1; i <= n; i++ {
		s, ok := t.RawGetInt(i).(lua.LString)
		if !ok {
			return nil, errors.New("not an array of strings")
		}
		out = append(out, string(s))
	}
	count := 0
	t.ForEach(func(lua.LValue, lua.LValue) { count++ })
	if count != n {
		return nil, errors.New("not an array")
	}
	return out, nil
}
