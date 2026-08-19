package controller

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	kavryntv1alpha1 "github.com/kavrynt/k8s-operator/api/v1alpha1"
	"github.com/kavrynt/k8s-operator/internal/registry"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestReconcileRegistersMCPServer(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := kavryntv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	server := &kavryntv1alpha1.MCPServer{
		TypeMeta: metav1.TypeMeta{
			APIVersion: kavryntv1alpha1.GroupVersion.String(),
			Kind:       kavryntv1alpha1.MCPServerKind,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "demo-mcp",
			Namespace: "default",
			Annotations: map[string]string{
				"kavrynt.io/description": "demo server",
			},
			Labels: map[string]string{
				"app": "demo",
			},
		},
		Spec: kavryntv1alpha1.MCPServerSpec{
			Version:   "0.1.0",
			Transport: "http",
			Endpoint:  "http://demo.default.svc.cluster.local:8080/mcp",
		},
	}

	var got registry.Manifest
	registryClient, err := registry.NewClient("http://registry.test", &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.Method != http.MethodPost {
				t.Fatalf("method = %s, want POST", req.Method)
			}
			if err := json.NewDecoder(req.Body).Decode(&got); err != nil {
				t.Fatal(err)
			}
			return &http.Response{
				StatusCode: http.StatusCreated,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{}`)),
			}, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}

	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&kavryntv1alpha1.MCPServer{}).
		WithObjects(server).
		Build()

	reconciler := &MCPServerReconciler{
		Client:         k8sClient,
		Scheme:         scheme,
		RegistryClient: registryClient,
		RequeueAfter:   time.Millisecond,
	}

	_, err = reconciler.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "demo-mcp", Namespace: "default"},
	})
	if err != nil {
		t.Fatal(err)
	}

	if got.Metadata.Name != "demo-mcp" {
		t.Fatalf("manifest name = %s, want demo-mcp", got.Metadata.Name)
	}
	if got.Metadata.Description != "demo server" {
		t.Fatalf("description = %s, want demo server", got.Metadata.Description)
	}
	if got.Spec.Endpoint != "http://demo.default.svc.cluster.local:8080/mcp" {
		t.Fatalf("endpoint = %s", got.Spec.Endpoint)
	}

	var updated kavryntv1alpha1.MCPServer
	if err := k8sClient.Get(context.Background(), types.NamespacedName{Name: "demo-mcp", Namespace: "default"}, &updated); err != nil {
		t.Fatal(err)
	}
	if !containsFinalizer(updated.Finalizers, FinalizerName) {
		t.Fatalf("missing finalizer: %#v", updated.Finalizers)
	}
	if updated.Status.ObservedGeneration != updated.Generation {
		t.Fatalf("observedGeneration = %d, want %d", updated.Status.ObservedGeneration, updated.Generation)
	}
	if updated.Status.RegistrySyncedAt == nil {
		t.Fatal("registrySyncedAt is nil")
	}
	if updated.Status.RegistryError != "" {
		t.Fatalf("registryError = %q, want empty", updated.Status.RegistryError)
	}
	condition := apimeta.FindStatusCondition(updated.Status.Conditions, kavryntv1alpha1.ConditionRegistered)
	if condition == nil {
		t.Fatalf("missing %s condition", kavryntv1alpha1.ConditionRegistered)
	}
	if condition.Status != metav1.ConditionTrue {
		t.Fatalf("condition status = %s, want True", condition.Status)
	}
}

func TestManifestFromServer(t *testing.T) {
	server := &kavryntv1alpha1.MCPServer{
		ObjectMeta: metav1.ObjectMeta{
			Name: "stdio-mcp",
		},
		Spec: kavryntv1alpha1.MCPServerSpec{
			Version:   "0.1.0",
			Transport: "stdio",
			Command:   "demo",
			Args:      []string{"--stdio"},
		},
	}

	manifest := manifestFromServer(server)
	if manifest.APIVersion != registry.APIVersion {
		t.Fatalf("apiVersion = %s", manifest.APIVersion)
	}
	if manifest.Kind != registry.Kind {
		t.Fatalf("kind = %s", manifest.Kind)
	}
	if manifest.Spec.Command != "demo" {
		t.Fatalf("command = %s", manifest.Spec.Command)
	}
}

func containsFinalizer(finalizers []string, want string) bool {
	for _, finalizer := range finalizers {
		if finalizer == want {
			return true
		}
	}
	return false
}
