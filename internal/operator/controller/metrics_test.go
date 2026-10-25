package controller

import (
	"context"
	"errors"
	"testing"
	"time"

	kavryntv1alpha1 "github.com/kavrynt/kavrynt/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

// readyMetric returns the kavrynt_operator_mcpserver_ready value of the test
// server, and whether the series exists.
func readyMetric(t *testing.T) (float64, bool) {
	t.Helper()
	return gaugeValue(t, "kavrynt_operator_mcpserver_ready")
}

// gaugeValue returns the value of the named gauge for the test server, and
// whether the series exists.
func gaugeValue(t *testing.T, name string) (float64, bool) {
	t.Helper()
	families, err := metrics.Registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() != name {
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

// Verifies SPEC-0010-R01.
func TestReadyMetricWithDiscoveryKeepsServersThatAreNotReady(t *testing.T) {
	k8sClient := newClient(t, newServer(kavryntv1alpha1.MCPServerSpec{Version: "0.1.0", Transport: "stdio", Command: "demo"}))
	clock := &testClock{now: time.Date(2026, 10, 25, 9, 0, 0, 0, time.UTC)}
	reconcileWith(t, newDiscoveringReconciler(k8sClient, &fakeDiscoverer{}, clock))
	if value, ok := readyMetric(t); !ok || value != 0 {
		t.Fatalf("ready metric for a server that is not Ready = %v (exists %v), want 0", value, ok)
	}
}

func toolsDiscoveredMetric(t *testing.T) (float64, bool) {
	t.Helper()
	return gaugeValue(t, "kavrynt_operator_mcpserver_tools_discovered")
}

// Verifies SPEC-0021-R01.
func TestToolsDiscoveredMetricFollowsDiscovery(t *testing.T) {
	k8sClient := newClient(t, newServer(validSpec()))
	discoverer := &fakeDiscoverer{tools: []kavryntv1alpha1.MCPServerTool{echoTool()}}
	clock := &testClock{now: time.Date(2026, 10, 25, 9, 0, 0, 0, time.UTC)}
	reconciler := newDiscoveringReconciler(k8sClient, discoverer, clock)
	reconcileWith(t, reconciler)
	if value, ok := toolsDiscoveredMetric(t); !ok || value != 1 {
		t.Fatalf("tools-discovered metric after a discovery = %v (exists %v), want 1", value, ok)
	}

	discoverer.err = errors.New("tools/list returned HTTP 500")
	clock.now = clock.now.Add(DefaultToolRefreshInterval)
	reconcileWith(t, reconciler)
	if value, ok := toolsDiscoveredMetric(t); !ok || value != 0 {
		t.Fatalf("tools-discovered metric after a failed discovery = %v (exists %v), want 0", value, ok)
	}

	if err := k8sClient.Delete(context.Background(), get(t, k8sClient)); err != nil {
		t.Fatal(err)
	}
	reconcileWith(t, reconciler)
	if value, ok := toolsDiscoveredMetric(t); ok {
		t.Fatalf("tools-discovered metric still exported after the MCPServer was deleted: %v", value)
	}
}

// Verifies SPEC-0021-R01.
func TestToolsDiscoveredMetricSkipsServersWithoutDiscovery(t *testing.T) {
	clock := &testClock{now: time.Date(2026, 10, 25, 9, 0, 0, 0, time.UTC)}
	tokenExchange := validSpec()
	tokenExchange.TokenExchange = &kavryntv1alpha1.MCPServerTokenExchange{Audience: "https://tickets.example.com"}
	cases := []struct {
		name      string
		spec      kavryntv1alpha1.MCPServerSpec
		discovery bool
	}{
		{name: "token exchange (CallerTokenRequired)", spec: tokenExchange, discovery: true},
		{name: "not Ready (NotRoutable)", spec: kavryntv1alpha1.MCPServerSpec{Version: "0.1.0", Transport: "stdio", Command: "demo"}, discovery: true},
		{name: "discovery off", spec: validSpec()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// A series left from an earlier discovery must go away.
			mcpServerToolsDiscovered.WithLabelValues(key().Namespace, key().Name).Set(1)
			k8sClient := newClient(t, newServer(tc.spec))
			if tc.discovery {
				reconcileWith(t, newDiscoveringReconciler(k8sClient, &fakeDiscoverer{}, clock))
			} else {
				reconcile(t, k8sClient)
			}
			if value, ok := toolsDiscoveredMetric(t); ok {
				t.Fatalf("tools-discovered metric = %v, want no series", value)
			}
		})
	}
}
