package cli

import (
	"context"
	"sync"
	"time"
)

// pathCompleter is the host's asynchronous path lookup.
type pathCompleter interface {
	Complete(context.Context, string, string) ([]string, error)
}

const completionWait = 75 * time.Millisecond

// completionCache keeps one lookup so a slow network cannot hold up the editor.
type completionCache struct {
	mu                sync.Mutex
	directory, prefix string
	request           *completionRequest
}

type completionRequest struct {
	cancel  context.CancelFunc
	done    chan struct{}
	matches []string
}

// lookup gives a fast reply a chance to complete on the first Tab.
func (c *completionCache) lookup(ctx context.Context, host pathCompleter, directory, prefix string) []string {
	c.mu.Lock()
	if c.request != nil {
		if c.directory == directory && c.prefix == prefix {
			request := c.request
			c.mu.Unlock()
			return waitForCompletion(ctx, request)
		}
		c.request.cancel()
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	request := &completionRequest{cancel: cancel, done: make(chan struct{})}
	c.directory, c.prefix, c.request = directory, prefix, request
	c.mu.Unlock()
	go func() {
		defer cancel()
		defer close(request.done)
		matches, err := host.Complete(ctx, directory, prefix)
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.request == request {
			if err != nil {
				c.request = nil
			} else {
				request.matches = matches
			}
		}
	}()
	return waitForCompletion(ctx, request)
}

// waitForCompletion bounds input latency without canceling a lookup the next Tab can reuse.
func waitForCompletion(ctx context.Context, request *completionRequest) []string {
	timer := time.NewTimer(completionWait)
	defer timer.Stop()
	select {
	case <-request.done:
		return request.matches
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return nil
	}
}

// clear invalidates results when the user submits a command.
func (c *completionCache) clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.request != nil {
		c.request.cancel()
		c.request = nil
	}
}
