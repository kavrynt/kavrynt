package main

import (
	"strings"
	"testing"
)

// Verifies SPEC-0029-R04: the Operator does not start without the Gateway's
// namespace, which single-label endpoint hosts resolve in.
func TestEndpointScopeNeedsTheGatewayNamespace(t *testing.T) {
	for _, namespace := range []string{"", "Not A Namespace"} {
		if _, err := endpointScope(namespace, "", "", "", nil); err == nil || !strings.Contains(err.Error(), "--gateway-namespace") {
			t.Errorf("gateway namespace %q: err = %v", namespace, err)
		}
	}
	policy, err := endpointScope("kavrynt-system", "team-a:team-b", "10.96.0.0/12", `{"*":["urn:x"]}`, nil)
	if err != nil || policy.GatewayNamespace != "kavrynt-system" || len(policy.CrossNamespace) != 1 || len(policy.ClusterCIDRs) != 1 || !policy.Audiences.Allowed("any", "urn:x") {
		t.Fatalf("endpointScope = %+v, %v", policy, err)
	}
}
