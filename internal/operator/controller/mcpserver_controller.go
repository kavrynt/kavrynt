package controller

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	kavryntv1alpha1 "github.com/kavrynt/kavrynt/api/v1alpha1"
	"github.com/kavrynt/kavrynt/internal/operator/discovery"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
)

const (
	conflictRetryDelay = time.Second

	// DefaultToolRefreshInterval is how often tools of a Ready server are
	// listed again.
	DefaultToolRefreshInterval = 5 * time.Minute
	// firstRetryDelay is the wait after the first failed discovery; it
	// doubles per failure up to the refresh interval.
	firstRetryDelay = 30 * time.Second
	// maxConcurrentReconciles keeps one slow or unreachable MCP server (up
	// to discovery.Timeout) from holding up the others.
	maxConcurrentReconciles = 4
)

// ToolDiscoverer lists an MCP server's tools.
type ToolDiscoverer interface {
	ListTools(ctx context.Context, endpoint string) (discovery.Result, error)
}

// MCPServerReconciler validates MCPServer resources and reports whether the
// Gateway can route to them. The Kubernetes API is the source of truth; the
// Gateway watches the same resources directly.
type MCPServerReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	// GatewayNamespaces lists the namespaces the Gateway watches. Empty means
	// all namespaces. Servers elsewhere are not Ready because the Gateway
	// never sees them.
	GatewayNamespaces []string

	// Discoverer lists the tools of Ready servers into status. Nil disables
	// tool discovery and clears status tools.
	Discoverer ToolDiscoverer
	// ToolRefreshInterval is how often tools are listed again. Zero means
	// DefaultToolRefreshInterval.
	ToolRefreshInterval time.Duration

	// now is the clock; tests replace it.
	now func() time.Time

	mu sync.Mutex
	// attempts holds the last discovery per server, so a status write that
	// fails or triggers another reconcile does not repeat discovery.
	attempts map[types.NamespacedName]*discoveryAttempt
}

// discoveryAttempt is the outcome of the last discovery for one server.
type discoveryAttempt struct {
	generation int64
	endpoint   string
	next       time.Time
	failures   int
	tools      []kavryntv1alpha1.MCPServerTool
	serverInfo *kavryntv1alpha1.MCPServerInfo
	condition  metav1.Condition
}

func (r *MCPServerReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var server kavryntv1alpha1.MCPServer
	if err := r.Get(ctx, req.NamespacedName, &server); err != nil {
		if apierrors.IsNotFound(err) {
			r.forget(req.NamespacedName)
		}
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !server.DeletionTimestamp.IsZero() {
		r.forget(req.NamespacedName)
		return ctrl.Result{}, nil
	}

	status := r.desiredStatus(&server)
	recordReady(req.NamespacedName, apimeta.IsStatusConditionTrue(status.Conditions, kavryntv1alpha1.ConditionReady))
	var result ctrl.Result
	if r.Discoverer != nil {
		result.RequeueAfter = r.applyTools(ctx, req.NamespacedName, &server, &status)
	} else {
		// Tools from an earlier run with discovery enabled would go stale.
		status.Tools = nil
		status.ServerInfo = nil
		apimeta.RemoveStatusCondition(&status.Conditions, kavryntv1alpha1.ConditionToolsDiscovered)
	}

	if statusUnchanged(server.Status, status) {
		return result, nil
	}
	server.Status = status
	if err := r.Status().Update(ctx, &server); err != nil {
		if apierrors.IsConflict(err) {
			// Another writer updated the object; retry on the newer version.
			return ctrl.Result{RequeueAfter: conflictRetryDelay}, nil
		}
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	return result, nil
}

// statusUnchanged reports whether desired matches current. The API server may
// re-encode input schemas, so they are compared canonically; otherwise an
// unchanged tool list would rewrite status on every reconcile.
func statusUnchanged(current, desired kavryntv1alpha1.MCPServerStatus) bool {
	current.Tools = discovery.CanonicalTools(current.Tools)
	return equality.Semantic.DeepEqual(current, desired)
}

// applyTools sets status tools and the ToolsDiscovered condition, listing
// tools when the last discovery is due for a refresh. It returns when to
// reconcile again; zero means not needed.
func (r *MCPServerReconciler) applyTools(ctx context.Context, key types.NamespacedName, server *kavryntv1alpha1.MCPServer, status *kavryntv1alpha1.MCPServerStatus) time.Duration {
	if !apimeta.IsStatusConditionTrue(status.Conditions, kavryntv1alpha1.ConditionReady) {
		r.forget(key)
		status.Tools = nil
		status.ServerInfo = nil
		apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
			Type:               kavryntv1alpha1.ConditionToolsDiscovered,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: server.Generation,
			Reason:             kavryntv1alpha1.ReasonNotRoutable,
			Message:            "Tools are discovered only for Ready servers",
		})
		return 0
	}

	now := r.clock()
	r.mu.Lock()
	attempt := r.attempts[key]
	r.mu.Unlock()
	if attempt == nil || attempt.generation != server.Generation || attempt.endpoint != server.Spec.Endpoint || !now.Before(attempt.next) {
		attempt = r.discover(ctx, server, attempt, now)
		r.mu.Lock()
		if r.attempts == nil {
			r.attempts = map[types.NamespacedName]*discoveryAttempt{}
		}
		r.attempts[key] = attempt
		r.mu.Unlock()
	}

	status.Tools = kavryntv1alpha1.CopyTools(attempt.tools)
	status.ServerInfo = kavryntv1alpha1.CopyServerInfo(attempt.serverInfo)
	condition := attempt.condition
	condition.ObservedGeneration = server.Generation
	apimeta.SetStatusCondition(&status.Conditions, condition)
	return max(attempt.next.Sub(now), time.Second)
}

// discover lists the server's tools and records its reported serverInfo. On
// failure it keeps the last known tools and serverInfo for the same endpoint
// (SPEC-0004-R02) and backs off.
func (r *MCPServerReconciler) discover(ctx context.Context, server *kavryntv1alpha1.MCPServer, previous *discoveryAttempt, now time.Time) *discoveryAttempt {
	refresh := r.ToolRefreshInterval
	if refresh <= 0 {
		refresh = DefaultToolRefreshInterval
	}
	attempt := &discoveryAttempt{generation: server.Generation, endpoint: server.Spec.Endpoint}

	result, err := r.Discoverer.ListTools(ctx, server.Spec.Endpoint)
	if err == nil {
		attempt.next = now.Add(refresh)
		attempt.tools = result.Tools
		attempt.serverInfo = result.ServerInfo
		attempt.condition = metav1.Condition{
			Type:    kavryntv1alpha1.ConditionToolsDiscovered,
			Status:  metav1.ConditionTrue,
			Reason:  kavryntv1alpha1.ReasonDiscovered,
			Message: result.Summary(),
		}
		return attempt
	}

	switch {
	case previous == nil:
		// After an Operator restart, status holds the last known tools.
		attempt.tools = discovery.CanonicalTools(server.Status.Tools)
		attempt.serverInfo = kavryntv1alpha1.CopyServerInfo(server.Status.ServerInfo)
	case previous.endpoint == server.Spec.Endpoint:
		attempt.tools = previous.tools
		attempt.serverInfo = previous.serverInfo
		attempt.failures = previous.failures
	}
	attempt.failures++
	delay := firstRetryDelay << min(attempt.failures-1, 10)
	attempt.next = now.Add(min(delay, refresh))
	attempt.condition = metav1.Condition{
		Type:    kavryntv1alpha1.ConditionToolsDiscovered,
		Status:  metav1.ConditionFalse,
		Reason:  kavryntv1alpha1.ReasonDiscoveryFailed,
		Message: err.Error(),
	}
	return attempt
}

func (r *MCPServerReconciler) forget(key types.NamespacedName) {
	r.mu.Lock()
	delete(r.attempts, key)
	r.mu.Unlock()
	forgetReady(key)
}

func (r *MCPServerReconciler) clock() time.Time {
	if r.now != nil {
		return r.now()
	}
	return time.Now()
}

func (r *MCPServerReconciler) desiredStatus(server *kavryntv1alpha1.MCPServer) kavryntv1alpha1.MCPServerStatus {
	status := kavryntv1alpha1.MCPServerStatus{
		ObservedGeneration: server.Generation,
		Conditions:         append([]metav1.Condition(nil), server.Status.Conditions...),
	}

	accepted := metav1.Condition{
		Type:               kavryntv1alpha1.ConditionAccepted,
		Status:             metav1.ConditionTrue,
		ObservedGeneration: server.Generation,
		Reason:             kavryntv1alpha1.ReasonValid,
		Message:            "MCPServer spec is valid",
	}
	ready := metav1.Condition{
		Type:               kavryntv1alpha1.ConditionReady,
		Status:             metav1.ConditionTrue,
		ObservedGeneration: server.Generation,
		Reason:             kavryntv1alpha1.ReasonRoutable,
		Message:            "Gateway routes /mcp/" + server.RouteName(),
	}

	switch err := server.Validate(); {
	case err != nil:
		accepted.Status = metav1.ConditionFalse
		accepted.Reason = kavryntv1alpha1.ReasonInvalidSpec
		accepted.Message = err.Error()
		ready.Status = metav1.ConditionFalse
		ready.Reason = kavryntv1alpha1.ReasonInvalidSpec
		ready.Message = "MCPServer spec is invalid"
	case server.Spec.Transport != "http":
		ready.Status = metav1.ConditionFalse
		ready.Reason = kavryntv1alpha1.ReasonUnsupportedTransport
		ready.Message = "Gateway routes only the http transport"
	case len(r.GatewayNamespaces) > 0 && !slices.Contains(r.GatewayNamespaces, server.Namespace):
		ready.Status = metav1.ConditionFalse
		ready.Reason = kavryntv1alpha1.ReasonNamespaceNotWatched
		ready.Message = fmt.Sprintf("Gateway does not watch namespace %q; add it to global.watchNamespaces", server.Namespace)
	}

	apimeta.SetStatusCondition(&status.Conditions, accepted)
	apimeta.SetStatusCondition(&status.Conditions, ready)
	return status
}

func (r *MCPServerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&kavryntv1alpha1.MCPServer{}).
		WithOptions(controller.Options{MaxConcurrentReconciles: maxConcurrentReconciles}).
		Complete(r)
}
