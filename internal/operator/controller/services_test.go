package controller

import (
	"context"
	"errors"
	"net/http"
	"net/netip"
	"strings"
	"testing"
	"time"

	kavryntv1alpha1 "github.com/kavrynt/kavrynt/api/v1alpha1"
	"github.com/kavrynt/kavrynt/internal/egress"
	"github.com/kavrynt/kavrynt/internal/egress/egresstest"
	"github.com/kavrynt/kavrynt/internal/operator/discovery"
	"github.com/kavrynt/kavrynt/internal/scope"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func serviceClient(t *testing.T, funcs interceptor.Funcs, objects ...client.Object) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{clientgoscheme.AddToScheme, kavryntv1alpha1.AddToScheme} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}
	return fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&kavryntv1alpha1.MCPServer{}).
		WithObjects(objects...).
		WithInterceptorFuncs(funcs).
		Build()
}

func externalName(namespace, name, target string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name},
		Spec:       corev1.ServiceSpec{Type: corev1.ServiceTypeExternalName, ExternalName: target},
	}
}

func selectorService(namespace, name string, selector map[string]string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name},
		Spec:       corev1.ServiceSpec{Selector: selector},
	}
}

func endpointSlice(namespace, name, service string, managed bool, endpoints ...discoveryv1.Endpoint) *discoveryv1.EndpointSlice {
	labels := map[string]string{discoveryv1.LabelServiceName: service}
	if managed {
		labels[discoveryv1.LabelManagedBy] = scope.EndpointSliceController
	}
	return &discoveryv1.EndpointSlice{
		ObjectMeta:  metav1.ObjectMeta{Namespace: namespace, Name: name, Labels: labels},
		AddressType: discoveryv1.AddressTypeIPv4,
		Endpoints:   endpoints,
	}
}

func podEndpoint(namespace, address string) discoveryv1.Endpoint {
	endpoint := discoveryv1.Endpoint{Addresses: []string{address}}
	if namespace != "" {
		endpoint.TargetRef = &corev1.ObjectReference{Kind: "Pod", Namespace: namespace, Name: "pod"}
	}
	return endpoint
}

func scopedReconciler(k8sClient client.Client) *MCPServerReconciler {
	return &MCPServerReconciler{Client: k8sClient, Scheme: k8sClient.Scheme(), Scope: &scope.Policy{
		GatewayNamespace: "kavrynt-system",
		ClusterCIDRs:     []netip.Prefix{netip.MustParsePrefix("10.96.0.0/12"), netip.MustParsePrefix("10.244.0.0/16")},
		Services:         ServiceReader{Reader: k8sClient}.Service,
	}}
}

func serverAt(endpoint string) *kavryntv1alpha1.MCPServer {
	spec := validSpec()
	spec.Endpoint = endpoint
	return newServer(spec)
}

func readyOf(t *testing.T, k8sClient client.Client) *metav1.Condition {
	t.Helper()
	ready := apimeta.FindStatusCondition(get(t, k8sClient).Status.Conditions, kavryntv1alpha1.ConditionReady)
	if ready == nil {
		t.Fatal("no Ready condition")
	}
	return ready
}

// Verifies SPEC-0039-R01, SPEC-0039-R02, and SPEC-0039-R08: a server behind
// a Service of its own namespace that leads into another namespace, or to a
// name from outside the cluster, is not Ready; one behind its own pods is.
func TestServicesThatLeadElsewhereAreNotReady(t *testing.T) {
	for name, c := range map[string]struct {
		objects []client.Object
		want    string
	}{
		"ExternalName to another namespace": {
			[]client.Object{externalName("default", "search", "other.team-b.svc.cluster.local")},
			`namespace "team-b"`,
		},
		"ExternalName to an outside name": {
			[]client.Object{externalName("default", "search", "mcp.rebind.example.com")},
			"not a Service's full name",
		},
		"EndpointSlice naming another namespace's pod": {
			[]client.Object{selectorService("default", "search", nil), endpointSlice("default", "search-1", "search", false, podEndpoint("team-b", "10.244.2.9"))},
			`namespace "team-b"`,
		},
		"hand-written EndpointSlice with a cluster address": {
			[]client.Object{selectorService("default", "search", nil), endpointSlice("default", "search-1", "search", false, podEndpoint("", "10.244.2.9"))},
			"Kubernetes does not manage",
		},
		"EndpointSlice added to a Service with a selector": {
			[]client.Object{
				selectorService("default", "search", map[string]string{"app": "search"}),
				endpointSlice("default", "search-1", "search", true, podEndpoint("default", "10.244.1.5")),
				endpointSlice("default", "extra", "search", false, podEndpoint("default", "10.244.2.9")),
			},
			"Kubernetes does not manage",
		},
		"ExternalName to such a Service": {
			[]client.Object{
				externalName("default", "search", "manual.default.svc.cluster.local"),
				selectorService("default", "manual", nil),
				endpointSlice("default", "manual-1", "manual", false, podEndpoint("team-b", "10.244.2.9")),
			},
			`namespace "team-b"`,
		},
		"the namespace's own pods": {
			[]client.Object{
				selectorService("default", "search", map[string]string{"app": "search"}),
				endpointSlice("default", "search-1", "search", true, podEndpoint("", "10.244.1.5"), podEndpoint("default", "10.244.1.6")),
				endpointSlice("default", "other-1", "other", false, podEndpoint("team-b", "10.244.2.9")),
			},
			"",
		},
		"no Service yet": {nil, ""},
	} {
		t.Run(name, func(t *testing.T) {
			k8sClient := serviceClient(t, interceptor.Funcs{}, append(c.objects, serverAt("http://search.default.svc:8080/mcp"))...)
			reconcileWith(t, scopedReconciler(k8sClient))
			ready := readyOf(t, k8sClient)
			if c.want == "" {
				if ready.Status != metav1.ConditionTrue {
					t.Fatalf("Ready = %+v, want True", ready)
				}
				return
			}
			if ready.Status != metav1.ConditionFalse || ready.Reason != kavryntv1alpha1.ReasonEndpointNotAllowed || !strings.Contains(ready.Message, c.want) {
				t.Fatalf("Ready = %+v, want False with EndpointNotAllowed and %q", ready, c.want)
			}
			if strings.Contains(ready.Message, "10.") {
				t.Fatalf("the message names an address: %s", ready.Message)
			}
		})
	}
}

// Verifies SPEC-0039-R03: a change to a Service or EndpointSlice an
// endpoint leads through checks its servers again, a Service that appears
// later included, and every such server is checked again within 5 minutes.
func TestServiceChangesCheckTheServerAgain(t *testing.T) {
	k8sClient := serviceClient(t, interceptor.Funcs{},
		serverAt("http://search.default.svc:8080/mcp"),
		externalName("default", "search", "next.default.svc.cluster.local"))
	reconciler := scopedReconciler(k8sClient)
	result := reconcileWith(t, reconciler)
	if readyOf(t, k8sClient).Status != metav1.ConditionTrue {
		t.Fatal("a Service that leads to nothing yet is not Ready")
	}
	if result.RequeueAfter <= 0 || result.RequeueAfter > 5*time.Minute {
		t.Fatalf("RequeueAfter = %s, want a check within 5 minutes", result.RequeueAfter)
	}
	for _, service := range []scope.ServiceKey{{Namespace: "default", Name: "search"}, {Namespace: "default", Name: "next"}} {
		if requests := reconciler.serversThrough(service); len(requests) != 1 || requests[0].NamespacedName != key() {
			t.Fatalf("a change to %v checks %v, want the server", service, requests)
		}
	}
	if requests := reconciler.serversThrough(scope.ServiceKey{Namespace: "team-b", Name: "search"}); len(requests) != 0 {
		t.Fatalf("another namespace's Service checks %v", requests)
	}

	// The Service the chain ends at appears, with another namespace's pod.
	for _, object := range []client.Object{selectorService("default", "next", nil), endpointSlice("default", "next-1", "next", false, podEndpoint("team-b", "10.244.2.9"))} {
		if err := k8sClient.Create(context.Background(), object); err != nil {
			t.Fatal(err)
		}
	}
	reconcileWith(t, reconciler)
	if ready := readyOf(t, k8sClient); ready.Reason != kavryntv1alpha1.ReasonEndpointNotAllowed {
		t.Fatalf("Ready after the Service changed = %+v, want EndpointNotAllowed", ready)
	}
	if requests := reconciler.serversThrough(scope.ServiceKey{Namespace: "default", Name: "next"}); len(requests) != 1 {
		t.Fatal("a refused server is no longer checked when its Service changes")
	}

	// A deleted server is forgotten.
	if err := k8sClient.Delete(context.Background(), get(t, k8sClient)); err != nil {
		t.Fatal(err)
	}
	reconcileWith(t, reconciler)
	if requests := reconciler.serversThrough(scope.ServiceKey{Namespace: "default", Name: "search"}); len(requests) != 0 {
		t.Fatalf("a deleted server is still checked: %v", requests)
	}
}

// Verifies SPEC-0039-R04: a Service or its EndpointSlices that cannot be
// read keep the server not Ready, checked again after 30 s.
func TestUnreadableServiceIsRetried(t *testing.T) {
	failing := errors.New("services is forbidden")
	for name, funcs := range map[string]interceptor.Funcs{
		"the Service": {Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, object client.Object, opts ...client.GetOption) error {
			if _, ok := object.(*corev1.Service); ok {
				return failing
			}
			return c.Get(ctx, key, object, opts...)
		}},
		"its EndpointSlices": {List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			if _, ok := list.(*discoveryv1.EndpointSliceList); ok {
				return failing
			}
			return c.List(ctx, list, opts...)
		}},
	} {
		t.Run(name, func(t *testing.T) {
			k8sClient := serviceClient(t, funcs, serverAt("http://search.default.svc:8080/mcp"), selectorService("default", "search", map[string]string{"app": "search"}))
			result := reconcileWith(t, scopedReconciler(k8sClient))
			ready := readyOf(t, k8sClient)
			if ready.Status != metav1.ConditionFalse || ready.Reason != kavryntv1alpha1.ReasonEndpointNotAllowed || strings.Contains(ready.Message, "forbidden") {
				t.Fatalf("Ready = %+v, want False with EndpointNotAllowed and no API error", ready)
			}
			if result.RequeueAfter != scopeRetryDelay {
				t.Fatalf("RequeueAfter = %s, want %s", result.RequeueAfter, scopeRetryDelay)
			}
		})
	}
}

// dialingDiscoverer connects to the endpoint host with the context the
// reconciler gives discovery, as the Operator's transport does.
type dialingDiscoverer struct {
	cluster *egress.Cluster
	network *egresstest.Network
}

func (d dialingDiscoverer) ListTools(ctx context.Context, endpoint string, _ netip.AddrPort, _ http.Header) (discovery.Result, error) {
	host := strings.TrimPrefix(strings.TrimSuffix(endpoint, "/mcp"), "http://")
	_, err := d.cluster.DialContext(false, d.network.Dial)(ctx, "tcp", host)
	if errors.Is(err, egresstest.ErrNoListener) {
		return discovery.Result{}, nil
	}
	return discovery.Result{}, err
}

// Verifies SPEC-0039-R07: discovery tells the transport the server's
// namespace and endpoint host, so an outside name that resolves to a
// cluster address is refused, a Service name is resolved in the cluster's
// zone, and neither changes Ready.
func TestDiscoveryConnectsLikeTheGateway(t *testing.T) {
	cluster := &egress.Cluster{CIDRs: []netip.Prefix{netip.MustParsePrefix("10.96.0.0/12")}}
	for endpoint, want := range map[string]struct {
		dialed     string
		discovered bool
	}{
		"http://rebind.example.com:8080/mcp": {"rebind.example.com", false},
		"http://search.default:8080/mcp":     {"search.default.svc.cluster.local.", true},
	} {
		network := &egresstest.Network{Hosts: map[string]string{
			"rebind.example.com":                "10.96.0.10",
			"search.default":                    "10.96.0.99",
			"search.default.svc.cluster.local.": "10.96.0.10",
		}}
		k8sClient := serviceClient(t, interceptor.Funcs{}, serverAt(endpoint))
		reconciler := newDiscoveringReconciler(k8sClient, dialingDiscoverer{cluster, network}, &testClock{now: time.Unix(1000, 0)})
		reconciler.Cluster = cluster
		if _, err := reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: key()}); err != nil {
			t.Fatal(err)
		}
		server := get(t, k8sClient)
		if !apimeta.IsStatusConditionTrue(server.Status.Conditions, kavryntv1alpha1.ConditionReady) {
			t.Errorf("%s: discovery changed Ready", endpoint)
		}
		if got := apimeta.IsStatusConditionTrue(server.Status.Conditions, kavryntv1alpha1.ConditionToolsDiscovered); got != want.discovered {
			t.Errorf("%s: ToolsDiscovered = %t, want %t", endpoint, got, want.discovered)
		}
		if dialed := network.Dialed(); len(dialed) != 1 || dialed[0] != want.dialed {
			t.Errorf("%s: dialed %v, want %s", endpoint, dialed, want.dialed)
		}
	}
}
