package client

import (
	"context"
	"fmt"
	"net"
	"strings"
	"syscall"
	"testing"
)

// TestDialErrorExplainsNextStep distinguishes name lookup, timeout, and refusal.
func TestDialErrorExplainsNextStep(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{&net.DNSError{IsNotFound: true}, "check the host address"},
		{context.DeadlineExceeded, "check the network"},
		{syscall.ECONNREFUSED, "check its listening address"},
	} {
		got := dialError("mini", "https://mini:47473", fmt.Errorf("wrapper: %w", tc.err)).Error()
		if !strings.Contains(got, tc.want) || !strings.Contains(got, "https://mini:47473") {
			t.Errorf("diagnostic = %q, want %q and the address", got, tc.want)
		}
	}
}
