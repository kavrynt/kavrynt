package auth

import (
	"fmt"
	"net/http"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func from(remote string, forwarded ...string) func(*http.Request) {
	return func(r *http.Request) {
		r.RemoteAddr = remote
		for _, value := range forwarded {
			r.Header.Add("X-Forwarded-For", value)
		}
	}
}

// Verifies SPEC-0030-R02: behind a trusted proxy, failures count per client,
// taken as the right-most X-Forwarded-For address that is not a proxy.
func TestLoginLimitBehindTrustedProxies(t *testing.T) {
	a, _, logs, _ := newTestAuth(t)
	a.TrustProxies([]netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")})

	for i := 0; i < maxFailures; i++ {
		// The client prepends a forged address; the proxies append theirs.
		login(t, a, "admin", "guess", from("10.1.2.3:40000", "198.51.100.9, 203.0.113.5", "10.9.9.9"))
	}
	if rr := login(t, a, "admin", testPassword, from("10.1.2.3:40000", "203.0.113.5")); rr.Code != http.StatusTooManyRequests {
		t.Fatalf("client A after %d failures: %d, want 429", maxFailures, rr.Code)
	}
	if rr := login(t, a, "admin", testPassword, from("10.1.2.3:40000", "203.0.113.6")); rr.Code != http.StatusNoContent {
		t.Fatalf("client B behind the same proxy: %d, want 204", rr.Code)
	}
	// A forged left-most address does not move A's failures elsewhere.
	if rr := login(t, a, "admin", testPassword, from("10.1.2.3:40000", "192.0.2.1, 203.0.113.5")); rr.Code != http.StatusTooManyRequests {
		t.Fatalf("client A with another forged address: %d, want 429", rr.Code)
	}
	if want := "client=203.0.113.5"; !strings.Contains(logs.String(), want) {
		t.Fatalf("logs do not name the client behind the proxy (%s):\n%s", want, logs.String())
	}

	// A header with no valid untrusted address counts against the proxy.
	for i := 0; i < maxFailures; i++ {
		login(t, a, "admin", "guess", from("10.5.5.5:40000", "not-an-ip"))
	}
	if rr := login(t, a, "admin", testPassword, from("10.5.5.5:40000", "10.0.0.1")); rr.Code != http.StatusTooManyRequests {
		t.Fatalf("proxy after malformed headers: %d, want 429", rr.Code)
	}
}

// Verifies SPEC-0030-R01 and SPEC-0030-R04: without trusted proxies, or from
// an address outside them, X-Forwarded-For is ignored.
func TestForwardedForIsIgnoredFromUntrustedAddresses(t *testing.T) {
	for name, trusted := range map[string][]netip.Prefix{
		"no trusted proxies":  nil,
		"another proxy range": {netip.MustParsePrefix("192.168.0.0/16")},
	} {
		a, _, _, _ := newTestAuth(t)
		a.TrustProxies(trusted)
		for i := 0; i < maxFailures; i++ {
			login(t, a, "admin", "guess", from("10.1.2.3:40000", fmt.Sprintf("203.0.113.%d", i)))
		}
		if rr := login(t, a, "admin", testPassword, from("10.1.2.3:40000", "203.0.113.200")); rr.Code != http.StatusTooManyRequests {
			t.Errorf("%s: a new X-Forwarded-For escaped the limit: %d, want 429", name, rr.Code)
		}
	}
}

// Verifies SPEC-0030-R03: a full limiter forgets ended windows first, then
// the oldest clients that are not refused, never a refused one; with only
// refused clients left, new ones share one entry.
func TestLimiterNeverForgetsRefusedClients(t *testing.T) {
	a, c, _, _ := newTestAuth(t)
	refused := "198.51.100.1"
	for i := 0; i < maxFailures; i++ {
		a.fail(refused)
	}
	c.advance(time.Second)
	for i := 1; i < maxTrackedClients; i++ {
		a.fail(fmt.Sprintf("client-%d", i))
		c.advance(time.Millisecond)
	}
	if len(a.failures) != maxTrackedClients {
		t.Fatalf("tracking %d clients, want %d", len(a.failures), maxTrackedClients)
	}
	a.fail("one-more")
	if a.blocked(refused) == 0 {
		t.Fatal("a full limiter forgot a refused client")
	}
	if len(a.failures) > maxTrackedClients {
		t.Fatalf("tracking %d clients, limit %d", len(a.failures), maxTrackedClients)
	}
	if _, ok := a.failures["client-1"]; ok {
		t.Fatal("the oldest client that is not refused was kept")
	}

	// Ended windows go first, all of them.
	c.advance(failureWindow)
	for i := 0; i < maxFailures; i++ {
		a.fail(refused)
	}
	a.fail("after-the-window")
	if len(a.failures) != 2 {
		t.Fatalf("tracking %d clients after the window, want the refused one and the new one", len(a.failures))
	}

	// Only refused clients: new ones share the overflow entry and are
	// refused together after five failures.
	a.failures = map[string]*failures{}
	for i := 0; i < maxTrackedClients; i++ {
		a.failures[fmt.Sprintf("refused-%d", i)] = &failures{count: maxFailures, since: c.now()}
	}
	for i := 0; i < maxFailures; i++ {
		a.fail(fmt.Sprintf("new-%d", i))
	}
	if a.blocked("new-other") == 0 {
		t.Fatal("clients beyond a limiter full of refused clients are not limited")
	}
	if len(a.failures) > maxTrackedClients+1 {
		t.Fatalf("tracking %d entries, want at most %d and the overflow", len(a.failures), maxTrackedClients)
	}
}
