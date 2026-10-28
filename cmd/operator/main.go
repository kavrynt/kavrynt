package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/netip"
	"os"
	"strings"
	"time"

	kavryntv1alpha1 "github.com/kavrynt/kavrynt/api/v1alpha1"
	"github.com/kavrynt/kavrynt/internal/egress"
	"github.com/kavrynt/kavrynt/internal/mcpsession"
	"github.com/kavrynt/kavrynt/internal/operator/build"
	"github.com/kavrynt/kavrynt/internal/operator/cloudsync"
	"github.com/kavrynt/kavrynt/internal/operator/controller"
	"github.com/kavrynt/kavrynt/internal/operator/discovery"
	"github.com/kavrynt/kavrynt/internal/scope"
	"github.com/kavrynt/kavrynt/internal/upstreamcred"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

func main() {
	var (
		metricsAddr       string
		probeAddr         string
		enableLeader      bool
		gatewayNamespaces string
		toolDiscovery     bool
		toolRefresh       time.Duration
		cloudSync         bool
		cloudURL          string
		cloudTokenFile    string
		cloudInterval     time.Duration
		allowLoopback     bool
		credentialsDir    string
		gatewayNamespace  string
		crossNamespace    string
		clusterCIDRs      string
		exchangeAudiences string
	)

	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "Metrics bind address")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "Health probe bind address")
	flag.BoolVar(&enableLeader, "leader-elect", false, "Enable leader election")
	flag.StringVar(&gatewayNamespaces, "gateway-watch-namespaces", "", "Comma-separated namespaces the Gateway watches (empty means all); servers elsewhere are not Ready")
	flag.BoolVar(&toolDiscovery, "tool-discovery", true, "List the tools of Ready MCP servers into MCPServer status")
	flag.DurationVar(&toolRefresh, "tool-refresh-interval", controller.DefaultToolRefreshInterval, "How often tools are listed again")
	flag.BoolVar(&allowLoopback, "allow-loopback-endpoints", false, "Allow tool discovery against endpoints that resolve to loopback addresses (local development only)")
	flag.StringVar(&credentialsDir, "upstream-credentials-dir", os.Getenv("KAVRYNT_UPSTREAM_CREDENTIALS_DIR"), "Directory of the mounted upstream credentials Secret, for discovering the tools of MCP servers that declare spec.credential")
	flag.StringVar(&gatewayNamespace, "gateway-namespace", os.Getenv("POD_NAMESPACE"), "Namespace of the Gateway, where single-label endpoint hosts resolve")
	flag.StringVar(&crossNamespace, "endpoint-cross-namespace", "", "Comma-separated from:to namespace pairs whose MCPServers in from may use endpoints in to")
	flag.StringVar(&clusterCIDRs, "cluster-cidrs", "", "Comma-separated Service and Pod CIDRs; IP endpoints inside them are not Ready")
	flag.StringVar(&exchangeAudiences, "token-exchange-audiences", "", `JSON object from namespace (or "*") to the token exchange audiences its MCPServers may request; empty allows none`)
	flag.BoolVar(&cloudSync, "cloud-sync", false, "Send the MCPServer inventory to Kavrynt Cloud")
	flag.StringVar(&cloudURL, "cloud-url", "", "Kavrynt Cloud base URL (https)")
	flag.StringVar(&cloudTokenFile, "cloud-token-file", cloudsync.DefaultTokenFile, "File holding the Kavrynt Cloud cluster token; read before every sync")
	flag.DurationVar(&cloudInterval, "cloud-sync-interval", cloudsync.DefaultInterval, "How often the inventory is sent without changes")
	opts := zap.Options{Development: true}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(kavryntv1alpha1.AddToScheme(scheme))

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: metricsAddr},
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeader,
		LeaderElectionID:       "kavrynt-k8s-operator",
		// A stopping leader gives up the lease at once, so a standby replica
		// takes over without waiting for it to expire (SPEC-0008-R08). Safe:
		// the process exits as soon as the manager stops.
		LeaderElectionReleaseOnCancel: true,
	})
	if err != nil {
		slog.Error("unable to create manager", "error", err)
		os.Exit(1)
	}

	policy, err := endpointScope(gatewayNamespace, crossNamespace, clusterCIDRs, exchangeAudiences, mgr.GetAPIReader())
	if err != nil {
		slog.Error("invalid endpoint scope", "error", err)
		os.Exit(1)
	}
	reconciler := &controller.MCPServerReconciler{
		Client:              mgr.GetClient(),
		Scheme:              mgr.GetScheme(),
		GatewayNamespaces:   splitList(gatewayNamespaces),
		ToolRefreshInterval: toolRefresh,
		Credentials:         upstreamcred.Store{Dir: credentialsDir},
		Scope:               policy,
	}
	if toolDiscovery {
		reconciler.Discoverer = discovery.New(build.Version, egress.Transport(allowLoopback))
		reconciler.SessionEndpoints = mcpsession.Endpoints{Reader: mgr.GetAPIReader(), AllowLoopback: allowLoopback}
	}
	if err := reconciler.SetupWithManager(mgr); err != nil {
		slog.Error("unable to create controller", "error", err)
		os.Exit(1)
	}

	if cloudSync {
		syncer, err := cloudsync.New(mgr.GetClient(), mgr.GetCache(), cloudsync.Config{
			URL:       cloudURL,
			TokenFile: cloudTokenFile,
			Interval:  cloudInterval,
			Version:   build.Version,
		})
		if err != nil {
			slog.Error("invalid Kavrynt Cloud sync configuration", "error", err)
			os.Exit(1)
		}
		if err := mgr.Add(syncer); err != nil {
			slog.Error("unable to add Kavrynt Cloud sync", "error", err)
			os.Exit(1)
		}
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		slog.Error("unable to set up health check", "error", err)
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		slog.Error("unable to set up readiness check", "error", err)
		os.Exit(1)
	}

	slog.Info("starting kavrynt k8s operator", "version", build.Version, "commit", build.Commit, "buildDate", build.BuildDate)
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		slog.Error("operator stopped with error", "error", err)
		os.Exit(1)
	}
}

func splitList(value string) []string {
	var items []string
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			items = append(items, item)
		}
	}
	return items
}

// endpointScope builds the endpoint and audience rules from the flags
// (SPEC-0029). Namespaces are read uncached, one at a time.
func endpointScope(gatewayNamespace, crossNamespace, clusterCIDRs, audiences string, reader client.Reader) (*scope.Policy, error) {
	policy := &scope.Policy{
		GatewayNamespace: gatewayNamespace,
		NamespaceExists: func(ctx context.Context, name string) (bool, error) {
			err := reader.Get(ctx, client.ObjectKey{Name: name}, &corev1.Namespace{})
			if apierrors.IsNotFound(err) {
				return false, nil
			}
			return err == nil, err
		},
	}
	var err error
	if policy.CrossNamespace, err = scope.ParseNamespacePairs(crossNamespace); err != nil {
		return nil, fmt.Errorf("--endpoint-cross-namespace: %w", err)
	}
	for _, cidr := range splitList(clusterCIDRs) {
		prefix, err := netip.ParsePrefix(cidr)
		if err != nil {
			return nil, fmt.Errorf("--cluster-cidrs: %q is not a CIDR", cidr)
		}
		policy.ClusterCIDRs = append(policy.ClusterCIDRs, prefix.Masked())
	}
	if policy.Audiences, err = scope.ParseAudiences(audiences); err != nil {
		return nil, fmt.Errorf("--token-exchange-audiences: %w", err)
	}
	return policy, nil
}
