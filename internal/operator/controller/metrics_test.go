package controller

import (
	"context"
	"testing"

	kavryntv1alpha1 "github.com/kavrynt/kavrynt/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

// readyMetric returns the kavrynt_operator_mcpserver_ready value of the test
// server, and whether the series exists.
func readyMetric(t *testing.T) (float64, bool) {
	t.Helper()
	families, err := metrics.Registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() != "kavrynt_operator_mcpserver_ready" {
			continue
		}
		for _, m := range family.GetMetric() {
			labels := map[string]string{}
			for _, l := range m.GetLabel() {
				labels[l.GetName()] = l.GetValue()
			}
			if labels["namespace"] == key().Namespace && labels["name"] == key().Name {
				return m.GetGauge().GetValue(), true
			}
		}
	}
	return 0, false
}

// Verifies SPEC-0010-R01.
func TestReadyMetricFollowsTheReadyCondition(t *testing.T) {
	server := newServer(kavryntv1alpha1.MCPServerSpec{Version: "0.1.0", Transport: "stdio", Command: "demo"})
	k8sClient := newClient(t, server)
	reconcile(t, k8sClient)
	if value, ok := readyMetric(t); !ok || value != 0 {
		t.Fatalf("ready metric for a server that is not Ready = %v (exists %v), want 0", value, ok)
	}

	updated := get(t, k8sClient)
	updated.Spec = validSpec()
	if err := k8sClient.Update(context.Background(), updated); err != nil {
		t.Fatal(err)
	}
	reconcile(t, k8sClient)
	if value, ok := readyMetric(t); !ok || value != 1 {
		t.Fatalf("ready metric for a Ready server = %v (exists %v), want 1", value, ok)
	}

	if err := k8sClient.Delete(context.Background(), get(t, k8sClient)); err != nil {
		t.Fatal(err)
	}
	reconcile(t, k8sClient)
	if value, ok := readyMetric(t); ok {
		t.Fatalf("ready metric still exported after the MCPServer was deleted: %v", value)
	}
}
