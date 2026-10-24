package controller

import (
	"github.com/prometheus/client_golang/prometheus"
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

func init() {
	metrics.Registry.MustRegister(mcpServerReady)
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
