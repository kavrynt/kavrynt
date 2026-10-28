package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	kavryntv1alpha1 "github.com/kavrynt/kavrynt/api/v1alpha1"
	"github.com/kavrynt/kavrynt/internal/egress"
	"github.com/kavrynt/kavrynt/internal/mcpsession"
	"github.com/kavrynt/kavrynt/internal/operator/discovery"
	"github.com/kavrynt/kavrynt/internal/upstreamcred"
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

func TestReconcileReportsNamespacesTheGatewayDoesNotWatch(t *testing.T) {
	tests := []struct {
		name       string
		namespaces []string
		wantReady  metav1.ConditionStatus
		wantReason string
	}{
		{name: "watched namespace", namespaces: []string{"team-a", "default"}, wantReady: metav1.ConditionTrue, wantReason: kavryntv1alpha1.ReasonRoutable},
		{name: "unwatched namespace", namespaces: []string{"team-a"}, wantReady: metav1.ConditionFalse, wantReason: kavryntv1alpha1.ReasonNamespaceNotWatched},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			k8sClient := newClient(t, newServer(validSpec()))
			reconciler := &MCPServerReconciler{Client: k8sClient, Scheme: k8sClient.Scheme(), GatewayNamespaces: tt.namespaces}
			if _, err := reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: key()}); err != nil {
				t.Fatal(err)
			}

			updated := get(t, k8sClient)
			accepted := apimeta.FindStatusCondition(updated.Status.Conditions, kavryntv1alpha1.ConditionAccepted)
			if accepted == nil || accepted.Status != metav1.ConditionTrue {
				t.Fatalf("Accepted = %+v, want True", accepted)
			}
			ready := apimeta.FindStatusCondition(updated.Status.Conditions, kavryntv1alpha1.ConditionReady)
			if ready == nil || ready.Status != tt.wantReady || ready.Reason != tt.wantReason {
				t.Fatalf("Ready = %+v, want %s/%s", ready, tt.wantReady, tt.wantReason)
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

func TestReconcileIgnoresMissingServer(t *testing.T) {
	k8sClient := newClient(t)
	reconcile(t, k8sClient)
}

// fakeDiscoverer returns its configured tools or error and counts calls.
type fakeDiscoverer struct {
	tools      []kavryntv1alpha1.MCPServerTool
	serverInfo *kavryntv1alpha1.MCPServerInfo
	err        error
	calls      int
	header     http.Header
	pod        netip.AddrPort
}

func (f *fakeDiscoverer) ListTools(_ context.Context, _ string, pod netip.AddrPort, header http.Header) (discovery.Result, error) {
	f.header = header
	f.pod = pod
	f.calls++
	if f.err != nil {
		return discovery.Result{}, f.err
	}
	return discovery.Result{Tools: kavryntv1alpha1.CopyTools(f.tools), ServerInfo: kavryntv1alpha1.CopyServerInfo(f.serverInfo)}, nil
}

func echoTool() kavryntv1alpha1.MCPServerTool {
	return kavryntv1alpha1.MCPServerTool{
		Name:        "echo",
		Description: "Echo a message.",
		InputSchema: &runtime.RawExtension{Raw: []byte(`{"properties":{"message":{"type":"string"}},"type":"object"}`)},
	}
}

// testClock is a settable clock for refresh and backoff tests.
type testClock struct{ now time.Time }

func (c *testClock) Now() time.Time { return c.now }

func newDiscoveringReconciler(k8sClient client.Client, discoverer ToolDiscoverer, clock *testClock) *MCPServerReconciler {
	return &MCPServerReconciler{Client: k8sClient, Scheme: k8sClient.Scheme(), Discoverer: discoverer, now: clock.Now}
}

func reconcileWith(t *testing.T, reconciler *MCPServerReconciler) ctrl.Result {
	t.Helper()
	result, err := reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: key()})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestReconcileDiscoversToolsOfReadyServers(t *testing.T) {
	k8sClient := newClient(t, newServer(validSpec()))
	discoverer := &fakeDiscoverer{tools: []kavryntv1alpha1.MCPServerTool{echoTool()}}
	clock := &testClock{now: time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC)}
	reconciler := newDiscoveringReconciler(k8sClient, discoverer, clock)

	result := reconcileWith(t, reconciler)
	if result.RequeueAfter != DefaultToolRefreshInterval {
		t.Fatalf("RequeueAfter = %s, want %s", result.RequeueAfter, DefaultToolRefreshInterval)
	}
	first := get(t, k8sClient)
	if len(first.Status.Tools) != 1 || first.Status.Tools[0].Name != "echo" {
		t.Fatalf("tools = %+v", first.Status.Tools)
	}
	discovered := apimeta.FindStatusCondition(first.Status.Conditions, kavryntv1alpha1.ConditionToolsDiscovered)
	if discovered == nil || discovered.Status != metav1.ConditionTrue || discovered.Reason != kavryntv1alpha1.ReasonDiscovered || discovered.Message != "Discovered 1 tools" {
		t.Fatalf("ToolsDiscovered = %+v", discovered)
	}

	// The status write triggers another reconcile: no new discovery, no write.
	reconcileWith(t, reconciler)
	second := get(t, k8sClient)
	if discoverer.calls != 1 {
		t.Fatalf("discoverer called %d times before the refresh interval, want 1", discoverer.calls)
	}
	if first.ResourceVersion != second.ResourceVersion {
		t.Fatalf("unchanged tools rewrote status: %s -> %s", first.ResourceVersion, second.ResourceVersion)
	}

	clock.now = clock.now.Add(DefaultToolRefreshInterval)
	discoverer.tools = append(discoverer.tools, kavryntv1alpha1.MCPServerTool{Name: "search"})
	reconcileWith(t, reconciler)
	if discoverer.calls != 2 {
		t.Fatalf("discoverer called %d times after the refresh interval, want 2", discoverer.calls)
	}
	if tools := get(t, k8sClient).Status.Tools; len(tools) != 2 {
		t.Fatalf("tools after refresh = %+v", tools)
	}
}

// The fake client re-encodes stored JSON, so this checks the comparison
// directly with the formatting a real API server might return.
func TestStatusUnchangedComparesSchemasCanonically(t *testing.T) {
	desired := kavryntv1alpha1.MCPServerStatus{ObservedGeneration: 1, Tools: []kavryntv1alpha1.MCPServerTool{echoTool()}}
	current := kavryntv1alpha1.MCPServerStatus{ObservedGeneration: 1, Tools: []kavryntv1alpha1.MCPServerTool{echoTool()}}
	current.Tools[0].InputSchema = &runtime.RawExtension{Raw: []byte(`{ "type": "object", "properties": { "message": { "type": "string" } } }`)}

	if !statusUnchanged(current, desired) {
		t.Fatal("re-encoded schema reported as a change")
	}
	if string(current.Tools[0].InputSchema.Raw) == string(desired.Tools[0].InputSchema.Raw) {
		t.Fatal("statusUnchanged modified its argument")
	}
	desired.Tools[0].InputSchema = &runtime.RawExtension{Raw: []byte(`{"type":"object"}`)}
	if statusUnchanged(current, desired) {
		t.Fatal("different schema reported as unchanged")
	}
}

func TestReconcileKeepsServerReadyWhenDiscoveryFails(t *testing.T) {
	server := newServer(validSpec())
	server.Status.Tools = []kavryntv1alpha1.MCPServerTool{echoTool()}
	k8sClient := newClient(t, server)
	discoverer := &fakeDiscoverer{err: errors.New("tools/list returned HTTP 401")}
	clock := &testClock{now: time.Now()}
	reconciler := newDiscoveringReconciler(k8sClient, discoverer, clock)

	result := reconcileWith(t, reconciler)
	if result.RequeueAfter != firstRetryDelay {
		t.Fatalf("RequeueAfter = %s, want %s", result.RequeueAfter, firstRetryDelay)
	}
	updated := get(t, k8sClient)
	if !apimeta.IsStatusConditionTrue(updated.Status.Conditions, kavryntv1alpha1.ConditionReady) {
		t.Fatal("discovery failure changed Ready")
	}
	discovered := apimeta.FindStatusCondition(updated.Status.Conditions, kavryntv1alpha1.ConditionToolsDiscovered)
	if discovered == nil || discovered.Status != metav1.ConditionFalse || discovered.Reason != kavryntv1alpha1.ReasonDiscoveryFailed || discovered.Message != "tools/list returned HTTP 401" {
		t.Fatalf("ToolsDiscovered = %+v", discovered)
	}
	if len(updated.Status.Tools) != 1 {
		t.Fatalf("last known tools dropped on failure: %+v", updated.Status.Tools)
	}

	clock.now = clock.now.Add(firstRetryDelay)
	if result := reconcileWith(t, reconciler); result.RequeueAfter != 2*firstRetryDelay {
		t.Fatalf("second RequeueAfter = %s, want %s", result.RequeueAfter, 2*firstRetryDelay)
	}
}

func TestReconcileRediscoversOnSpecChange(t *testing.T) {
	k8sClient := newClient(t, newServer(validSpec()))
	discoverer := &fakeDiscoverer{tools: []kavryntv1alpha1.MCPServerTool{echoTool()}}
	reconciler := newDiscoveringReconciler(k8sClient, discoverer, &testClock{now: time.Now()})
	reconcileWith(t, reconciler)

	server := get(t, k8sClient)
	server.Spec.Endpoint = "http://other.default.svc:8080/mcp"
	server.Generation++
	if err := k8sClient.Update(context.Background(), server); err != nil {
		t.Fatal(err)
	}
	reconcileWith(t, reconciler)
	if discoverer.calls != 2 {
		t.Fatalf("discoverer called %d times, want 2 after a spec change", discoverer.calls)
	}
}

func TestReconcileClearsToolsWhenDiscoveryIsDisabled(t *testing.T) {
	server := newServer(validSpec())
	server.Status.Tools = []kavryntv1alpha1.MCPServerTool{echoTool()}
	server.Status.Conditions = []metav1.Condition{{
		Type: kavryntv1alpha1.ConditionToolsDiscovered, Status: metav1.ConditionTrue, Reason: kavryntv1alpha1.ReasonDiscovered, LastTransitionTime: metav1.Now(),
	}}
	k8sClient := newClient(t, server)
	reconcile(t, k8sClient)

	updated := get(t, k8sClient)
	if len(updated.Status.Tools) != 0 {
		t.Fatalf("tools = %+v, want none with discovery disabled", updated.Status.Tools)
	}
	if apimeta.FindStatusCondition(updated.Status.Conditions, kavryntv1alpha1.ConditionToolsDiscovered) != nil {
		t.Fatal("ToolsDiscovered kept with discovery disabled")
	}
}

func TestReconcileSkipsDiscoveryForServersThatAreNotReady(t *testing.T) {
	server := newServer(kavryntv1alpha1.MCPServerSpec{Version: "0.1.0", Transport: "stdio", Command: "demo"})
	server.Status.Tools = []kavryntv1alpha1.MCPServerTool{echoTool()}
	k8sClient := newClient(t, server)
	discoverer := &fakeDiscoverer{}
	reconciler := newDiscoveringReconciler(k8sClient, discoverer, &testClock{now: time.Now()})

	if result := reconcileWith(t, reconciler); result.RequeueAfter != 0 {
		t.Fatalf("RequeueAfter = %s, want 0", result.RequeueAfter)
	}
	if discoverer.calls != 0 {
		t.Fatal("discovered tools of a server that is not Ready")
	}
	updated := get(t, k8sClient)
	if len(updated.Status.Tools) != 0 {
		t.Fatalf("tools = %+v, want none", updated.Status.Tools)
	}
	discovered := apimeta.FindStatusCondition(updated.Status.Conditions, kavryntv1alpha1.ConditionToolsDiscovered)
	if discovered == nil || discovered.Status != metav1.ConditionFalse || discovered.Reason != kavryntv1alpha1.ReasonNotRoutable {
		t.Fatalf("ToolsDiscovered = %+v", discovered)
	}
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

// Verifies SPEC-0004-R02.
func TestReconcileRecordsKeepsAndClearsServerInfo(t *testing.T) {
	k8sClient := newClient(t, newServer(validSpec()))
	reported := &kavryntv1alpha1.MCPServerInfo{Name: "demo", Version: "0.2.0"}
	discoverer := &fakeDiscoverer{tools: []kavryntv1alpha1.MCPServerTool{echoTool()}, serverInfo: reported}
	clock := &testClock{now: time.Date(2026, 10, 23, 9, 0, 0, 0, time.UTC)}
	reconciler := newDiscoveringReconciler(k8sClient, discoverer, clock)

	reconcileWith(t, reconciler)
	if info := get(t, k8sClient).Status.ServerInfo; info == nil || *info != *reported {
		t.Fatalf("serverInfo after discovery = %+v, want %+v", info, reported)
	}

	// A failed discovery for the same endpoint keeps it and leaves Ready alone.
	clock.now = clock.now.Add(DefaultToolRefreshInterval)
	discoverer.err = errors.New("initialize: connection refused")
	reconcileWith(t, reconciler)
	failed := get(t, k8sClient)
	if info := failed.Status.ServerInfo; info == nil || *info != *reported {
		t.Fatalf("serverInfo after a failed discovery = %+v, want it kept", info)
	}
	if !apimeta.IsStatusConditionTrue(failed.Status.Conditions, kavryntv1alpha1.ConditionReady) {
		t.Fatal("keeping serverInfo changed Ready")
	}

	// After an Operator restart, the last value comes from status.
	restarted := newDiscoveringReconciler(k8sClient, discoverer, clock)
	reconcileWith(t, restarted)
	if info := get(t, k8sClient).Status.ServerInfo; info == nil || *info != *reported {
		t.Fatalf("serverInfo after a restart and a failed discovery = %+v, want it kept", info)
	}

	// A new endpoint is a different server: nothing until it answers.
	server := get(t, k8sClient)
	server.Spec.Endpoint = "http://other.default.svc:8080/mcp"
	server.Generation++
	if err := k8sClient.Update(context.Background(), server); err != nil {
		t.Fatal(err)
	}
	reconcileWith(t, restarted)
	if info := get(t, k8sClient).Status.ServerInfo; info != nil {
		t.Fatalf("serverInfo after an endpoint change and a failed discovery = %+v, want none", info)
	}

	// A server that is not Ready has none.
	discoverer.err = nil
	clock.now = clock.now.Add(DefaultToolRefreshInterval)
	reconcileWith(t, restarted)
	if get(t, k8sClient).Status.ServerInfo == nil {
		t.Fatal("serverInfo not recorded after discovery succeeded again")
	}
	server = get(t, k8sClient)
	server.Spec.Transport = "stdio"
	server.Spec.Endpoint = ""
	server.Spec.Command = "demo-server"
	server.Generation++
	if err := k8sClient.Update(context.Background(), server); err != nil {
		t.Fatal(err)
	}
	reconcileWith(t, restarted)
	if info := get(t, k8sClient).Status.ServerInfo; info != nil {
		t.Fatalf("serverInfo of a server that is not Ready = %+v, want none", info)
	}
}

// Verifies SPEC-0011-R08 and SPEC-0011-R09: discovery sends the stored
// credential; without it ToolsDiscovered is False with
// CredentialUnavailable, Ready stays True, and the value is never in status.
func TestDiscoveryUsesTheStoredCredential(t *testing.T) {
	spec := validSpec()
	spec.Credential = &kavryntv1alpha1.MCPServerCredential{Scheme: "Bearer"}
	k8sClient := newClient(t, newServer(spec))
	discoverer := &fakeDiscoverer{tools: []kavryntv1alpha1.MCPServerTool{echoTool()}}
	clock := &testClock{now: time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC)}
	reconciler := newDiscoveringReconciler(k8sClient, discoverer, clock)
	dir := t.TempDir()
	reconciler.Credentials = upstreamcred.Store{Dir: dir}

	reconcileWith(t, reconciler)
	server := get(t, k8sClient)
	discovered := apimeta.FindStatusCondition(server.Status.Conditions, kavryntv1alpha1.ConditionToolsDiscovered)
	if discovered == nil || discovered.Status != metav1.ConditionFalse || discovered.Reason != kavryntv1alpha1.ReasonCredentialUnavailable {
		t.Fatalf("ToolsDiscovered = %+v, want False/CredentialUnavailable", discovered)
	}
	if !strings.Contains(discovered.Message, "default.demo-mcp") {
		t.Fatalf("message %q does not name the missing key", discovered.Message)
	}
	if !apimeta.IsStatusConditionTrue(server.Status.Conditions, kavryntv1alpha1.ConditionReady) {
		t.Fatal("a missing credential changed Ready")
	}
	if discoverer.calls != 0 {
		t.Fatal("discovery ran without the credential")
	}

	for name, content := range map[string]string{"default.demo-mcp": "stored-secret", "default.demo-mcp.origin": "http://demo.default.svc:8080"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	clock.now = clock.now.Add(DefaultToolRefreshInterval)
	reconcileWith(t, reconciler)
	if got := discoverer.header.Get("Authorization"); got != "Bearer stored-secret" {
		t.Fatalf("discovery Authorization = %q", got)
	}
	server = get(t, k8sClient)
	if !apimeta.IsStatusConditionTrue(server.Status.Conditions, kavryntv1alpha1.ConditionToolsDiscovered) {
		t.Fatalf("conditions = %+v", server.Status.Conditions)
	}
	raw, _ := json.Marshal(server)
	if strings.Contains(string(raw), "stored-secret") {
		t.Fatal("the credential reached the MCPServer")
	}
}

// Verifies SPEC-0015-R10: a server that gets per-caller tokens is not
// discovered; ToolsDiscovered says why and Ready is unaffected.
func TestDiscoverySkipsTokenExchangeServers(t *testing.T) {
	spec := validSpec()
	spec.TokenExchange = &kavryntv1alpha1.MCPServerTokenExchange{Audience: "https://tickets.example.com"}
	k8sClient := newClient(t, newServer(spec))
	discoverer := &fakeDiscoverer{tools: []kavryntv1alpha1.MCPServerTool{echoTool()}}
	clock := &testClock{now: time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC)}
	reconciler := newDiscoveringReconciler(k8sClient, discoverer, clock)

	reconcileWith(t, reconciler)
	clock.now = clock.now.Add(DefaultToolRefreshInterval)
	reconcileWith(t, reconciler)
	server := get(t, k8sClient)
	discovered := apimeta.FindStatusCondition(server.Status.Conditions, kavryntv1alpha1.ConditionToolsDiscovered)
	if discovered == nil || discovered.Status != metav1.ConditionFalse || discovered.Reason != kavryntv1alpha1.ReasonCallerTokenRequired {
		t.Fatalf("ToolsDiscovered = %+v, want False/CallerTokenRequired", discovered)
	}
	if !apimeta.IsStatusConditionTrue(server.Status.Conditions, kavryntv1alpha1.ConditionReady) {
		t.Fatal("token exchange changed Ready")
	}
	if discoverer.calls != 0 {
		t.Fatalf("discovery ran %d times for a server that needs a caller's token", discoverer.calls)
	}
}

// Verifies SPEC-0027-R03: whatever the cause, a failed discovery keeps the
// reason DiscoveryFailed and leaves Ready alone.
func TestEveryDiscoveryFailureKeepsReasonAndReady(t *testing.T) {
	for name, err := range map[string]error{
		"refused by egress": fmt.Errorf("initialize: %w", &egress.RefusedError{Kind: "cloud metadata", Addr: netip.MustParseAddr("169.254.169.254")}),
		"host not found":    &net.DNSError{Err: "no such host", Name: "demo.default.svc", IsNotFound: true},
		"timeout":           fmt.Errorf("initialize: %w", context.DeadlineExceeded),
		"HTTP 404":          errors.New("initialize returned HTTP 404"),
	} {
		k8sClient := newClient(t, newServer(validSpec()))
		reconciler := newDiscoveringReconciler(k8sClient, &fakeDiscoverer{err: err}, &testClock{now: time.Now()})
		reconcileWith(t, reconciler)
		updated := get(t, k8sClient)
		discovered := apimeta.FindStatusCondition(updated.Status.Conditions, kavryntv1alpha1.ConditionToolsDiscovered)
		if discovered == nil || discovered.Reason != kavryntv1alpha1.ReasonDiscoveryFailed {
			t.Errorf("%s: ToolsDiscovered = %+v, want reason DiscoveryFailed", name, discovered)
		}
		if !apimeta.IsStatusConditionTrue(updated.Status.Conditions, kavryntv1alpha1.ConditionReady) {
			t.Errorf("%s: discovery failure changed Ready", name)
		}
	}
}

// fakePods are the ready pods of the Service demo.default.
type fakePods []mcpsession.Endpoint

func (p fakePods) Ready(_ context.Context, service kavryntv1alpha1.ServiceRef) ([]mcpsession.Endpoint, error) {
	if service != (kavryntv1alpha1.ServiceRef{Namespace: "default", Name: "demo", Port: 8080}) {
		return nil, errors.New("unexpected service")
	}
	return p, nil
}

// Verifies SPEC-0026-R09: with MCPSession, discovery talks to one ready pod;
// without one it fails and leaves the server Ready.
func TestDiscoveryUsesOnePodWithSessionAffinity(t *testing.T) {
	spec := validSpec()
	spec.SessionAffinity = kavryntv1alpha1.SessionAffinityMCPSession
	k8sClient := newClient(t, newServer(spec))
	discoverer := &fakeDiscoverer{tools: []kavryntv1alpha1.MCPServerTool{echoTool()}}
	clock := &testClock{now: time.Now()}
	reconciler := newDiscoveringReconciler(k8sClient, discoverer, clock)
	pods := fakePods{
		{Tag: mcpsession.TagOf("uid-a"), Addr: netip.MustParseAddrPort("10.0.0.1:8080")},
		{Tag: mcpsession.TagOf("uid-b"), Addr: netip.MustParseAddrPort("10.0.0.2:8080")},
	}
	reconciler.SessionEndpoints = pods

	reconcileWith(t, reconciler)
	if discoverer.calls != 1 || discoverer.pod != pods[0].Addr {
		t.Fatalf("discovery went to %v after %d calls, want %v", discoverer.pod, discoverer.calls, pods[0].Addr)
	}

	reconciler.SessionEndpoints = fakePods{}
	clock.now = clock.now.Add(DefaultToolRefreshInterval)
	reconcileWith(t, reconciler)
	server := get(t, k8sClient)
	discovered := apimeta.FindStatusCondition(server.Status.Conditions, kavryntv1alpha1.ConditionToolsDiscovered)
	if discoverer.calls != 1 || discovered == nil || discovered.Reason != kavryntv1alpha1.ReasonDiscoveryFailed || !strings.Contains(discovered.Message, "no ready pod") {
		t.Fatalf("without ready pods: %d calls, ToolsDiscovered = %+v", discoverer.calls, discovered)
	}
	if !apimeta.IsStatusConditionTrue(server.Status.Conditions, kavryntv1alpha1.ConditionReady) {
		t.Fatal("a failed discovery made the server not Ready")
	}

	// Without affinity the endpoint is used as is.
	spec.SessionAffinity = ""
	plain := &fakeDiscoverer{}
	reconciler = newDiscoveringReconciler(newClient(t, newServer(spec)), plain, clock)
	reconcileWith(t, reconciler)
	if plain.pod.IsValid() {
		t.Fatalf("discovery without affinity went to pod %v", plain.pod)
	}
}
