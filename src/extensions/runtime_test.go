package extensions

import (
	"context"
	"errors"
	"testing"
	"time"
)

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
