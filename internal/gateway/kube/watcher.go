// Package kube keeps the Gateway route table in sync with MCPServer
// resources through a read-only informer cache.
package kube

import (
	"context"
	"errors"
	"log/slog"
	"time"

	kavryntv1alpha1 "github.com/kavrynt/kavrynt/api/v1alpha1"
	"github.com/kavrynt/kavrynt/internal/gateway/model"
	"github.com/kavrynt/kavrynt/internal/gateway/routing"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	toolscache "k8s.io/client-go/tools/cache"
	"sigs.k8s.io/controller-runtime/pkg/cache"
)

// NewCache returns an informer cache for MCPServer resources, limited to the
// given namespaces when any are set.
func NewCache(cfg *rest.Config, namespaces []string) (cache.Cache, error) {
	scheme := runtime.NewScheme()
	if err := kavryntv1alpha1.AddToScheme(scheme); err != nil {
		return nil, err
	}
	opts := cache.Options{Scheme: scheme}
	if len(namespaces) > 0 {
		opts.DefaultNamespaces = make(map[string]cache.Config, len(namespaces))
		for _, namespace := range namespaces {
			opts.DefaultNamespaces[namespace] = cache.Config{}
		}
	}
	return cache.New(cfg, opts)
}

// Watcher rebuilds the route table whenever an MCPServer changes.
type Watcher struct {
	Cache  cache.Cache
	Table  *routing.Table
	Logger *slog.Logger
}

// Run starts the cache and blocks until ctx is cancelled or the cache fails.
func (w *Watcher) Run(ctx context.Context) error {
	informer, err := w.Cache.GetInformer(ctx, &kavryntv1alpha1.MCPServer{})
	if err != nil {
		return err
	}

	changed := make(chan struct{}, 1)
	notify := func(any) {
		select {
		case changed <- struct{}{}:
		default:
		}
	}
	if _, err := informer.AddEventHandler(toolscache.ResourceEventHandlerFuncs{
		AddFunc:    notify,
		UpdateFunc: func(_, obj any) { notify(obj) },
		DeleteFunc: notify,
	}); err != nil {
		return err
	}

	errCh := make(chan error, 1)
	go func() { errCh <- w.Cache.Start(ctx) }()

	if !w.Cache.WaitForCacheSync(ctx) {
		select {
		case err := <-errCh:
			if err != nil {
				return err
			}
		default:
		}
		return errors.New("MCPServer cache did not sync")
	}
	w.sync(ctx)

	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-errCh:
			return err
		case <-changed:
			w.sync(ctx)
		}
	}
}

func (w *Watcher) sync(ctx context.Context) {
	var servers kavryntv1alpha1.MCPServerList
	if err := w.Cache.List(ctx, &servers); err != nil {
		w.Table.MarkSyncError(err)
		w.logger().Error("list MCPServers failed", "error", err)
		return
	}
	routes := Routes(servers.Items)
	w.Table.Replace(routes, time.Now())
	w.logger().Info("routes updated", "routes", len(routes))
}

func (w *Watcher) logger() *slog.Logger {
	if w.Logger != nil {
		return w.Logger
	}
	return slog.Default()
}

// Routes returns routes for servers the operator reports Ready that also
// pass local validation and use the http transport.
func Routes(servers []kavryntv1alpha1.MCPServer) []model.Route {
	routes := make([]model.Route, 0, len(servers))
	for i := range servers {
		server := &servers[i]
		if !server.DeletionTimestamp.IsZero() {
			continue
		}
		if !apimeta.IsStatusConditionTrue(server.Status.Conditions, kavryntv1alpha1.ConditionReady) {
			continue
		}
		if server.Spec.Transport != "http" || server.Validate() != nil {
			continue
		}
		routes = append(routes, model.Route{
			Name:      server.RouteName(),
			Version:   server.Spec.Version,
			Transport: server.Spec.Transport,
			Endpoint:  server.Spec.Endpoint,
		})
	}
	return routes
}
