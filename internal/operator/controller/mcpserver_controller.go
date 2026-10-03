package controller

import (
	"context"
	"time"

	kavryntv1alpha1 "github.com/kavrynt/kavrynt/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const conflictRetryDelay = time.Second

// MCPServerReconciler validates MCPServer resources and reports whether the
// Gateway can route to them. The Kubernetes API is the source of truth; the
// Gateway watches the same resources directly.
type MCPServerReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

func (r *MCPServerReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var server kavryntv1alpha1.MCPServer
	if err := r.Get(ctx, req.NamespacedName, &server); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !server.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	status := desiredStatus(&server)
	if equality.Semantic.DeepEqual(server.Status, status) {
		return ctrl.Result{}, nil
	}
	server.Status = status
	if err := r.Status().Update(ctx, &server); err != nil {
		if apierrors.IsConflict(err) {
			// Another writer updated the object; retry on the newer version.
			return ctrl.Result{RequeueAfter: conflictRetryDelay}, nil
		}
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	return ctrl.Result{}, nil
}

func desiredStatus(server *kavryntv1alpha1.MCPServer) kavryntv1alpha1.MCPServerStatus {
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
	}

	apimeta.SetStatusCondition(&status.Conditions, accepted)
	apimeta.SetStatusCondition(&status.Conditions, ready)
	return status
}

func (r *MCPServerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&kavryntv1alpha1.MCPServer{}).
		Complete(r)
}
