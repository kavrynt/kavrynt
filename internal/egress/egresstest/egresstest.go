// Package egresstest resolves host names for tests of egress.Cluster
// without a network.
package egresstest

import (
	"context"
	"errors"
	"net"
	"sync"
)

// ErrNoListener is returned for a connection the egress policy allowed when
// the Network has no Listener.
var ErrNoListener = errors.New("egresstest: allowed, and nothing listens")

// Network is a DNS zone and one listener. Dial resolves the dialed name in
// Hosts exactly as it was dialed (a trailing dot included), asks the
// dialer's Control about the resolved address, as the kernel-facing dialer
// would, and then connects to Listener whatever the address was.
type Network struct {
	// Hosts maps dialed names to the addresses they resolve to.
	Hosts map[string]string
	// Listener is the address every allowed connection reaches.
	Listener string

	mu     sync.Mutex
	dialed []string
}

// Dial is an egress.DialFunc.
func (n *Network) Dial(ctx context.Context, dialer *net.Dialer, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	n.mu.Lock()
	n.dialed = append(n.dialed, host)
	n.mu.Unlock()
	ip := host
	if net.ParseIP(host) == nil {
		resolved, ok := n.Hosts[host]
		if !ok {
			return nil, &net.OpError{Op: "dial", Net: network, Err: &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}}
		}
		ip = resolved
	}
	if dialer.Control != nil {
		if err := dialer.Control(network, net.JoinHostPort(ip, port), nil); err != nil {
			return nil, &net.OpError{Op: "dial", Net: network, Err: err}
		}
	}
	if n.Listener == "" {
		return nil, ErrNoListener
	}
	return (&net.Dialer{}).DialContext(ctx, network, n.Listener)
}

// Dialed returns the names and addresses dialed so far, in order.
func (n *Network) Dialed() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]string(nil), n.dialed...)
}
