package controller

import (
	"context"
	"time"

	kavryntv1alpha1 "github.com/kavrynt/k8s-operator/api/v1alpha1"
	"github.com/kavrynt/k8s-operator/internal/registry"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const FinalizerName = "mcpservers.kavrynt.io/registry-sync"

type MCPServerReconciler struct {
	client.Client
	Scheme         *runtime.Scheme
	RegistryClient *registry.Client
	RequeueAfter   time.Duration
}

func (r *MCPServerReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var server kavryntv1alpha1.MCPServer
	if err := r.Get(ctx, req.NamespacedName, &server); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if !server.ObjectMeta.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, &server)
	}

	if controllerutil.AddFinalizer(&server, FinalizerName) {
		if err := r.Update(ctx, &server); err != nil {
			return ctrl.Result{}, err
		}
	}

	manifest := manifestFromServer(&server)
	if err := r.RegistryClient.Upsert(ctx, manifest); err != nil {
		logger.Error(err, "failed to sync MCPServer to Registry", "name", server.Name, "namespace", server.Namespace)
		if statusErr := r.updateStatus(ctx, req.NamespacedName, false, err.Error()); statusErr != nil {
			return ctrl.Result{}, statusErr
		}
		return ctrl.Result{RequeueAfter: r.requeueAfter()}, nil
	}

	if err := r.updateStatus(ctx, req.NamespacedName, true, ""); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *MCPServerReconciler) reconcileDelete(ctx context.Context, server *kavryntv1alpha1.MCPServer) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(server, FinalizerName) {
		return ctrl.Result{}, nil
	}

	if err := r.RegistryClient.Delete(ctx, server.Name); err != nil {
		_ = r.updateStatus(ctx, types.NamespacedName{Name: server.Name, Namespace: server.Namespace}, false, err.Error())
		return ctrl.Result{RequeueAfter: r.requeueAfter()}, nil
	}

	controllerutil.RemoveFinalizer(server, FinalizerName)
	if err := r.Update(ctx, server); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (r *MCPServerReconciler) updateStatus(ctx context.Context, key types.NamespacedName, registered bool, message string) error {
	var latest kavryntv1alpha1.MCPServer
	if err := r.Get(ctx, key, &latest); err != nil {
		return err
	}

	now := metav1.Now()
	conditionStatus := metav1.ConditionFalse
	reason := "RegistrySyncFailed"
	if registered {
		conditionStatus = metav1.ConditionTrue
		reason = "RegistrySynced"
		message = "MCPServer is synced to Kavrynt Registry"
		latest.Status.RegistrySyncedAt = &now
		latest.Status.RegistryError = ""
	} else {
		latest.Status.RegistryError = message
	}
	latest.Status.ObservedGeneration = latest.Generation
	latest.Status.Conditions = []metav1.Condition{
		{
			Type:               kavryntv1alpha1.ConditionRegistered,
			Status:             conditionStatus,
			ObservedGeneration: latest.Generation,
			LastTransitionTime: now,
			Reason:             reason,
			Message:            message,
		},
	}

	return r.Status().Update(ctx, &latest)
}

func (r *MCPServerReconciler) requeueAfter() time.Duration {
	if r.RequeueAfter <= 0 {
		return 30 * time.Second
	}
	return r.RequeueAfter
}

func (r *MCPServerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&kavryntv1alpha1.MCPServer{}).
		Complete(r)
}

func manifestFromServer(server *kavryntv1alpha1.MCPServer) registry.Manifest {
	return registry.Manifest{
		APIVersion: registry.APIVersion,
		Kind:       registry.Kind,
		Metadata: registry.Metadata{
			Name:        server.Name,
			Description: server.Annotations["kavrynt.io/description"],
			Labels:      server.Labels,
		},
		Spec: registry.Spec{
			Version:     server.Spec.Version,
			Transport:   server.Spec.Transport,
			Command:     server.Spec.Command,
			Args:        server.Spec.Args,
			Endpoint:    server.Spec.Endpoint,
			Environment: server.Spec.Environment,
		},
	}
}
