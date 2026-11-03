package extensions

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func write(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, code := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(code), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestLoadEmptyAndMissingDirectory(t *testing.T) {
	for _, dir := range []string{t.TempDir(), filepath.Join(t.TempDir(), "missing")} {
		r, err := Load(dir)
		if err != nil || len(r.Programs()) != 0 || r.Has("x") {
			t.Fatalf("Load(%q) = %v, %v", dir, r, err)
		}
	}
}

// TestLoadRejectsWritableOrLinkedExtensions protects trusted host-side callbacks.
func TestLoadRejectsWritableOrLinkedExtensions(t *testing.T) {
	dir := write(t, map[string]string{"unsafe.lua": `rmt.on("prepare", function() end)`})
	file := filepath.Join(dir, "unsafe.lua")
	if err := os.Chmod(file, 0666); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil {
		t.Fatal("accepted writable extension")
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "target.lua")
	if err := os.WriteFile(target, []byte(`rmt.on("prepare", function() end)`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, file); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil {
		t.Fatal("accepted linked extension")
	}
}

func TestProgramsHostAndClient(t *testing.T) {
	dir := write(t, map[string]string{
		"b.lua":    `rmt.program({name="beta", client=function(c) return {c.program, c.directory, c.endpoint, table.concat(c.args, ",")} end})`,
		"a.lua":    `rmt.program({name="alpha", host=function(c) return {url="http://x/"..c.directory} end})`,
		"skip.txt": `not lua`,
	})
	r, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := r.Programs(); !reflect.DeepEqual(got, []string{"alpha", "beta"}) || !r.Has("beta") || r.Has("gamma") {
		t.Fatalf("Programs = %v", got)
	}
	host, err := r.Host(context.Background(), "alpha", "d", nil)
	if err != nil || !reflect.DeepEqual(host, map[string]string{"url": "http://x/d"}) {
		t.Fatalf("Host = %v, %v", host, err)
	}
	argv, err := r.Client(context.Background(), "beta", "d", "e", []string{"1", "2"})
	if err != nil || !reflect.DeepEqual(argv, []string{"beta", "d", "e", "1,2"}) {
		t.Fatalf("Client = %v, %v", argv, err)
	}
	if _, err := r.Host(context.Background(), "beta", "d", nil); err == nil {
		t.Fatal("Host on a client-only program should fail")
	}
}

func TestPrepareChainsCallbacksAndRuns(t *testing.T) {
	dir := write(t, map[string]string{
		"1.lua": `rmt.on("prepare", function(c)
			local out, err, status = c.run({"git", "root"})
			return {directory = out .. c.directory, args = {c.program, err, tostring(status)}}
		end)`,
		"2.lua": `rmt.on("prepare", function(c) return {args = {c.directory, table.concat(c.args, "|")}} end)
		rmt.on("prepare", function(c) return nil end)`,
	})
	r, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	run := func(_ string, argv []string) (string, string, int, error) {
		if !reflect.DeepEqual(argv, []string{"git", "root"}) {
			t.Errorf("argv = %v", argv)
		}
		return "/r/", "warn", 3, nil
	}
	d, args, err := r.Prepare(context.Background(), "codex", "sub", []string{"x"}, run)
	if err != nil || d != "/r/sub" || !reflect.DeepEqual(args, []string{"/r/sub", "codex|warn|3"}) {
		t.Fatalf("Prepare = %q, %v, %v", d, args, err)
	}
	failing := func(string, []string) (string, string, int, error) { return "", "", 0, errors.New("boom") }
	if _, _, err := r.Prepare(context.Background(), "codex", "sub", nil, failing); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("run error not surfaced: %v", err)
	}
}

func TestInvocationsDoNotShareState(t *testing.T) {
	dir := write(t, map[string]string{"a.lua": `
		n = (n or 0) + 1
		rmt.program({name="p", host=function() n = n + 1; return {url=tostring(n)} end})`})
	r, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if h, err := r.Host(context.Background(), "p", "", nil); err != nil || h["url"] != "2" {
			t.Fatalf("Host = %v, %v", h, err)
		}
	}
}

func TestLoadRejectsInvalidExtensions(t *testing.T) {
	cases := map[string]string{
		"syntax":         `rmt.program(`,
		"runtime":        `error("kaboom")`,
		"bad name":       `rmt.program({name="Bad Name", host=function() end})`,
		"no callbacks":   `rmt.program({name="p"})`,
		"unknown field":  `rmt.program({name="p", host=function() end, extra=1})`,
		"duplicate":      `rmt.program({name="p", host=function() end}) rmt.program({name="p", client=function() end})`,
		"unknown event":  `rmt.on("launch", function() end)`,
		"non-func event": `rmt.on("prepare", 1)`,
	}
	for name, code := range cases {
		if _, err := Load(write(t, map[string]string{"x.lua": code})); err == nil || !strings.Contains(err.Error(), "x.lua") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	dir := write(t, map[string]string{
		"a.lua": `rmt.program({name="p", host=function() end})`,
		"b.lua": `rmt.program({name="p", host=function() end})`,
	})
	if _, err := Load(dir); err == nil || !strings.Contains(err.Error(), "b.lua") {
		t.Errorf("duplicate across files: %v", err)
	}
}

func TestCallbacksRejectBadOutputShapes(t *testing.T) {
	dir := write(t, map[string]string{"a.lua": `
		rmt.program({name="nourl", host=function() return {x="y"} end, client=function() return {} end})
		rmt.program({name="nontable", host=function() return "u" end, client=function() return {1} end})
		rmt.program({name="mixed", host=function() return {url=1} end, client=function() return {"a", x="b"} end})`})
	r, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, name := range []string{"nourl", "nontable", "mixed"} {
		if _, err := r.Host(ctx, name, "", nil); err == nil {
			t.Errorf("Host %s should fail", name)
		}
		if _, err := r.Client(ctx, name, "", "", nil); err == nil {
			t.Errorf("Client %s should fail", name)
		}
	}
	for _, code := range []string{
		`return 5`, `return {directory=""}`, `return {args={1}}`, `return {args={"a", k="b"}}`, `return {other="x"}`,
	} {
		r, err := Load(write(t, map[string]string{"a.lua": `rmt.on("prepare", function() ` + code + ` end)`}))
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := r.Prepare(ctx, "p", "d", nil, nil); err == nil {
			t.Errorf("Prepare %q should fail", code)
		}
	}
}

func TestContextCancellationStopsCallbacks(t *testing.T) {
	dir := write(t, map[string]string{"a.lua": `rmt.program({name="spin", host=function() while true do end end})`})
	r, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := r.Host(ctx, "spin", "", nil); done <- err }()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("err = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("callback was not interrupted")
	}
}
