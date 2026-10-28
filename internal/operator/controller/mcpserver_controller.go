package controller

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"slices"
	"sync"
	"time"

	kavryntv1alpha1 "github.com/kavrynt/kavrynt/api/v1alpha1"
	"github.com/kavrynt/kavrynt/internal/mcpsession"
	"github.com/kavrynt/kavrynt/internal/operator/discovery"
	"github.com/kavrynt/kavrynt/internal/scope"
	"github.com/kavrynt/kavrynt/internal/upstreamcred"
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

// ToolDiscoverer lists an MCP server's tools, from one pod when pod is
// valid.
type ToolDiscoverer interface {
	ListTools(ctx context.Context, endpoint string, pod netip.AddrPort, header http.Header) (discovery.Result, error)
}

// SessionEndpoints lists the ready pods of a Service (SPEC-0026-R09).
type SessionEndpoints interface {
	Ready(ctx context.Context, service kavryntv1alpha1.ServiceRef) ([]mcpsession.Endpoint, error)
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

	// Credentials holds the stored credentials of servers that declare
	// spec.credential; discovery sends them like the Gateway (SPEC-0011-R08).
	Credentials upstreamcred.Store

	// SessionEndpoints gives the pods of servers with session affinity, so
	// a discovery session stays on one of them (SPEC-0026-R09).
	SessionEndpoints SessionEndpoints

	// Scope limits the endpoints and token exchange audiences a namespace
	// may use (SPEC-0029). Nil checks neither.
	Scope *scope.Policy

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

	status, retry := r.desiredStatus(ctx, &server)
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
	if retry > 0 && (result.RequeueAfter == 0 || retry < result.RequeueAfter) {
		result.RequeueAfter = retry
	}
	recordToolsDiscovered(req.NamespacedName, apimeta.FindStatusCondition(status.Conditions, kavryntv1alpha1.ConditionToolsDiscovered))

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
		r.forgetAttempt(key)
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

// sessionPod returns the pod a discovery session of a server with session
// affinity goes to: the first ready pod of its Service. The error names the
// Service, never a pod.
func (r *MCPServerReconciler) sessionPod(ctx context.Context, server *kavryntv1alpha1.MCPServer) (netip.AddrPort, error) {
	service, ok := kavryntv1alpha1.ServiceEndpoint(server.Spec.Endpoint)
	if !ok || r.SessionEndpoints == nil {
		return netip.AddrPort{}, errors.New("session affinity needs the endpoint's Service, which the Operator cannot read")
	}
	pods, err := r.SessionEndpoints.Ready(ctx, service)
	if err != nil {
		return netip.AddrPort{}, fmt.Errorf("list the pods of Service %s/%s: %w", service.Namespace, service.Name, err)
	}
	if len(pods) == 0 {
		return netip.AddrPort{}, fmt.Errorf("no ready pod of Service %s/%s to list tools from", service.Namespace, service.Name)
	}
	return pods[0].Addr, nil
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
	if server.Spec.TokenExchange != nil {
		// Its tools are listed with a caller's exchanged token, and the
		// Operator has no caller (SPEC-0015-R10).
		attempt.next = now.Add(refresh)
		attempt.condition = metav1.Condition{
			Type:    kavryntv1alpha1.ConditionToolsDiscovered,
			Status:  metav1.ConditionFalse,
			Reason:  kavryntv1alpha1.ReasonCallerTokenRequired,
			Message: "the server gets per-caller tokens by token exchange; the Operator has no caller token to list its tools with",
		}
		return attempt
	}

	var header http.Header
	reason := kavryntv1alpha1.ReasonDiscoveryFailed
	var result discovery.Result
	var err error
	if credential := server.Spec.Credential; credential != nil {
		var value string
		value, err = r.Credentials.Lookup(server.RouteName(), server.Spec.Endpoint)
		if err != nil {
			// The message names the Secret key, never the value.
			reason = kavryntv1alpha1.ReasonCredentialUnavailable
		} else {
			header = http.Header{}
			header.Set(credential.HeaderName(), credential.HeaderValue(value))
		}
	}
	var pod netip.AddrPort
	if err == nil && server.Spec.SessionAffinity == kavryntv1alpha1.SessionAffinityMCPSession {
		pod, err = r.sessionPod(ctx, server)
	}
	if err == nil {
		result, err = r.Discoverer.ListTools(ctx, server.Spec.Endpoint, pod, header)
	}
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
		Reason:  reason,
		Message: err.Error(),
	}
	return attempt
}

// forget drops everything kept for a deleted MCPServer, including its metrics.
func (r *MCPServerReconciler) forget(key types.NamespacedName) {
	r.forgetAttempt(key)
	forgetReady(key)
	forgetToolsDiscovered(key)
}

// forgetAttempt drops the last discovery of a server that still exists.
func (r *MCPServerReconciler) forgetAttempt(key types.NamespacedName) {
	r.mu.Lock()
	delete(r.attempts, key)
	r.mu.Unlock()
}

func (r *MCPServerReconciler) clock() time.Time {
	if r.now != nil {
		return r.now()
	}
	return time.Now()
}

// desiredStatus returns the status the server should have, and how soon to
// check it again when a check could not be completed.
func (r *MCPServerReconciler) desiredStatus(ctx context.Context, server *kavryntv1alpha1.MCPServer) (kavryntv1alpha1.MCPServerStatus, time.Duration) {
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
	var retry time.Duration
	if ready.Status == metav1.ConditionTrue && r.Scope != nil {
		retry = r.checkScope(ctx, server, &ready)
	}

	apimeta.SetStatusCondition(&status.Conditions, accepted)
	apimeta.SetStatusCondition(&status.Conditions, ready)
	return status, retry
}

// scopeRetryDelay is when an endpoint whose namespace could not be read is
// checked again; scopeRecheckInterval is when one allowed only because its
// namespace does not exist is, in case the namespace is created.
const (
	scopeRetryDelay      = 30 * time.Second
	scopeRecheckInterval = 5 * time.Minute
)

// checkScope makes ready False when the server's endpoint or token exchange
// audience is not allowed for its namespace (SPEC-0029-R04, R08). It
// returns when to check again when a namespace could not be read; the
// server stays not Ready until then.
func (r *MCPServerReconciler) checkScope(ctx context.Context, server *kavryntv1alpha1.MCPServer, ready *metav1.Condition) time.Duration {
	if err := r.Scope.Endpoint(ctx, server.Namespace, server.Spec.Endpoint); err != nil {
		ready.Status, ready.Reason, ready.Message = metav1.ConditionFalse, kavryntv1alpha1.ReasonEndpointNotAllowed, err.Error()
		if errors.Is(err, scope.ErrNamespaceUnknown) {
			return scopeRetryDelay
		}
		return 0
	}
	if exchange := server.Spec.TokenExchange; exchange != nil && !r.Scope.Audiences.Allowed(server.Namespace, exchange.Audience) {
		ready.Status, ready.Reason = metav1.ConditionFalse, kavryntv1alpha1.ReasonAudienceNotAllowed
		ready.Message = fmt.Sprintf("spec.tokenExchange.audience %q is not allowed for namespace %q; add it to global.tokenExchangeAudiences", exchange.Audience, server.Namespace)
	}
	if scope.DependsOnNamespace(server.Spec.Endpoint) {
		return scopeRecheckInterval
	}
	return 0
}

func (r *MCPServerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&kavryntv1alpha1.MCPServer{}).
		WithOptions(controller.Options{MaxConcurrentReconciles: maxConcurrentReconciles}).
		Complete(r)
}
