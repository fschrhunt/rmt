package extensions

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

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
