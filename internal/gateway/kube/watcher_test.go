package kube

import (
	"testing"

	kavryntv1alpha1 "github.com/kavrynt/kavrynt/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestRoutesIncludesOnlyReadyHTTPServers(t *testing.T) {
	now := metav1.Now()
	servers := []kavryntv1alpha1.MCPServer{
		server("team-a", "ready", "http", "http://ready:8080", metav1.ConditionTrue),
		server("team-a", "not-ready", "http", "http://not-ready:8080", metav1.ConditionFalse),
		server("team-a", "no-status", "http", "http://no-status:8080", ""),
		server("team-a", "stdio", "stdio", "", metav1.ConditionTrue),
		server("team-a", "bad-endpoint", "http", "file:///etc/passwd", metav1.ConditionTrue),
		server("team-b", "deleting", "http", "http://deleting:8080", metav1.ConditionTrue),
	}
	servers[5].DeletionTimestamp = &now

	routes := Routes(servers)
	if len(routes) != 1 {
		t.Fatalf("routes = %+v, want only team-a.ready", routes)
	}
	if routes[0].Name != "team-a.ready" || routes[0].Endpoint != "http://ready:8080" || routes[0].Version != "1.0.0" {
		t.Fatalf("route = %+v", routes[0])
	}
}

func server(namespace, name, transport, endpoint string, ready metav1.ConditionStatus) kavryntv1alpha1.MCPServer {
	s := kavryntv1alpha1.MCPServer{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec:       kavryntv1alpha1.MCPServerSpec{Version: "1.0.0", Transport: transport, Endpoint: endpoint, Command: "cmd"},
	}
	if ready != "" {
		s.Status.Conditions = []metav1.Condition{{Type: kavryntv1alpha1.ConditionReady, Status: ready, Reason: "Test"}}
	}
	return s
}

// Verifies SPEC-0011-R05 and SPEC-0011-R07: a declared credential reaches the
// route and marks the server as needing one.
func TestRoutesCarryTheDeclaredCredential(t *testing.T) {
	item := server("team-a", "search", "http", "http://search.team-a.svc:8080", metav1.ConditionTrue)
	item.Spec.Credential = &kavryntv1alpha1.MCPServerCredential{Header: "X-API-Key"}
	routes := Routes([]kavryntv1alpha1.MCPServer{item})
	if len(routes) != 1 || routes[0].Credential == nil || routes[0].Credential.HeaderName() != "X-API-Key" {
		t.Fatalf("routes = %+v", routes)
	}
	servers := Servers([]kavryntv1alpha1.MCPServer{item})
	if servers[0].Credential == nil || !servers[0].Credential.Required {
		t.Fatalf("server credential = %+v", servers[0].Credential)
	}
	item.Spec.Credential = nil
	if Routes([]kavryntv1alpha1.MCPServer{item})[0].Credential != nil || Servers([]kavryntv1alpha1.MCPServer{item})[0].Credential != nil {
		t.Fatal("a server without spec.credential needs a credential")
	}
}
