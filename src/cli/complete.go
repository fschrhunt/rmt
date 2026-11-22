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

// completionCache keeps one lookup so Tab never waits on the network.
type completionCache struct {
	mu                sync.Mutex
	directory, prefix string
	request           *completionRequest
}

type completionRequest struct {
	cancel  context.CancelFunc
	matches []string
}

// lookup returns ready matches, or starts a bounded lookup for a subsequent Tab.
func (c *completionCache) lookup(ctx context.Context, host pathCompleter, directory, prefix string) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.request != nil {
		if c.directory == directory && c.prefix == prefix {
			return c.request.matches
		}
		c.request.cancel()
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	request := &completionRequest{cancel: cancel}
	c.directory, c.prefix, c.request = directory, prefix, request
	go func() {
		defer cancel()
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
	return nil
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
