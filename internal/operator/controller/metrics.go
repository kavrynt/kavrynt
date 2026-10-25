package controller

import (
	kavryntv1alpha1 "github.com/kavrynt/kavrynt/api/v1alpha1"
	"github.com/prometheus/client_golang/prometheus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

// mcpServerReady exports each MCPServer's Ready condition so alerts can name
// the servers that are not routable (SPEC-0010-R01). Only the leader
// reconciles, so only the leader exports series.
var mcpServerReady = prometheus.NewGaugeVec(prometheus.GaugeOpts{
	Name: "kavrynt_operator_mcpserver_ready",
	Help: "1 when the MCPServer's Ready condition is True, otherwise 0.",
}, []string{"namespace", "name"})

// mcpServerToolsDiscovered exports whether the Operator could list each
// MCPServer's tools, so an alert can name Ready servers whose discovery fails
// (SPEC-0021-R01). Servers whose tools are not listed by design export no
// series.
var mcpServerToolsDiscovered = prometheus.NewGaugeVec(prometheus.GaugeOpts{
	Name: "kavrynt_operator_mcpserver_tools_discovered",
	Help: "1 when the MCPServer's ToolsDiscovered condition is True, 0 when discovery failed.",
}, []string{"namespace", "name"})

func init() {
	metrics.Registry.MustRegister(mcpServerReady, mcpServerToolsDiscovered)
}

func recordReady(key types.NamespacedName, ready bool) {
	value := 0.0
	if ready {
		value = 1
	}
	mcpServerReady.WithLabelValues(key.Namespace, key.Name).Set(value)
}

func forgetReady(key types.NamespacedName) {
	mcpServerReady.DeleteLabelValues(key.Namespace, key.Name)
}

// recordToolsDiscovered exports the ToolsDiscovered condition; nil means the
// condition is absent because discovery is off. Only failed discoveries
// export 0: servers that are not Ready (NotRoutable) or whose tools need a
// caller's token (CallerTokenRequired) are not listed by design.
func recordToolsDiscovered(key types.NamespacedName, condition *metav1.Condition) {
	switch {
	case condition == nil:
		forgetToolsDiscovered(key)
	case condition.Status == metav1.ConditionTrue:
		mcpServerToolsDiscovered.WithLabelValues(key.Namespace, key.Name).Set(1)
	case condition.Reason == kavryntv1alpha1.ReasonDiscoveryFailed, condition.Reason == kavryntv1alpha1.ReasonCredentialUnavailable:
		mcpServerToolsDiscovered.WithLabelValues(key.Namespace, key.Name).Set(0)
	default:
		forgetToolsDiscovered(key)
	}
}

func forgetToolsDiscovered(key types.NamespacedName) {
	mcpServerToolsDiscovered.DeleteLabelValues(key.Namespace, key.Name)
}
