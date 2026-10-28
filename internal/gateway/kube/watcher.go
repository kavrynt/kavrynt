// Package kube keeps the Gateway route table in sync with MCPServer
// resources through a read-only informer cache.
package kube

import (
	"context"
	"errors"
	"log/slog"
	"net/url"
	"sort"
	"time"

	kavryntv1alpha1 "github.com/kavrynt/kavrynt/api/v1alpha1"
	"github.com/kavrynt/kavrynt/internal/gateway/model"
	"github.com/kavrynt/kavrynt/internal/gateway/routing"
	discoveryv1 "k8s.io/api/discovery/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	toolscache "k8s.io/client-go/tools/cache"
	"sigs.k8s.io/controller-runtime/pkg/cache"
)

// NewCache returns an informer cache for MCPServer resources and, for
// session affinity, EndpointSlices (SPEC-0026), limited to the given
// namespaces when any are set. Managed fields are dropped to save memory.
func NewCache(cfg *rest.Config, namespaces []string) (cache.Cache, error) {
	scheme := runtime.NewScheme()
	if err := kavryntv1alpha1.AddToScheme(scheme); err != nil {
		return nil, err
	}
	if err := discoveryv1.AddToScheme(scheme); err != nil {
		return nil, err
	}
	opts := cache.Options{Scheme: scheme, DefaultTransform: cache.TransformStripManagedFields()}
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
	// Started with the cache, so the Gateway is ready only once the pods
	// of MCPSession routes are known (SPEC-0026).
	if _, err := w.Cache.GetInformer(ctx, &discoveryv1.EndpointSlice{}); err != nil {
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
		return errors.New("MCPServer and EndpointSlice cache did not sync")
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
	w.Table.ReplaceServers(Servers(servers.Items))
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
		route := model.Route{
			Name:      server.RouteName(),
			Version:   server.Spec.Version,
			Transport: server.Spec.Transport,
			Endpoint:  server.Spec.Endpoint,
		}
		if server.Spec.Credential != nil {
			credential := *server.Spec.Credential
			route.Credential = &credential
		}
		route.TokenExchange = server.Spec.TokenExchange.Copy()
		route.SessionAffinity = server.Spec.SessionAffinity == kavryntv1alpha1.SessionAffinityMCPSession
		// Tools kept from before a failed discovery are not current
		// (SPEC-0028-R04).
		if apimeta.IsStatusConditionTrue(server.Status.Conditions, kavryntv1alpha1.ConditionToolsDiscovered) {
			route.Tools = make(map[string]map[string]bool, len(server.Status.Tools))
			for _, tool := range server.Status.Tools {
				route.Tools[tool.Name] = tool.Annotations.Hints()
			}
		}
		routes = append(routes, route)
	}
	return routes
}

// Servers returns the console view of every MCPServer, routed or not, sorted
// by namespace and name.
func Servers(items []kavryntv1alpha1.MCPServer) []model.Server {
	routed := map[string]bool{}
	for _, route := range Routes(items) {
		routed[route.Name] = true
	}
	servers := make([]model.Server, 0, len(items))
	for i := range items {
		item := &items[i]
		server := model.Server{
			Namespace:       item.Namespace,
			Name:            item.Name,
			Route:           item.RouteName(),
			Description:     item.Annotations[kavryntv1alpha1.DescriptionAnnotation],
			Version:         item.Spec.Version,
			Transport:       item.Spec.Transport,
			Endpoint:        redactEndpoint(item.Spec.Endpoint),
			Routed:          routed[item.RouteName()],
			SessionAffinity: kavryntv1alpha1.SessionAffinityNone,
			Conditions:      make([]model.Condition, 0, len(item.Status.Conditions)),
			Tools:           make([]model.Tool, 0, len(item.Status.Tools)),
			CreatedAt:       item.CreationTimestamp.Time,
		}
		if item.Spec.SessionAffinity == kavryntv1alpha1.SessionAffinityMCPSession {
			server.SessionAffinity = kavryntv1alpha1.SessionAffinityMCPSession
		}
		if info := item.Status.ServerInfo; info != nil {
			server.ServerInfo = &model.ServerInfo{Name: info.Name, Version: info.Version}
		}
		if item.Spec.Credential != nil {
			server.Credential = &model.ServerCredential{Required: true}
		}
		if exchange := item.Spec.TokenExchange; exchange != nil {
			server.TokenExchange = &model.ServerTokenExchange{Audience: exchange.Audience}
		}
		for _, condition := range item.Status.Conditions {
			server.Conditions = append(server.Conditions, model.Condition{
				Type:               condition.Type,
				Status:             string(condition.Status),
				Reason:             condition.Reason,
				Message:            condition.Message,
				LastTransitionTime: condition.LastTransitionTime.Time,
			})
		}
		for _, tool := range item.Status.Tools {
			entry := model.Tool{Name: tool.Name, Description: tool.Description}
			if hints := tool.Annotations.Hints(); len(hints) > 0 {
				entry.Annotations = hints
			}
			if tool.InputSchema != nil && len(tool.InputSchema.Raw) > 0 {
				entry.InputSchema = append([]byte(nil), tool.InputSchema.Raw...)
			}
			server.Tools = append(server.Tools, entry)
		}
		servers = append(servers, server)
	}
	sort.Slice(servers, func(i, j int) bool {
		if servers[i].Namespace != servers[j].Namespace {
			return servers[i].Namespace < servers[j].Namespace
		}
		return servers[i].Name < servers[j].Name
	})
	return servers
}

// redactEndpoint drops the query and fragment, which may carry secrets.
func redactEndpoint(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.ForceQuery = false
	parsed.Fragment = ""
	parsed.RawFragment = ""
	return parsed.String()
}
