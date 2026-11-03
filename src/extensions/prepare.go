package extensions

import (
	"context"
	"errors"
	"fmt"

	lua "github.com/yuin/gopher-lua"
)

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
