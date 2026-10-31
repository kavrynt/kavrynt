package cli

import (
	"context"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	kavryntv1alpha1 "github.com/kavrynt/kavrynt/api/v1alpha1"
	"github.com/kavrynt/kavrynt/internal/kavryctl/manifest"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// redactedAnnotation lists the spec.environment keys an export wrote empty
// (SPEC-0034-R01). import refuses documents that still carry it (R04).
const redactedAnnotation = "kavrynt.io/redacted-environment"

// redactedArgsAnnotation counts the spec.args an export wrote empty
// (SPEC-0034-R09); import refuses documents that still carry it.
const redactedArgsAnnotation = "kavrynt.io/redacted-args"

const lastAppliedAnnotation = "kubectl.kubernetes.io/last-applied-configuration"

// maxImportBytes bounds the file import reads (SPEC-0034-R10).
const maxImportBytes = 16 << 20

// kubeContextName is the kubeconfig context a command uses, named in the
// export header instead of the API server address (SPEC-0034-R02). Tests
// replace it.
var kubeContextName = func(opts kubeOptions) string {
	if opts.context != "" {
		return opts.context
	}
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	rules.ExplicitPath = opts.kubeconfig
	raw, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, &clientcmd.ConfigOverrides{}).RawConfig()
	if err != nil || raw.CurrentContext == "" {
		return "unknown"
	}
	return raw.CurrentContext
}

// exportedMeta is the metadata an export keeps: labels, and annotations
// without kubectl's last-applied copy (SPEC-0034-R01).
func exportedMeta(server *kavryntv1alpha1.MCPServer) (labels, annotations map[string]string) {
	labels = maps.Clone(server.Labels)
	annotations = maps.Clone(server.Annotations)
	delete(annotations, lastAppliedAnnotation)
	if len(labels) == 0 {
		labels = nil
	}
	if len(annotations) == 0 {
		annotations = nil
	}
	return labels, annotations
}

func runExport(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("export", stderr)
	var opts kubeOptions
	opts.bind(fs)
	allNamespaces := fs.Bool("all-namespaces", false, "Export MCPServers in all namespaces")
	fs.BoolVar(allNamespaces, "A", false, "Shorthand for --all-namespaces")
	outFile := fs.String("o", "", "Write to this file (mode 0600) instead of standard output")
	includeEnv := fs.Bool("include-environment", false, "Keep spec.environment values (the file may then hold credentials)")
	includeArgs := fs.Bool("include-args", false, "Keep spec.args values (the file may then hold credentials)")
	releaseNamespace := fs.String("release-namespace", "kavrynt-system", "Namespace of the Kavrynt release, for the Operator version")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: kavryctl export [-n NAMESPACE | -A] [-o FILE] [--include-environment]")
		return 2
	}
	c, namespace, err := newKubeClient(opts)
	if err != nil {
		fmt.Fprintf(stderr, "export failed: %v\n", err)
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
		fmt.Fprintf(stderr, "export failed: %v\n", err)
		return 1
	}
	slices.SortFunc(servers.Items, func(a, b kavryntv1alpha1.MCPServer) int {
		return strings.Compare(a.Namespace+"/"+a.Name, b.Namespace+"/"+b.Name)
	})

	var body strings.Builder
	var credentials, redactions []string
	for i := range servers.Items {
		server := copyServer(&servers.Items[i])
		labels, annotations := exportedMeta(server)
		out := &kavryntv1alpha1.MCPServer{}
		out.Name, out.Namespace, out.Labels, out.Annotations = server.Name, server.Namespace, labels, annotations
		out.Spec = server.Spec
		if len(out.Spec.Environment) > 0 && !*includeEnv {
			keys := slices.Sorted(maps.Keys(out.Spec.Environment))
			for _, key := range keys {
				out.Spec.Environment[key] = ""
			}
			if out.Annotations == nil {
				out.Annotations = map[string]string{}
			}
			out.Annotations[redactedAnnotation] = strings.Join(keys, ",")
			redactions = append(redactions, fmt.Sprintf("%s/%s: %s", out.Namespace, out.Name, strings.Join(keys, ", ")))
		}
		// Arguments can carry a key too (--token=...); they are written
		// empty and counted, and import refuses them until filled in
		// (SPEC-0034-R09).
		if len(out.Spec.Args) > 0 && !*includeArgs {
			out.Spec.Args = make([]string, len(out.Spec.Args))
			if out.Annotations == nil {
				out.Annotations = map[string]string{}
			}
			out.Annotations[redactedArgsAnnotation] = strconv.Itoa(len(out.Spec.Args))
			redactions = append(redactions, fmt.Sprintf("%s/%s: %d spec.args", out.Namespace, out.Name, len(out.Spec.Args)))
		}
		if out.Spec.Credential != nil {
			key := out.Namespace + "." + out.Name
			credentials = append(credentials, key+", "+key+".origin")
		}
		data, err := manifest.Marshal(out)
		if err != nil {
			fmt.Fprintf(stderr, "export failed: %s/%s: %v\n", out.Namespace, out.Name, err)
			return 1
		}
		// Every document passes kavryctl validate (SPEC-0034-R03).
		if _, err := manifest.Parse(data); err != nil {
			fmt.Fprintf(stderr, "export failed: %s/%s does not validate: %v\n", out.Namespace, out.Name, err)
			return 1
		}
		body.WriteString("---\n")
		body.Write(data)
	}

	var file strings.Builder
	fmt.Fprintf(&file, "# kavryctl export of %d MCPServers, %s\n", len(servers.Items), time.Now().UTC().Format(time.RFC3339))
	fmt.Fprintf(&file, "# Source context: %s\n", kubeContextName(opts))
	fmt.Fprintf(&file, "# Operator version: %s\n", operatorVersion(ctx, c, *releaseNamespace))
	if len(credentials) > 0 {
		file.WriteString("# Upstream credentials Secret keys to provide (values are never exported):\n")
		for _, keys := range credentials {
			fmt.Fprintf(&file, "#   %s\n", keys)
		}
	}
	if len(redactions) > 0 {
		fmt.Fprintf(&file, "# spec.environment and spec.args values written empty (see the %s and\n# %s annotations); fill them in and remove the annotations before importing:\n", redactedAnnotation, redactedArgsAnnotation)
		for _, line := range redactions {
			fmt.Fprintf(&file, "#   %s\n", line)
		}
	}
	file.WriteString(body.String())

	if *includeEnv {
		fmt.Fprintln(stderr, "warning: --include-environment keeps spec.environment values; the export may hold credentials")
	}
	if *includeArgs {
		fmt.Fprintln(stderr, "warning: --include-args keeps spec.args values; the export may hold credentials")
	}
	if *outFile == "" {
		fmt.Fprint(stdout, file.String())
		return 0
	}
	if err := writePrivateFile(*outFile, file.String()); err != nil {
		fmt.Fprintf(stderr, "export failed: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "exported %s to %s\n", plural(len(servers.Items), "MCPServer"), *outFile)
	return 0
}

// writePrivateFile writes data to path with mode 0600, also when the file
// existed with a wider mode: the mode is set before anything is written.
func writePrivateFile(path, data string) error {
	// #nosec G304 -- writing the file the user names is the command's purpose.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Truncate(0); err != nil {
		_ = f.Close()
		return err
	}
	if _, err := f.WriteString(data); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// operatorVersion is the image tag of the Operator Deployment in namespace,
// or "unknown" (SPEC-0034-R02).
func operatorVersion(ctx context.Context, c client.Client, namespace string) string {
	for _, component := range []string{"operator", "k8s-operator"} {
		var list appsv1.DeploymentList
		if err := c.List(ctx, &list, client.InNamespace(namespace), client.MatchingLabels{"app.kubernetes.io/name": component}); err != nil {
			return "unknown"
		}
		if len(list.Items) > 0 {
			if v := imageVersion(&list.Items[0]); v != "" {
				return v
			}
		}
	}
	return "unknown"
}

// importPlan is what import does with one document.
type importPlan struct {
	server   *kavryntv1alpha1.MCPServer
	existing *kavryntv1alpha1.MCPServer
	// updated is the object an overwrite sends, checked by a server-side
	// dry run before anything is written.
	updated *kavryntv1alpha1.MCPServer
	action  string // create, unchanged, overwrite
}

func (p importPlan) key() string { return p.server.Namespace + "/" + p.server.Name }

func runImport(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("import", stderr)
	var opts kubeOptions
	opts.bind(fs)
	overwrite := fs.Bool("overwrite", false, "Replace MCPServers whose spec, labels, or annotations differ")
	dryRun := fs.Bool("dry-run", false, "Report what import would do, with server-side dry run, and change nothing")
	wait := fs.Duration("wait", 60*time.Second, "How long to wait for imported MCPServers to become Ready (0 skips)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: kavryctl import [--overwrite] [--dry-run] [--wait 60s] <file>")
		return 2
	}
	data, err := readImportFile(fs.Arg(0))
	if err != nil {
		fmt.Fprintf(stderr, "import failed: %v\n", err)
		return 1
	}
	c, namespace, err := newKubeClient(opts)
	if err != nil {
		fmt.Fprintf(stderr, "import failed: %v\n", err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()

	plans, problems := planImport(ctx, c, namespace, manifest.SplitDocuments(data), *overwrite)
	if len(problems) > 0 {
		// Nothing is written while any document is refused (SPEC-0034-R04).
		for _, problem := range problems {
			fmt.Fprintln(stdout, "refused", problem)
		}
		fmt.Fprintf(stdout, "%s refused; nothing was changed\n", plural(len(problems), "document"))
		return 1
	}

	var applied []importPlan
	failed := 0
	for _, plan := range plans {
		var dry []client.CreateOption
		var dryUpdate []client.UpdateOption
		if *dryRun {
			dry = append(dry, client.DryRunAll)
			dryUpdate = append(dryUpdate, client.DryRunAll)
		}
		switch plan.action {
		case "unchanged":
			fmt.Fprintln(stdout, "unchanged", plan.key())
			continue
		case "create":
			err = c.Create(ctx, plan.server, dry...)
		case "overwrite":
			// The resourceVersion read in the check makes a concurrent change
			// a conflict instead of a lost update.
			err = c.Update(ctx, copyServer(plan.updated), dryUpdate...)
		}
		switch {
		case apierrors.IsConflict(err) || apierrors.IsAlreadyExists(err):
			fmt.Fprintln(stdout, "refused", plan.key()+": changed by someone else since the check; run import again")
			failed++
		case err != nil:
			fmt.Fprintf(stdout, "refused %s: %v\n", plan.key(), err)
			failed++
		default:
			verb := map[string]string{"create": "created", "overwrite": "overwrote"}[plan.action]
			if *dryRun {
				verb = "would " + map[string]string{"create": "create", "overwrite": "overwrite"}[plan.action]
			}
			fmt.Fprintln(stdout, verb, plan.key())
			applied = append(applied, plan)
		}
	}
	if !*dryRun && *wait > 0 && len(applied) > 0 {
		reportReadiness(c, applied, *wait, stdout)
	}
	if failed > 0 {
		return 1
	}
	return 0
}

// readImportFile reads at most maxImportBytes and refuses a larger file
// before parsing any of it (SPEC-0034-R10).
func readImportFile(path string) ([]byte, error) {
	// #nosec G304 -- reading the file the user names is the command's purpose.
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxImportBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxImportBytes {
		return nil, fmt.Errorf("%s is larger than %d MiB; split it", path, maxImportBytes>>20)
	}
	return data, nil
}

// planImport checks every document before anything is written
// (SPEC-0034-R04, R05, R07) and returns the plans, or every problem.
func planImport(ctx context.Context, c client.Client, defaultNamespace string, docs [][]byte, overwrite bool) ([]importPlan, []string) {
	var plans []importPlan
	var problems []string
	seen := map[string]bool{}
	namespaces := map[string]bool{}
	for i, doc := range docs {
		server, err := manifest.Parse(doc)
		if err != nil {
			problems = append(problems, fmt.Sprintf("document %d: %v", i+1, err))
			continue
		}
		if server.Namespace == "" {
			server.Namespace = defaultNamespace
		}
		plan := importPlan{server: server}
		key := plan.key()
		if seen[key] {
			problems = append(problems, key+": named by two documents")
			continue
		}
		seen[key] = true
		if count, ok := server.Annotations[redactedArgsAnnotation]; ok {
			problems = append(problems, fmt.Sprintf("%s: %s spec.args were redacted; fill them in and remove the %s annotation", key, count, redactedArgsAnnotation))
			continue
		}
		if keys, ok := server.Annotations[redactedAnnotation]; ok {
			problems = append(problems, fmt.Sprintf("%s: spec.environment was redacted; fill in %s and remove the %s annotation", key, keys, redactedAnnotation))
			continue
		}
		if exists, checked := namespaces[server.Namespace]; checked && !exists {
			problems = append(problems, fmt.Sprintf("%s: namespace %s does not exist; import does not create namespaces", key, server.Namespace))
			continue
		} else if !checked {
			err := c.Get(ctx, client.ObjectKey{Name: server.Namespace}, &corev1.Namespace{})
			// A user who may not read namespaces is checked by the server-side
			// dry run below instead.
			namespaces[server.Namespace] = !apierrors.IsNotFound(err)
			if apierrors.IsNotFound(err) {
				problems = append(problems, fmt.Sprintf("%s: namespace %s does not exist; import does not create namespaces", key, server.Namespace))
				continue
			}
		}
		existing := &kavryntv1alpha1.MCPServer{}
		err = c.Get(ctx, client.ObjectKey{Namespace: server.Namespace, Name: server.Name}, existing)
		switch {
		case apierrors.IsNotFound(err):
			plan.action = "create"
			if err := c.Create(ctx, copyServer(server), client.DryRunAll); err != nil {
				problems = append(problems, fmt.Sprintf("%s: the API server refuses it: %v", key, err))
				continue
			}
		case err != nil:
			problems = append(problems, fmt.Sprintf("%s: %v", key, err))
			continue
		default:
			plan.existing = existing
			labels, annotations := exportedMeta(existing)
			same := equality.Semantic.DeepEqual(existing.Spec, server.Spec) &&
				equality.Semantic.DeepEqual(labels, emptyToNil(server.Labels)) &&
				equality.Semantic.DeepEqual(annotations, emptyToNil(server.Annotations))
			switch {
			case same:
				plan.action = "unchanged"
			case overwrite:
				plan.action = "overwrite"
				plan.updated = copyServer(existing)
				plan.updated.Labels, plan.updated.Annotations, plan.updated.Spec = server.Labels, mergeLastApplied(existing, server.Annotations), server.Spec
				// The update is checked like a create, so an admission or
				// validation refusal stops the import before any write.
				if err := c.Update(ctx, copyServer(plan.updated), client.DryRunAll); err != nil {
					problems = append(problems, fmt.Sprintf("%s: the API server refuses the overwrite: %v", key, err))
					continue
				}
			default:
				problems = append(problems, key+": exists with another spec, labels, or annotations; --overwrite replaces it")
				continue
			}
		}
		plans = append(plans, plan)
	}
	return plans, problems
}

// copyServer deep-copies an MCPServer.
func copyServer(server *kavryntv1alpha1.MCPServer) *kavryntv1alpha1.MCPServer {
	return server.DeepCopyObject().(*kavryntv1alpha1.MCPServer)
}

func emptyToNil(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	return m
}

// mergeLastApplied keeps kubectl's last-applied annotation, which exports
// leave out, when import replaces the annotations.
func mergeLastApplied(existing *kavryntv1alpha1.MCPServer, annotations map[string]string) map[string]string {
	out := maps.Clone(annotations)
	if last, ok := existing.Annotations[lastAppliedAnnotation]; ok {
		if out == nil {
			out = map[string]string{}
		}
		out[lastAppliedAnnotation] = last
	}
	return out
}

// reportReadiness waits up to wait for the imported servers and reports
// those not Ready, and those missing their upstream credential
// (SPEC-0034-R08).
func reportReadiness(c client.Client, plans []importPlan, wait time.Duration, w io.Writer) {
	deadline := time.Now().Add(wait)
	pending := slices.Clone(plans)
	for {
		var still []importPlan
		for _, plan := range pending {
			ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
			server := &kavryntv1alpha1.MCPServer{}
			err := c.Get(ctx, client.ObjectKey{Namespace: plan.server.Namespace, Name: plan.server.Name}, server)
			cancel()
			ready := meta.FindStatusCondition(server.Status.Conditions, kavryntv1alpha1.ConditionReady)
			discovered := meta.FindStatusCondition(server.Status.Conditions, kavryntv1alpha1.ConditionToolsDiscovered)
			// Conditions of the stored spec, not one an overwrite replaced.
			current := ready != nil && ready.ObservedGeneration == server.Generation
			if err == nil && current && ready.Status == "True" && discovered != nil {
				if discovered.Reason == kavryntv1alpha1.ReasonCredentialUnavailable {
					key := plan.server.Namespace + "." + plan.server.Name
					fmt.Fprintf(w, "%s is Ready but needs its credential: add %s and %s to the upstream credentials Secret\n", plan.key(), key, key+".origin")
				}
				continue
			}
			still = append(still, plan)
		}
		pending = still
		if len(pending) == 0 {
			fmt.Fprintln(w, "every imported MCPServer is Ready")
			return
		}
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(min(2*time.Second, time.Until(deadline)))
	}
	for _, plan := range pending {
		ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
		server := &kavryntv1alpha1.MCPServer{}
		_ = c.Get(ctx, client.ObjectKey{Namespace: plan.server.Namespace, Name: plan.server.Name}, server)
		cancel()
		reason := "Pending"
		if ready := meta.FindStatusCondition(server.Status.Conditions, kavryntv1alpha1.ConditionReady); ready != nil && ready.Status != "True" && ready.ObservedGeneration == server.Generation {
			reason = ready.Reason
		}
		fmt.Fprintf(w, "%s is not Ready after %s: %s (kubectl describe mcpserver -n %s %s)\n", plan.key(), wait, reason, plan.server.Namespace, plan.server.Name)
	}
}
