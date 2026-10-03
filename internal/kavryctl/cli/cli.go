package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	kavryntv1alpha1 "github.com/kavrynt/kavrynt/api/v1alpha1"
	"github.com/kavrynt/kavrynt/internal/kavryctl/manifest"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/duration"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var (
	Version   = "0.1.0-dev"
	Commit    = "unknown"
	BuildDate = "unknown"
)

const requestTimeout = 30 * time.Second

// kubeOptions are the connection flags shared by cluster commands.
type kubeOptions struct {
	kubeconfig string
	context    string
	namespace  string
}

func (o *kubeOptions) bind(fs *flag.FlagSet) {
	fs.StringVar(&o.kubeconfig, "kubeconfig", "", "Path to the kubeconfig file (defaults to KUBECONFIG or ~/.kube/config)")
	fs.StringVar(&o.context, "context", "", "Kubeconfig context to use")
	fs.StringVar(&o.namespace, "namespace", "", "Namespace (defaults to the context namespace)")
	fs.StringVar(&o.namespace, "n", "", "Shorthand for --namespace")
}

// newKubeClient returns a client and the effective namespace. Tests replace it.
var newKubeClient = func(opts kubeOptions) (client.Client, string, error) {
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	rules.ExplicitPath = opts.kubeconfig
	overrides := &clientcmd.ConfigOverrides{CurrentContext: opts.context}
	loader := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, overrides)

	cfg, err := loader.ClientConfig()
	if err != nil {
		return nil, "", fmt.Errorf("load kubeconfig: %w", err)
	}
	namespace := opts.namespace
	if namespace == "" {
		if namespace, _, err = loader.Namespace(); err != nil {
			return nil, "", fmt.Errorf("resolve namespace: %w", err)
		}
	}

	scheme := runtime.NewScheme()
	if err := kavryntv1alpha1.AddToScheme(scheme); err != nil {
		return nil, "", err
	}
	c, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		return nil, "", fmt.Errorf("create Kubernetes client: %w", err)
	}
	return c, namespace, nil
}

func Execute(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printUsage(stderr)
		return 2
	}

	switch args[0] {
	case "version":
		fmt.Fprintf(stdout, "kavryctl %s\ncommit %s\nbuilt %s\n", Version, Commit, BuildDate)
		return 0
	case "help", "-h", "--help":
		printUsage(stdout)
		return 0
	case "validate":
		return runValidate(args[1:], stdout, stderr)
	case "register":
		return runRegister(args[1:], stdout, stderr)
	case "unregister":
		return runUnregister(args[1:], stdout, stderr)
	case "list":
		return runList(args[1:], stdout, stderr)
	case "inspect":
		return runInspect(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown command %q\n\n", args[0])
		printUsage(stderr)
		return 2
	}
}

func runValidate(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("validate", stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: kavryctl validate <manifest.yaml|json>")
		return 2
	}

	server, err := manifest.Load(fs.Arg(0))
	if err != nil {
		fmt.Fprintf(stderr, "validation failed: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "valid MCPServer manifest: %s\n", server.Name)
	return 0
}

func runRegister(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("register", stderr)
	var opts kubeOptions
	opts.bind(fs)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: kavryctl register [-n NAMESPACE] <manifest.yaml|json>")
		return 2
	}

	server, err := manifest.Load(fs.Arg(0))
	if err != nil {
		fmt.Fprintf(stderr, "registration failed: %v\n", err)
		return 1
	}
	if opts.namespace != "" && server.Namespace != "" && opts.namespace != server.Namespace {
		fmt.Fprintf(stderr, "registration failed: manifest namespace %q conflicts with --namespace %q\n", server.Namespace, opts.namespace)
		return 2
	}
	if opts.namespace == "" {
		opts.namespace = server.Namespace
	}

	c, namespace, err := newKubeClient(opts)
	if err != nil {
		fmt.Fprintf(stderr, "registration failed: %v\n", err)
		return 1
	}
	server.Namespace = namespace

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()

	action := "registered"
	if err := c.Create(ctx, server); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			fmt.Fprintf(stderr, "registration failed: %v\n", err)
			return 1
		}
		var existing kavryntv1alpha1.MCPServer
		if err := c.Get(ctx, client.ObjectKeyFromObject(server), &existing); err != nil {
			fmt.Fprintf(stderr, "registration failed: %v\n", err)
			return 1
		}
		existing.Labels = server.Labels
		existing.Annotations = server.Annotations
		existing.Spec = server.Spec
		if err := c.Update(ctx, &existing); err != nil {
			fmt.Fprintf(stderr, "registration failed: %v\n", err)
			return 1
		}
		action = "updated"
	}

	fmt.Fprintf(stdout, "%s MCPServer %s/%s@%s\n", action, server.Namespace, server.Name, server.Spec.Version)
	return 0
}

func runUnregister(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("unregister", stderr)
	var opts kubeOptions
	opts.bind(fs)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: kavryctl unregister [-n NAMESPACE] <name>")
		return 2
	}

	c, namespace, err := newKubeClient(opts)
	if err != nil {
		fmt.Fprintf(stderr, "unregister failed: %v\n", err)
		return 1
	}

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()

	server := &kavryntv1alpha1.MCPServer{ObjectMeta: metav1.ObjectMeta{Name: fs.Arg(0), Namespace: namespace}}
	if err := c.Delete(ctx, server); err != nil {
		fmt.Fprintf(stderr, "unregister failed: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "unregistered MCPServer %s/%s\n", namespace, fs.Arg(0))
	return 0
}

func runList(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("list", stderr)
	var opts kubeOptions
	opts.bind(fs)
	allNamespaces := fs.Bool("all-namespaces", false, "List MCPServers in all namespaces")
	fs.BoolVar(allNamespaces, "A", false, "Shorthand for --all-namespaces")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(stderr, "list does not accept positional arguments")
		return 2
	}

	c, namespace, err := newKubeClient(opts)
	if err != nil {
		fmt.Fprintf(stderr, "list failed: %v\n", err)
		return 1
	}

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()

	var listOpts []client.ListOption
	if !*allNamespaces {
		listOpts = append(listOpts, client.InNamespace(namespace))
	}
	var servers kavryntv1alpha1.MCPServerList
	if err := c.List(ctx, &servers, listOpts...); err != nil {
		fmt.Fprintf(stderr, "list failed: %v\n", err)
		return 1
	}
	if len(servers.Items) == 0 {
		fmt.Fprintln(stdout, "no MCPServers found")
		return 0
	}

	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAMESPACE\tNAME\tVERSION\tTRANSPORT\tREADY\tROUTE\tAGE")
	now := time.Now()
	for i := range servers.Items {
		server := &servers.Items[i]
		ready := "Unknown"
		if condition := apimeta.FindStatusCondition(server.Status.Conditions, kavryntv1alpha1.ConditionReady); condition != nil {
			ready = string(condition.Status)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			server.Namespace,
			server.Name,
			server.Spec.Version,
			server.Spec.Transport,
			ready,
			"/mcp/"+server.RouteName(),
			duration.HumanDuration(now.Sub(server.CreationTimestamp.Time)),
		)
	}
	_ = tw.Flush()
	return 0
}

func runInspect(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("inspect", stderr)
	var opts kubeOptions
	opts.bind(fs)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: kavryctl inspect [-n NAMESPACE] <name>")
		return 2
	}

	c, namespace, err := newKubeClient(opts)
	if err != nil {
		fmt.Fprintf(stderr, "inspect failed: %v\n", err)
		return 1
	}

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()

	var server kavryntv1alpha1.MCPServer
	if err := c.Get(ctx, types.NamespacedName{Name: fs.Arg(0), Namespace: namespace}, &server); err != nil {
		fmt.Fprintf(stderr, "inspect failed: %v\n", err)
		return 1
	}
	server.APIVersion = kavryntv1alpha1.GroupVersion.String()
	server.Kind = kavryntv1alpha1.MCPServerKind
	server.ManagedFields = nil

	data, err := json.MarshalIndent(&server, "", "  ")
	if err != nil {
		fmt.Fprintf(stderr, "inspect failed: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, string(data))
	return 0
}

func newFlagSet(name string, stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	return fs
}

func printUsage(w io.Writer) {
	fmt.Fprint(w, strings.TrimSpace(`
kavryctl manages Kavrynt MCPServer resources in a Kubernetes cluster.

Usage:
  kavryctl version
  kavryctl validate <manifest.yaml|json>
  kavryctl register   [cluster flags] <manifest.yaml|json>
  kavryctl unregister [cluster flags] <name>
  kavryctl list       [cluster flags] [-A]
  kavryctl inspect    [cluster flags] <name>

Cluster flags:
  --kubeconfig PATH   Kubeconfig file (defaults to KUBECONFIG or ~/.kube/config)
  --context NAME      Kubeconfig context
  -n, --namespace NS  Namespace (defaults to the context namespace)

The Gateway routes each Ready MCPServer at /mcp/<namespace>.<name>.
`)+"\n")
}
