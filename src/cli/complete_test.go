package cli

import (
	"context"
	"slices"
	"testing"
	"testing/synctest"
)

// completionFunc allows tests to hold a remote lookup open without a network.
type completionFunc func(context.Context, string, string) ([]string, error)

func (f completionFunc) Complete(ctx context.Context, directory, prefix string) ([]string, error) {
	return f(ctx, directory, prefix)
}

// TestCompletionDoesNotWait returns control to the editor while a lookup is in flight.
func TestCompletionDoesNotWait(t *testing.T) {
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
		if got := cache.lookup(t.Context(), host, "/home", "pro"); got != nil {
			t.Fatalf("got results before the host replied: %v", got)
		}
		synctest.Wait()
		close(reply)
		synctest.Wait()
		if got := cache.lookup(t.Context(), host, "/home", "pro"); !slices.Equal(got, []string{"project/"}) {
			t.Fatalf("completion = %v", got)
		}
	})
}
