package kube

import (
	"encoding/json"
	"strings"
	"testing"

	kavryntv1alpha1 "github.com/kavrynt/kavrynt/api/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
)

func TestServersLeavesOutSecretsAndKeepsTools(t *testing.T) {
	ready := server("team-a", "ready", "http", "http://ready:8080/mcp?api_key=query-secret", "True")
	ready.Annotations = map[string]string{kavryntv1alpha1.DescriptionAnnotation: "Payments tools"}
	ready.Spec.Args = []string{"--token=args-secret"}
	ready.Spec.Environment = map[string]string{"API_KEY": "env-secret"}
	ready.Status.Tools = []kavryntv1alpha1.MCPServerTool{{
		Name:        "refund",
		Description: "Refund an order",
		InputSchema: &runtime.RawExtension{Raw: []byte(`{"type":"object"}`)},
	}}
	stdio := server("team-a", "local", "stdio", "", "False")

	servers := Servers([]kavryntv1alpha1.MCPServer{stdio, ready})
	if len(servers) != 2 || servers[0].Name != "local" || servers[1].Name != "ready" {
		t.Fatalf("servers = %+v, want local then ready", servers)
	}
	if servers[0].Routed || !servers[1].Routed {
		t.Fatalf("routed = %v, %v; want false, true", servers[0].Routed, servers[1].Routed)
	}
	got := servers[1]
	if got.Route != "team-a.ready" || got.Description != "Payments tools" || got.Endpoint != "http://ready:8080/mcp" {
		t.Fatalf("server = %+v", got)
	}
	if len(got.Tools) != 1 || got.Tools[0].Name != "refund" || string(got.Tools[0].InputSchema) != `{"type":"object"}` {
		t.Fatalf("tools = %+v", got.Tools)
	}

	encoded, err := json.Marshal(servers)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"query-secret", "args-secret", "env-secret", "API_KEY"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("server view contains %q: %s", secret, encoded)
		}
	}
}
