package mcpsession

import (
	"context"

	kavryntv1alpha1 "github.com/kavrynt/kavrynt/api/v1alpha1"
	discoveryv1 "k8s.io/api/discovery/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Endpoints lists the ready pods of a Service from its EndpointSlices
// (SPEC-0026). The Gateway reads them from its informer cache, which holds
// the EndpointSlices of the namespaces it watches; the Operator reads them
// from the API server when it discovers tools.
type Endpoints struct {
	Reader client.Reader
	// AllowLoopback lets pods at loopback addresses through, like the
	// egress policy (local development only).
	AllowLoopback bool
}

// Ready returns the ready pods of service.
func (e Endpoints) Ready(ctx context.Context, service kavryntv1alpha1.ServiceRef) ([]Endpoint, error) {
	var slices discoveryv1.EndpointSliceList
	if err := e.Reader.List(ctx, &slices, client.InNamespace(service.Namespace), client.MatchingLabels{ServiceNameLabel: service.Name}); err != nil {
		return nil, err
	}
	return Ready(slices.Items, service.Port, e.AllowLoopback), nil
}
