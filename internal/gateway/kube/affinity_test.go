package kube

import (
	"testing"

	kavryntv1alpha1 "github.com/kavrynt/kavrynt/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Verifies SPEC-0026-R01: routes carry the affinity, and the console view
// names it.
func TestRoutesCarrySessionAffinity(t *testing.T) {
	item := server("team-a", "search", "http", "http://search.team-a.svc:8080/mcp", metav1.ConditionTrue)
	item.Spec.SessionAffinity = kavryntv1alpha1.SessionAffinityMCPSession
	if routes := Routes([]kavryntv1alpha1.MCPServer{item}); len(routes) != 1 || !routes[0].SessionAffinity {
		t.Fatalf("routes = %+v", routes)
	}
	if got := Servers([]kavryntv1alpha1.MCPServer{item})[0].SessionAffinity; got != "MCPSession" {
		t.Fatalf("server sessionAffinity = %q", got)
	}
	item.Spec.SessionAffinity = ""
	if Routes([]kavryntv1alpha1.MCPServer{item})[0].SessionAffinity || Servers([]kavryntv1alpha1.MCPServer{item})[0].SessionAffinity != "None" {
		t.Fatal("a server without spec.sessionAffinity has affinity")
	}
}
