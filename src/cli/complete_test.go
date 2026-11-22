package cli

import (
	"context"
	"slices"
	"testing"
	"testing/synctest"
	"time"
)

// completionFunc allows tests to hold a remote lookup open without a network.
type completionFunc func(context.Context, string, string) ([]string, error)

func (f completionFunc) Complete(ctx context.Context, directory, prefix string) ([]string, error) {
	return f(ctx, directory, prefix)
}

// TestSlowCompletionReturnsToEditor keeps a slow lookup available for the next Tab.
func TestSlowCompletionReturnsToEditor(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		reply := make(chan struct{})
		host := completionFunc(func(ctx context.Context, _, _ string) ([]string, error) {
			select {
			case <-reply:
				return []string{"project/"}, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		})
		var cache completionCache
		start := time.Now()
		if got := cache.lookup(t.Context(), host, "/home", "pro"); got != nil {
			t.Fatalf("got results before the host replied: %v", got)
		}
		if elapsed := time.Since(start); elapsed != completionWait {
			t.Fatalf("editor waited %v for a slow lookup", elapsed)
		}
		synctest.Wait()
		close(reply)
		synctest.Wait()
		if got := cache.lookup(t.Context(), host, "/home", "pro"); !slices.Equal(got, []string{"project/"}) {
			t.Fatalf("completion = %v", got)
		}
	})
}

// TestFastCompletionUsesFirstTab returns results without requiring a second keypress.
func TestFastCompletionUsesFirstTab(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		host := completionFunc(func(context.Context, string, string) ([]string, error) {
			time.Sleep(time.Millisecond)
			return []string{"project/"}, nil
		})
		var cache completionCache
		if got := cache.lookup(t.Context(), host, "/home", "pro"); !slices.Equal(got, []string{"project/"}) {
			t.Fatalf("completion = %v", got)
		}
	})
}
