package mcpsession

import (
	"net/netip"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	"k8s.io/apimachinery/pkg/types"
)

func endpoint(address, pod string, uid types.UID, ready *bool) discoveryv1.Endpoint {
	e := discoveryv1.Endpoint{Addresses: []string{address}, Conditions: discoveryv1.EndpointConditions{Ready: ready}}
	if pod != "" {
		e.TargetRef = &corev1.ObjectReference{Kind: "Pod", Namespace: "team-a", Name: pod, UID: uid}
	}
	return e
}

func slice(addressType discoveryv1.AddressType, ports []int32, endpoints ...discoveryv1.Endpoint) discoveryv1.EndpointSlice {
	s := discoveryv1.EndpointSlice{AddressType: addressType, Endpoints: endpoints}
	for _, port := range ports {
		s.Ports = append(s.Ports, discoveryv1.EndpointPort{Port: new(port)})
	}
	return s
}

// Verifies SPEC-0026-R03: only ready pod endpoints, on the Service's port.
func TestReadySelectsReadyPodEndpoints(t *testing.T) {
	slices := []discoveryv1.EndpointSlice{
		slice(discoveryv1.AddressTypeIPv4, []int32{8080},
			endpoint("10.0.0.1", "search-a", "uid-a", nil),
			endpoint("10.0.0.2", "search-b", "uid-b", new(true)),
			endpoint("10.0.0.3", "search-c", "uid-c", new(false)),
			endpoint("10.0.0.4", "", "", nil)),
		// One pod listed twice (dual-stack) counts once.
		slice(discoveryv1.AddressTypeIPv6, []int32{8080}, endpoint("fd00::1", "search-a", "uid-a", nil)),
		// More than one port: the one with the endpoint's number.
		slice(discoveryv1.AddressTypeIPv4, []int32{9090, 3000}, endpoint("10.0.0.5", "search-d", "uid-d", nil)),
		slice(discoveryv1.AddressTypeIPv4, []int32{9090, 9091}, endpoint("10.0.0.6", "search-e", "uid-e", nil)),
		slice(discoveryv1.AddressTypeFQDN, []int32{3000}, endpoint("search.example.com", "search-f", "uid-f", nil)),
	}
	got := map[string]netip.AddrPort{}
	for _, e := range Ready(slices, 3000, false) {
		got[e.Tag] = e.Addr
	}
	want := map[string]netip.AddrPort{
		TagOf("uid-d"): netip.MustParseAddrPort("10.0.0.5:3000"),
	}
	for _, uid := range []types.UID{"uid-a", "uid-b"} {
		if _, ok := got[TagOf(uid)]; !ok {
			t.Errorf("ready pod %s missing from %v", uid, got)
		}
		delete(got, TagOf(uid))
	}
	if len(got) != len(want) || got[TagOf("uid-d")] != want[TagOf("uid-d")] {
		t.Fatalf("other endpoints = %v, want %v", got, want)
	}
	if ready := Ready(slices, 3000, false); ready[0].Tag > ready[1].Tag || ready[1].Tag > ready[2].Tag {
		t.Fatalf("endpoints are not sorted by tag: %v", ready)
	}
}

// Verifies SPEC-0026-R08.
func TestReadySkipsAddressesEgressRefuses(t *testing.T) {
	slices := []discoveryv1.EndpointSlice{slice(discoveryv1.AddressTypeIPv4, []int32{8080},
		endpoint("169.254.169.254", "metadata", "uid-m", nil),
		endpoint("127.0.0.1", "loopback", "uid-l", nil),
		endpoint("10.0.0.1", "search", "uid-s", nil))}
	if ready := Ready(slices, 8080, false); len(ready) != 1 || ready[0].Tag != TagOf("uid-s") {
		t.Fatalf("ready = %v, want only the pod at 10.0.0.1", ready)
	}
	if ready := Ready(slices, 8080, true); len(ready) != 2 {
		t.Fatalf("ready with loopback allowed = %v, want loopback and 10.0.0.1", ready)
	}
}

// Verifies SPEC-0026-R06.
func TestTagsNameNoPod(t *testing.T) {
	tag := TagOf("6c1f0c1e-2c2b-4f0e-9d8a-0a1b2c3d4e5f")
	if len(tag) != 16 || strings.Trim(tag, "0123456789abcdef") != "" {
		t.Fatalf("tag %q is not 16 lowercase hex characters", tag)
	}
	if tag != TagOf("6c1f0c1e-2c2b-4f0e-9d8a-0a1b2c3d4e5f") || tag == TagOf("another-uid") {
		t.Fatal("tags must be stable per pod and differ between pods")
	}
	ready := Ready([]discoveryv1.EndpointSlice{slice(discoveryv1.AddressTypeIPv4, []int32{8080}, endpoint("10.0.0.1", "search-7d9f", "uid-s", nil))}, 8080, false)
	if wrapped := Wrap(ready[0].Tag, "server-session"); strings.Contains(wrapped, "10.0.0.1") || strings.Contains(wrapped, "search-7d9f") || strings.Contains(wrapped, "uid-s") {
		t.Fatalf("session ID %q reveals the pod", wrapped)
	}
}

// Verifies SPEC-0026-R06 and SPEC-0026-R07: the server's session ID comes
// back unchanged, and anything that is not a wrapped ID is refused.
func TestWrapAndUnwrap(t *testing.T) {
	tag := TagOf("uid-a")
	for _, id := range []string{"abc", "a.b.c", "0123456789abcdef.nested"} {
		gotTag, gotID, ok := Unwrap(Wrap(tag, id))
		if !ok || gotTag != tag || gotID != id {
			t.Errorf("Unwrap(Wrap(%q)) = %q, %q, %v", id, gotTag, gotID, ok)
		}
	}
	for _, id := range []string{"", "abc", tag, tag + ".", "0123456789ABCDEF.abc", "0123456789abcde.abc", "0123456789abcdefa.abc", "z123456789abcdef.abc"} {
		if _, _, ok := Unwrap(id); ok {
			t.Errorf("Unwrap(%q) accepted", id)
		}
	}
}
