// Package egress limits the addresses the Gateway and the Operator connect to
// for MCPServer endpoints. Endpoints come from anyone who can create an
// MCPServer, so they are untrusted: without this check an endpoint (or a DNS
// name that resolves later) could reach cloud instance metadata or the
// component's own loopback listeners.
//
// The check runs on the resolved address at dial time, so DNS rebinding and
// redirects cannot bypass it.
package egress

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"syscall"
	"time"
)

// metadataAddrs are cloud instance metadata services outside the link-local
// range, which is blocked as a whole.
var metadataAddrs = []netip.Addr{
	netip.MustParseAddr("fd00:ec2::254"),   // AWS IMDS over IPv6
	netip.MustParseAddr("100.100.100.200"), // Alibaba Cloud metadata
}

// Allowed reports whether an MCP endpoint may resolve to addr. Unspecified,
// link-local (which holds 169.254.169.254), multicast, and known metadata
// addresses are always refused; loopback only when allowLoopback is false.
func Allowed(addr netip.Addr, allowLoopback bool) bool {
	addr = addr.Unmap()
	switch {
	case !addr.IsValid(), addr.IsUnspecified(), addr.IsLinkLocalUnicast(), addr.IsMulticast():
		return false
	case addr.IsLoopback():
		return allowLoopback
	}
	for _, blocked := range metadataAddrs {
		if addr == blocked {
			return false
		}
	}
	return true
}

// Control is a net.Dialer Control function that refuses connections to
// addresses Allowed rejects.
func Control(allowLoopback bool) func(network, address string, _ syscall.RawConn) error {
	return func(_, address string, _ syscall.RawConn) error {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return err
		}
		addr, err := netip.ParseAddr(host)
		if err != nil {
			return fmt.Errorf("egress: unexpected dial address %q", address)
		}
		if !Allowed(addr, allowLoopback) {
			return fmt.Errorf("egress: connections to %s are not allowed for MCP endpoints", addr)
		}
		return nil
	}
}

// Transport returns a clone of http.DefaultTransport whose connections are
// checked with Control.
func Transport(allowLoopback bool) *http.Transport {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	dialer := &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
		Control:   Control(allowLoopback),
	}
	transport.DialContext = dialer.DialContext
	return transport
}
