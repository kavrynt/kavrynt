package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	auditv1 "github.com/kavrynt/kavrynt/api/audit/v1"
	kavryntv1alpha1 "github.com/kavrynt/kavrynt/api/v1alpha1"
	"github.com/kavrynt/kavrynt/internal/kavryctl/manifest"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
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
	// upgrade reads and adopts the MCPServer CRD (SPEC-0019).
	if err := apiextensionsv1.AddToScheme(scheme); err != nil {
		return nil, "", err
	}
	// doctor reads the Gateway and Operator Deployments (SPEC-0033).
	if err := appsv1.AddToScheme(scheme); err != nil {
		return nil, "", err
	}
	// import checks that namespaces exist (SPEC-0034).
	if err := corev1.AddToScheme(scheme); err != nil {
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
	case "export":
		return runExport(args[1:], stdout, stderr)
	case "import":
		return runImport(args[1:], stdout, stderr)
	case "doctor":
		return runDoctor(args[1:], stdout, stderr)
	case "audit":
		return runAudit(args[1:], os.Stdin, stdout, stderr)
	case "upgrade":
		return runUpgrade(args[1:], stdout, stderr)
	case "rollback":
		return runRollback(args[1:], stdout, stderr)
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
	warnNotRouted(stderr, server)
	return 0
}

// warnNotRouted says when the Gateway will never route a valid server.
func warnNotRouted(stderr io.Writer, server *kavryntv1alpha1.MCPServer) {
	if server.Spec.Transport == "stdio" {
		fmt.Fprintf(stderr, "warning: the Gateway does not proxy stdio servers, so %s will not be routed; run it behind a Streamable HTTP bridge and register the bridge's http endpoint (see examples/stdio-bridge)\n", server.Name)
	}
}

// runAudit verifies Gateway audit event chains read from a file or standard
// input (SPEC-0013-R07). Lines that are not audit events are skipped, so
// collected Gateway logs can be passed as they are.
func runAudit(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "verify" || len(args) > 2 {
		fmt.Fprintln(stderr, "usage: kavryctl audit verify [file]   (reads standard input without a file)")
		return 2
	}
	input := stdin
	if len(args) == 2 && args[1] != "-" {
		// #nosec G304 -- reading the file the user names is the command's purpose.
		file, err := os.Open(args[1])
		if err != nil {
			fmt.Fprintf(stderr, "audit verify: %v\n", err)
			return 1
		}
		defer file.Close()
		input = file
	}
	summary, err := auditv1.Verify(input)
	if err != nil {
		fmt.Fprintf(stderr, "audit verify: %v\n", err)
		return 1
	}
	for _, b := range summary.Breaks {
		fmt.Fprintln(stdout, "BREAK", b)
	}
	if summary.Events == 0 {
		fmt.Fprintln(stderr, "audit verify: no audit events found")
		return 1
	}
	if len(summary.Breaks) > 0 {
		fmt.Fprintf(stdout, "%s from %s: %s\n", plural(summary.Events, "event"), plural(summary.Instances, "instance"), plural(len(summary.Breaks), "break"))
		return 1
	}
	fmt.Fprintf(stdout, "%s from %s: every chain verifies\n", plural(summary.Events, "event"), plural(summary.Instances, "instance"))
	if summary.UnknownFields > 0 {
		// Their hashes cover those fields, but this version cannot say what
		// they mean (SPEC-0035-R05).
		fmt.Fprintf(stdout, "%s carried fields this kavryctl does not know; a newer kavryctl can interpret them\n", plural(summary.UnknownFields, "event"))
	}
	return 0
}

// plural returns n and noun, with an "s" unless n is 1.
func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
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
	warnNotRouted(stderr, server)
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
		if *allNamespaces {
			fmt.Fprintln(stdout, "no MCPServers found")
		} else {
			fmt.Fprintln(stdout, emptyListMessage(ctx, c, namespace, listAllCommand(opts)))
		}
		return 0
	}

	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAMESPACE\tNAME\tVERSION\tTRANSPORT\tREADY\tROUTE\tTOOLS\tAGE")
	now := time.Now()
	for i := range servers.Items {
		server := &servers.Items[i]
		ready := "Unknown"
		if condition := apimeta.FindStatusCondition(server.Status.Conditions, kavryntv1alpha1.ConditionReady); condition != nil {
			ready = string(condition.Status)
		}
		// The Gateway routes only Ready servers, so others have no route.
		route := "-"
		if ready == string(metav1.ConditionTrue) {
			route = "/mcp/" + server.RouteName()
		}
		// Tools are known once the Operator has listed them; until then the
		// reason says why not, so a failed discovery is not mistaken for one
		// that has not run (SPEC-0027-R02).
		tools := "-"
		if condition := apimeta.FindStatusCondition(server.Status.Conditions, kavryntv1alpha1.ConditionToolsDiscovered); condition != nil {
			tools = condition.Reason
			if condition.Status == metav1.ConditionTrue {
				tools = strconv.Itoa(len(server.Status.Tools))
			}
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			server.Namespace,
			server.Name,
			server.Spec.Version,
			server.Spec.Transport,
			ready,
			route,
			tools,
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

// countTimeout bounds the optional count of an empty list: the answer is
// already known, so a slow cluster-wide list only drops the count.
const countTimeout = 5 * time.Second

// countPage is the page size of that count, so it never holds the whole
// cluster's list.
const countPage = 500

// emptyListMessage names the namespace an empty list searched and how to
// search all of them, with the count elsewhere when the user may list it
// (SPEC-0037). A refused, failed, or slow cluster-wide list only leaves the
// count out.
func emptyListMessage(ctx context.Context, c client.Client, namespace, listAll string) string {
	ctx, cancel := context.WithTimeout(ctx, countTimeout)
	defer cancel()
	count, next := 0, ""
	for {
		var page kavryntv1alpha1.MCPServerList
		if err := c.List(ctx, &page, client.Limit(countPage), client.Continue(next)); err != nil {
			count = 0
			break
		}
		count += len(page.Items)
		if next = page.Continue; next == "" {
			break
		}
	}
	if count > 0 {
		return fmt.Sprintf("no MCPServers found in namespace %s; %d in other namespaces: %s", namespace, count, listAll)
	}
	return fmt.Sprintf("no MCPServers found in namespace %s (%s lists every namespace)", namespace, listAll)
}

// listAllCommand is the kavryctl list -A command for the cluster the user
// chose with --kubeconfig and --context.
func listAllCommand(opts kubeOptions) string {
	command := "kavryctl list -A"
	if opts.kubeconfig != "" {
		command += " --kubeconfig " + shellQuote(opts.kubeconfig)
	}
	if opts.context != "" {
		command += " --context " + shellQuote(opts.context)
	}
	return command
}

// shellQuote quotes s for a POSIX shell unless it needs none.
func shellQuote(s string) string {
	if s != "" && strings.Trim(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_./:@") == "" {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
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
  kavryctl export   [cluster flags] [-A] [-o FILE] [--include-environment]
                    Write MCPServers as manifests to restore elsewhere; environment
                    values are written empty and Secrets are never read
  kavryctl import   [cluster flags] [--overwrite] [--dry-run] [--wait 60s] <file>
                    Create the MCPServers of an export, checking every document
                    first; nothing changes while any is refused
  kavryctl doctor   [cluster flags] [--release kavrynt] [--output table|json] [--anonymize]
                    Check the installation and its MCPServers (read-only; -n
                    defaults to kavrynt-system); --anonymize hides names
  kavryctl audit verify [file]   Verify Gateway audit event chains (file or stdin);
                    events cut off the end of a chain are not detected
  kavryctl upgrade  [cluster flags] --version V [--chart REF|DIR] [--values FILE] [--adopt-crd] [--dry-run]
  kavryctl rollback [cluster flags] [--revision N] [--dry-run]
                    Upgrade or roll back the Kavrynt release with helm, checking
                    before that nothing blocks it and after that every routed
                    server is routed again (--release kavrynt, -n kavrynt-system)

Cluster flags:
  --kubeconfig PATH   Kubeconfig file (defaults to KUBECONFIG or ~/.kube/config)
  --context NAME      Kubeconfig context
  -n, --namespace NS  Namespace (defaults to the context namespace)

The Gateway routes each Ready MCPServer at /mcp/<namespace>.<name>.
`)+"\n")
}
