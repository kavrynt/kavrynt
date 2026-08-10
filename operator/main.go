package main

import (
	"flag"
	"log/slog"
	"net/http"
	"os"
	"time"

	kavryntv1alpha1 "github.com/kavrynt/k8s-operator/api/v1alpha1"
	"github.com/kavrynt/k8s-operator/internal/build"
	"github.com/kavrynt/k8s-operator/internal/controller"
	"github.com/kavrynt/k8s-operator/internal/registry"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

func main() {
	var (
		metricsAddr     string
		probeAddr       string
		registryURL     string
		syncRetryPeriod time.Duration
		enableLeader    bool
	)

	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "Metrics bind address")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "Health probe bind address")
	flag.StringVar(&registryURL, "registry-url", getenv("KAVRYNT_REGISTRY_URL", ""), "Kavrynt Registry base URL")
	flag.DurationVar(&syncRetryPeriod, "sync-retry-period", 30*time.Second, "Retry period after Registry sync failures")
	flag.BoolVar(&enableLeader, "leader-elect", false, "Enable leader election")
	opts := zap.Options{Development: true}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(kavryntv1alpha1.AddToScheme(scheme))

	registryClient, err := registry.NewClient(registryURL, &http.Client{Timeout: 30 * time.Second})
	if err != nil {
		slog.Error("invalid registry configuration", "error", err)
		os.Exit(1)
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: metricsAddr},
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeader,
		LeaderElectionID:       "kavrynt-k8s-operator",
	})
	if err != nil {
		slog.Error("unable to create manager", "error", err)
		os.Exit(1)
	}

	if err := (&controller.MCPServerReconciler{
		Client:         mgr.GetClient(),
		Scheme:         mgr.GetScheme(),
		RegistryClient: registryClient,
		RequeueAfter:   syncRetryPeriod,
	}).SetupWithManager(mgr); err != nil {
		slog.Error("unable to create controller", "error", err)
		os.Exit(1)
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

func getenv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
