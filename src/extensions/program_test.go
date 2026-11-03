package extensions

import (
	"context"
	"reflect"
	"testing"
)

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
