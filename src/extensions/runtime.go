package extensions

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	lua "github.com/yuin/gopher-lua"
)

// callTimeout bounds loading and each callback invocation.
const callTimeout = 30 * time.Second

// instance is one fresh Lua state with every extension file executed in it.
type instance struct {
	L        *lua.LState
	ctx      context.Context
	parent   context.Context
	cancel   context.CancelFunc
	programs map[string]*program
	prepares []*lua.LFunction
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
