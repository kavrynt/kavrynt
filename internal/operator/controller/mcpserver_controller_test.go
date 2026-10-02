package controller

import (
	"context"
	"testing"

	kavryntv1alpha1 "github.com/kavrynt/kavrynt/api/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestReconcileSetsConditions(t *testing.T) {
	tests := []struct {
		name          string
		spec          kavryntv1alpha1.MCPServerSpec
		wantAccepted  metav1.ConditionStatus
		wantReady     metav1.ConditionStatus
		wantReadyWhy  string
		wantReadyText string
	}{
		{
			name:          "http server is routable",
			spec:          kavryntv1alpha1.MCPServerSpec{Version: "0.1.0", Transport: "http", Endpoint: "http://demo.default.svc:8080/mcp"},
			wantAccepted:  metav1.ConditionTrue,
			wantReady:     metav1.ConditionTrue,
			wantReadyWhy:  kavryntv1alpha1.ReasonRoutable,
			wantReadyText: "/mcp/default.demo-mcp",
		},
		{
			name:         "stdio server is accepted but not routable",
			spec:         kavryntv1alpha1.MCPServerSpec{Version: "0.1.0", Transport: "stdio", Command: "demo"},
			wantAccepted: metav1.ConditionTrue,
			wantReady:    metav1.ConditionFalse,
			wantReadyWhy: kavryntv1alpha1.ReasonUnsupportedTransport,
		},
		{
			name:         "invalid endpoint is rejected",
			spec:         kavryntv1alpha1.MCPServerSpec{Version: "0.1.0", Transport: "http", Endpoint: "file:///etc/passwd"},
			wantAccepted: metav1.ConditionFalse,
			wantReady:    metav1.ConditionFalse,
			wantReadyWhy: kavryntv1alpha1.ReasonInvalidSpec,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			k8sClient := newClient(t, newServer(tt.spec))
			reconcile(t, k8sClient)

			updated := get(t, k8sClient)
			if updated.Status.ObservedGeneration != updated.Generation {
				t.Fatalf("observedGeneration = %d, want %d", updated.Status.ObservedGeneration, updated.Generation)
			}
			accepted := apimeta.FindStatusCondition(updated.Status.Conditions, kavryntv1alpha1.ConditionAccepted)
			if accepted == nil || accepted.Status != tt.wantAccepted {
				t.Fatalf("Accepted = %+v, want %s", accepted, tt.wantAccepted)
			}
			ready := apimeta.FindStatusCondition(updated.Status.Conditions, kavryntv1alpha1.ConditionReady)
			if ready == nil || ready.Status != tt.wantReady || ready.Reason != tt.wantReadyWhy {
				t.Fatalf("Ready = %+v, want %s/%s", ready, tt.wantReady, tt.wantReadyWhy)
			}
			if tt.wantReadyText != "" && ready.Message != "Gateway routes "+tt.wantReadyText {
				t.Fatalf("Ready message = %q", ready.Message)
			}
		})
	}
}

func TestReconcileIsIdempotent(t *testing.T) {
	k8sClient := newClient(t, newServer(validSpec()))
	reconcile(t, k8sClient)
	first := get(t, k8sClient)

	reconcile(t, k8sClient)
	second := get(t, k8sClient)
	if first.ResourceVersion != second.ResourceVersion {
		t.Fatalf("second reconcile wrote the object: %s -> %s", first.ResourceVersion, second.ResourceVersion)
	}
}

func TestReconcileMigratesLegacyRegistryState(t *testing.T) {
	server := newServer(validSpec())
	server.Finalizers = []string{kavryntv1alpha1.LegacyRegistryFinalizer}
	server.Status.Conditions = []metav1.Condition{{
		Type:               "Registered",
		Status:             metav1.ConditionTrue,
		Reason:             "RegistrySynced",
		Message:            "MCPServer is synced to Kavrynt Registry",
		LastTransitionTime: metav1.Now(),
	}}
	k8sClient := newClient(t, server)

	reconcile(t, k8sClient)

	updated := get(t, k8sClient)
	if len(updated.Finalizers) != 0 {
		t.Fatalf("finalizers = %v, want none", updated.Finalizers)
	}
	if apimeta.FindStatusCondition(updated.Status.Conditions, "Registered") != nil {
		t.Fatal("legacy Registered condition was not removed")
	}
	if !apimeta.IsStatusConditionTrue(updated.Status.Conditions, kavryntv1alpha1.ConditionReady) {
		t.Fatal("Ready condition is not True")
	}
}

func TestReconcileReleasesDeletionBlockedByLegacyFinalizer(t *testing.T) {
	server := newServer(validSpec())
	server.Finalizers = []string{kavryntv1alpha1.LegacyRegistryFinalizer}
	k8sClient := newClient(t, server)

	if err := k8sClient.Delete(context.Background(), get(t, k8sClient)); err != nil {
		t.Fatal(err)
	}
	reconcile(t, k8sClient)

	var gone kavryntv1alpha1.MCPServer
	err := k8sClient.Get(context.Background(), key(), &gone)
	if !apierrors.IsNotFound(err) {
		t.Fatalf("Get after delete = %v, want NotFound", err)
	}
}

func TestReconcileIgnoresMissingServer(t *testing.T) {
	k8sClient := newClient(t)
	reconcile(t, k8sClient)
}

func validSpec() kavryntv1alpha1.MCPServerSpec {
	return kavryntv1alpha1.MCPServerSpec{Version: "0.1.0", Transport: "http", Endpoint: "http://demo.default.svc:8080/mcp"}
}

func newServer(spec kavryntv1alpha1.MCPServerSpec) *kavryntv1alpha1.MCPServer {
	return &kavryntv1alpha1.MCPServer{
		TypeMeta: metav1.TypeMeta{
			APIVersion: kavryntv1alpha1.GroupVersion.String(),
			Kind:       kavryntv1alpha1.MCPServerKind,
		},
		ObjectMeta: metav1.ObjectMeta{Name: "demo-mcp", Namespace: "default", Generation: 1},
		Spec:       spec,
	}
}

func newClient(t *testing.T, objects ...client.Object) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := kavryntv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	return fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&kavryntv1alpha1.MCPServer{}).
		WithObjects(objects...).
		Build()
}

func reconcile(t *testing.T, k8sClient client.Client) {
	t.Helper()
	reconciler := &MCPServerReconciler{Client: k8sClient, Scheme: k8sClient.Scheme()}
	if _, err := reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: key()}); err != nil {
		t.Fatal(err)
	}
}

func get(t *testing.T, k8sClient client.Client) *kavryntv1alpha1.MCPServer {
	t.Helper()
	var server kavryntv1alpha1.MCPServer
	if err := k8sClient.Get(context.Background(), key(), &server); err != nil {
		t.Fatal(err)
	}
	return &server
}

func key() types.NamespacedName {
	return types.NamespacedName{Name: "demo-mcp", Namespace: "default"}
}
