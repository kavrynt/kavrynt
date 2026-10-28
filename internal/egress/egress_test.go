package egress

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
)

func TestAllowed(t *testing.T) {
	tests := []struct {
		addr          string
		allowLoopback bool
		want          bool
	}{
		{addr: "10.96.0.15", want: true},
		{addr: "192.168.1.20", want: true},
		{addr: "2001:db8::1", want: true},
		{addr: "169.254.169.254", want: false},
		{addr: "169.254.170.2", want: false},
		{addr: "fe80::1", want: false},
		{addr: "fd00:ec2::254", want: false},
		{addr: "100.100.100.200", want: false},
		{addr: "0.0.0.0", want: false},
		{addr: "::", want: false},
		{addr: "224.0.0.1", want: false},
		{addr: "::ffff:169.254.169.254", want: false},
		{addr: "127.0.0.1", want: false},
		{addr: "::1", want: false},
		{addr: "127.0.0.1", allowLoopback: true, want: true},
		{addr: "::1", allowLoopback: true, want: true},
	}
	for _, tt := range tests {
		if got := Allowed(netip.MustParseAddr(tt.addr), tt.allowLoopback); got != tt.want {
			t.Errorf("Allowed(%s, allowLoopback=%v) = %v, want %v", tt.addr, tt.allowLoopback, got, tt.want)
		}
	}
}

func TestTransportRefusesLoopbackAtDialTime(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer server.Close()

	// A host name is resolved before the check, so it cannot hide the address.
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(server.URL, "http://"))
	for _, target := range []string{server.URL, "http://localhost:" + port} {
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, target, nil)
		_, err := (&http.Client{Transport: Transport(false)}).Do(req)
		if err == nil || !strings.Contains(err.Error(), "not allowed") {
			t.Fatalf("GET %s error = %v, want refused", target, err)
		}
	}

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL, nil)
	resp, err := (&http.Client{Transport: Transport(true)}).Do(req)
	if err != nil {
		t.Fatalf("GET with loopback allowed: %v", err)
	}
	_ = resp.Body.Close()
}

// Verifies SPEC-0027-R07: a refusal names the kind of address, so discovery
// can say why without the address itself.
func TestRefusalsNameTheAddressKind(t *testing.T) {
	control := Control(false)
	for addr, kind := range map[string]string{
		"169.254.169.254:80":    "cloud metadata",
		"[fd00:ec2::254]:80":    "cloud metadata",
		"100.100.100.200:80":    "cloud metadata",
		"169.254.170.2:80":      "link-local",
		"[fe80::1]:80":          "link-local",
		"127.0.0.1:80":          "loopback",
		"[::1]:80":              "loopback",
		"0.0.0.0:80":            "unspecified",
		"224.0.0.1:80":          "multicast",
		"[::ffff:127.0.0.1]:80": "loopback",
	} {
		var refused *RefusedError
		if err := control("tcp", addr, nil); !errors.As(err, &refused) || refused.Kind != kind {
			t.Errorf("Control(%s) = %v, want a RefusedError of kind %q", addr, err, kind)
		}
	}
	if err := control("tcp", "10.96.0.15:80", nil); err != nil {
		t.Errorf("Control(10.96.0.15) = %v, want allowed", err)
	}

	// The kind survives the HTTP client's wrapping.
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://127.0.0.1:1/", nil)
	_, err := (&http.Client{Transport: Transport(false)}).Do(req)
	var refused *RefusedError
	if !errors.As(err, &refused) || refused.Kind != "loopback" {
		t.Fatalf("GET loopback error = %v, want a RefusedError of kind loopback", err)
	}
}

// The check runs on the address the component dials. Through an HTTP proxy
// that is the proxy's address, and the proxy would reach any endpoint, so
// the transport never uses HTTP_PROXY, HTTPS_PROXY, or ALL_PROXY.
func TestTransportIgnoresProxyEnvironment(t *testing.T) {
	for _, allowLoopback := range []bool{false, true} {
		if Transport(allowLoopback).Proxy != nil {
			t.Fatalf("Transport(%v) uses a proxy, which would bypass the dial-time check", allowLoopback)
		}
	}
}
