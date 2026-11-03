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
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"syscall"
)

type source struct {
	path string
	code []byte
}

// Registry is the immutable set of extensions found by Load.
type Registry struct {
	sources  []source
	programs []string
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
