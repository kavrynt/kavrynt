package mcpsession

import (
	"context"
	"testing"

	kavryntv1alpha1 "github.com/kavrynt/kavrynt/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func endpointSliceOf(namespace, name, service, address string, uid string) *discoveryv1.EndpointSlice {
	return &discoveryv1.EndpointSlice{
		ObjectMeta:  metav1.ObjectMeta{Namespace: namespace, Name: name, Labels: map[string]string{ServiceNameLabel: service}},
		AddressType: discoveryv1.AddressTypeIPv4,
		Ports:       []discoveryv1.EndpointPort{{Port: new(int32(8080))}},
		Endpoints: []discoveryv1.Endpoint{{
			Addresses: []string{address},
			TargetRef: &corev1.ObjectReference{Kind: "Pod", UID: k8stypes.UID("uid-" + uid)},
		}},
	}
}

// Verifies SPEC-0026-R03: the ready pods of a route's Service come from that
// Service's EndpointSlices only.
func TestEndpointsListTheServicesReadyPods(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := discoveryv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	reader := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		endpointSliceOf("team-a", "search-abc", "search", "10.0.0.1", "a"),
		endpointSliceOf("team-a", "search-def", "search", "10.0.0.2", "b"),
		endpointSliceOf("team-a", "other-abc", "other", "10.0.0.3", "c"),
		endpointSliceOf("team-b", "search-abc", "search", "10.0.0.4", "d"),
	).Build()

	ready, err := Endpoints{Reader: reader}.Ready(context.Background(), kavryntv1alpha1.ServiceRef{Namespace: "team-a", Name: "search", Port: 8080})
	if err != nil {
		t.Fatal(err)
	}
	if len(ready) != 2 {
		t.Fatalf("ready = %v, want the two pods of team-a/search", ready)
	}
	for _, pod := range ready {
		if a := pod.Addr.Addr().String(); a != "10.0.0.1" && a != "10.0.0.2" {
			t.Fatalf("pod %v is not one of team-a/search", pod)
		}
	}
}
