package client

import (
	"errors"
	"fmt"
	"net"
	"syscall"
)

// dialError gives a next step for common transport failures without guessing their cause.
func dialError(host, address string, err error) error {
	var dns *net.DNSError
	var network net.Error
	switch {
	case errors.As(err, &dns) && dns.IsNotFound:
		return fmt.Errorf("cannot resolve %s (%s); check the host address in ~/.rmt/settings.toml", host, address)
	case errors.As(err, &network) && network.Timeout():
		return fmt.Errorf("timed out connecting to %s (%s); check the network and run 'rmt status' on that machine", host, address)
	case errors.Is(err, syscall.ECONNREFUSED):
		return fmt.Errorf("%s (%s) refused the connection; run 'rmt status' on that machine and check its listening address", host, address)
	default:
		return fmt.Errorf("cannot connect to %s: %w", host, err)
	}
}
