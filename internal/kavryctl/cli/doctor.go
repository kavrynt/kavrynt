package cli

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"text/tabwriter"

	kavryntv1alpha1 "github.com/kavrynt/kavrynt/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// doctorType identifies version 1 of the JSON report (SPEC-0033-R06).
const doctorType = "kavrynt.doctor.v1"

// Check results (SPEC-0033-R04).
const (
	resultOK      = "ok"
	resultWarn    = "warn"
	resultFail    = "fail"
	resultSkipped = "skipped"
)

const docsBase = "https://github.com/kavrynt/kavrynt/blob/main/"

// readFailedDoc explains a ReadFailed check (SPEC-0033-R05).
const readFailedDoc = "docs/components/kavryctl/README.md#doctor"

// doctorCheck is one line of the report. It never holds condition
// messages, endpoints, or Secret data (SPEC-0033-R07).
type doctorCheck struct {
	ID      string `json:"id"`
	Result  string `json:"result"`
	Subject string `json:"subject"`
	Reason  string `json:"reason"`
	Fix     string `json:"fix"`
	Link    string `json:"link"`
}

type doctorReport struct {
	Type     string            `json:"type"`
	Versions map[string]string `json:"versions"`
	Checks   []doctorCheck     `json:"checks"`
}

// errUnreachable means the cluster did not answer at all (exit 2).
var errUnreachable = errors.New("the cluster could not be reached")

// doctor runs the checks of SPEC-0033 with get and list requests only.
type doctor struct {
	c         client.Client
	namespace string
	release   string
	names     *anonymizer // nil keeps names
	report    doctorReport
}

func runDoctor(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("doctor", stderr)
	var opts kubeOptions
	opts.bind(fs)
	release := fs.String("release", "kavrynt", "Helm release name")
	output := fs.String("output", "table", "Output format: table or json")
	anonymize := fs.Bool("anonymize", false, "Replace namespaces and MCPServer names with per-report hashes, for public issues")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 || (*output != "table" && *output != "json") {
		fmt.Fprintln(stderr, "usage: kavryctl doctor [-n kavrynt-system] [--release kavrynt] [--output table|json] [--anonymize]")
		return 2
	}
	if opts.namespace == "" {
		opts.namespace = "kavrynt-system"
	}
	// Errors can name the API server or the kubeconfig context; a report
	// made for a public issue leaves them out (SPEC-0033-R08).
	failed := func(err error) int {
		if *anonymize {
			if errors.Is(err, errUnreachable) {
				err = errUnreachable
			} else {
				err = errors.New("the cluster could not be read (details hidden by --anonymize)")
			}
		}
		fmt.Fprintf(stderr, "doctor: %v\n", err)
		return 2
	}
	c, namespace, err := newKubeClient(opts)
	if err != nil {
		return failed(err)
	}
	d := &doctor{c: c, namespace: namespace, release: *release,
		report: doctorReport{Type: doctorType, Versions: map[string]string{"kavryctl": strings.TrimPrefix(Version, "v")}}}
	if *anonymize {
		if d.names, err = newAnonymizer(); err != nil {
			fmt.Fprintf(stderr, "doctor: %v\n", err)
			return 2
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()
	if err := d.run(ctx); err != nil {
		return failed(err)
	}

	if *output == "json" {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(d.report)
	} else {
		d.printTable(stdout)
	}
	for _, check := range d.report.Checks {
		if check.Result == resultFail {
			return 1
		}
	}
	return 0
}

func (d *doctor) add(check doctorCheck) { d.report.Checks = append(d.report.Checks, check) }

// skipped reports a check the user may not run, naming the verb and
// resource (SPEC-0033-R02).
func (d *doctor) skipped(id, subject, verb, resource string) {
	d.add(doctorCheck{ID: id, Result: resultSkipped, Subject: subject, Reason: "forbidden: " + verb + " " + resource,
		Fix: "ask for " + verb + " on " + resource + ", or run doctor with a user who has it"})
}

// unreachable reports whether err means the API server did not answer, as
// opposed to answering with an error status.
func unreachable(err error) bool {
	var status apierrors.APIStatus
	return err != nil && !errors.As(err, &status) && !meta.IsNoMatchError(err)
}

func (d *doctor) run(ctx context.Context) error {
	if err := d.checkCRD(ctx); err != nil {
		return err
	}
	gateway, operator := d.checkDeployments(ctx)
	d.checkVersions(gateway, operator)
	d.checkAuth(gateway)
	d.checkServers(ctx)
	return nil
}

// checkCRD is check 1. It is the first request, so it also tells an
// unreachable cluster (exit 2) from one that answers.
func (d *doctor) checkCRD(ctx context.Context) error {
	var crd apiextensionsv1.CustomResourceDefinition
	err := d.c.Get(ctx, client.ObjectKey{Name: "mcpservers.kavrynt.io"}, &crd)
	switch {
	case unreachable(err):
		return fmt.Errorf("%w: %v", errUnreachable, err)
	case apierrors.IsForbidden(err):
		d.skipped("crd", "mcpservers.kavrynt.io", "get", "customresourcedefinitions")
	case apierrors.IsNotFound(err):
		d.add(doctorCheck{ID: "crd", Result: resultFail, Subject: "mcpservers.kavrynt.io", Reason: "NotInstalled",
			Fix: "install the Kavrynt Helm chart, which installs the MCPServer CRD", Link: docsBase + "charts/kavrynt/README.md"})
	case err != nil:
		d.add(doctorCheck{ID: "crd", Result: resultFail, Subject: "mcpservers.kavrynt.io", Reason: "ReadFailed",
			Fix: "check access to the API server and retry", Link: docsBase + readFailedDoc})
	default:
		served := slices.ContainsFunc(crd.Spec.Versions, func(v apiextensionsv1.CustomResourceDefinitionVersion) bool {
			return v.Name == kavryntv1alpha1.GroupVersion.Version && v.Served
		})
		if served {
			d.add(doctorCheck{ID: "crd", Result: resultOK, Subject: "mcpservers.kavrynt.io", Reason: "Served"})
		} else {
			d.add(doctorCheck{ID: "crd", Result: resultFail, Subject: "mcpservers.kavrynt.io", Reason: "VersionNotServed",
				Fix: "upgrade the chart so the CRD serves " + kavryntv1alpha1.GroupVersion.Version, Link: docsBase + "docs/components/kavryctl/README.md#upgrade-and-rollback"})
		}
	}
	return nil
}

// checkDeployments is check 2; it returns the Deployments it found.
func (d *doctor) checkDeployments(ctx context.Context) (gateway, operator *appsv1.Deployment) {
	// components are the app.kubernetes.io/name values to try in order:
	// the umbrella chart's name first, then the subchart's own.
	find := func(id, runbook string, components ...string) *appsv1.Deployment {
		subject := d.name(d.namespace) + "/" + components[0]
		var list appsv1.DeploymentList
		var err error
		for _, component := range components {
			list = appsv1.DeploymentList{}
			err = d.c.List(ctx, &list, client.InNamespace(d.namespace),
				client.MatchingLabels{"app.kubernetes.io/name": component, "app.kubernetes.io/instance": d.release})
			if err != nil || len(list.Items) > 0 {
				break
			}
		}
		switch {
		case apierrors.IsForbidden(err):
			d.skipped(id, subject, "list", "deployments")
			return nil
		case err != nil:
			d.add(doctorCheck{ID: id, Result: resultFail, Subject: subject, Reason: "ReadFailed",
				Fix: "check access to the API server and retry", Link: docsBase + runbook})
			return nil
		case len(list.Items) == 0:
			d.add(doctorCheck{ID: id, Result: resultFail, Subject: subject, Reason: "NotFound",
				Fix: "install the chart, or pass -n and --release for your installation", Link: docsBase + "charts/kavrynt/README.md"})
			return nil
		}
		deployment := &list.Items[0]
		want := int32(1)
		if deployment.Spec.Replicas != nil {
			want = *deployment.Spec.Replicas
		}
		if deployment.Status.ReadyReplicas < want {
			// An anonymized report must not name the namespace in the fix
			// either (SPEC-0033-R08).
			fix := "check the pods' events and logs: kubectl -n " + d.namespace + " describe deployment " + deployment.Name
			if d.names != nil {
				fix = "check the pods' events and logs with kubectl describe deployment"
			}
			d.add(doctorCheck{ID: id, Result: resultFail, Subject: subject,
				Reason: fmt.Sprintf("Ready %d/%d", deployment.Status.ReadyReplicas, want),
				Fix:    fix, Link: docsBase + runbook})
		} else {
			d.add(doctorCheck{ID: id, Result: resultOK, Subject: subject, Reason: fmt.Sprintf("Ready %d/%d", deployment.Status.ReadyReplicas, want)})
		}
		return deployment
	}
	gateway = find("gateway", "docs/runbooks/KavryntGatewayDown.md", "gateway")
	operator = find("operator", "docs/runbooks/KavryntOperatorDown.md", "operator", "k8s-operator")
	return gateway, operator
}

// imageVersion is the tag of the Deployment's first container image.
func imageVersion(deployment *appsv1.Deployment) string {
	if deployment == nil || len(deployment.Spec.Template.Spec.Containers) == 0 {
		return ""
	}
	image, _, _ := strings.Cut(deployment.Spec.Template.Spec.Containers[0].Image, "@")
	if i := strings.LastIndex(image, ":"); i > strings.LastIndex(image, "/") {
		return strings.TrimPrefix(image[i+1:], "v")
	}
	return ""
}

// checkVersions is check 3.
func (d *doctor) checkVersions(gateway, operator *appsv1.Deployment) {
	gw, op, cli := imageVersion(gateway), imageVersion(operator), d.report.Versions["kavryctl"]
	d.report.Versions["gateway"], d.report.Versions["operator"] = gw, op
	link := docsBase + "docs/components/kavryctl/README.md#install"
	switch {
	case gw == "" || op == "":
		d.add(doctorCheck{ID: "versions", Result: resultSkipped, Subject: "gateway, operator", Reason: "Unknown",
			Fix: "the Deployment checks above could not read both components"})
	case gw != op:
		d.add(doctorCheck{ID: "versions", Result: resultFail, Subject: "gateway " + gw + ", operator " + op, Reason: "Mismatch",
			Fix: "upgrade both components to one release with kavryctl upgrade", Link: docsBase + "docs/components/kavryctl/README.md#upgrade-and-rollback"})
	case cli != gw:
		d.add(doctorCheck{ID: "versions", Result: resultWarn, Subject: "kavryctl " + cli + ", runtime " + gw, Reason: "KavryctlDiffers",
			Fix: "install kavryctl " + gw + " to match the runtime", Link: link})
	default:
		d.add(doctorCheck{ID: "versions", Result: resultOK, Subject: gw, Reason: "Same"})
	}
}

// checkAuth is check 4: the Gateway runs with --auth.
func (d *doctor) checkAuth(gateway *appsv1.Deployment) {
	if gateway == nil || len(gateway.Spec.Template.Spec.Containers) == 0 {
		d.add(doctorCheck{ID: "auth", Result: resultSkipped, Subject: "gateway", Reason: "Unknown",
			Fix: "the Gateway Deployment could not be read"})
		return
	}
	if slices.Contains(gateway.Spec.Template.Spec.Containers[0].Args, "--auth") {
		d.add(doctorCheck{ID: "auth", Result: resultOK, Subject: "gateway", Reason: "Enabled"})
		return
	}
	d.add(doctorCheck{ID: "auth", Result: resultWarn, Subject: "gateway", Reason: "Disabled",
		Fix:  "anyone who reaches the Gateway can call every tool: set gateway.auth.resourceURL and gateway.auth.issuers",
		Link: docsBase + "docs/AUTH-SETUP.md"})
}

// serverPage bounds how many MCPServers one list request returns.
const serverPage = 200

// checkServers is checks 5 and 6, per MCPServer in every namespace, read
// in bounded pages, each checked before the next is requested.
func (d *doctor) checkServers(ctx context.Context) {
	seen, next := 0, ""
	for {
		var servers kavryntv1alpha1.MCPServerList
		err := d.c.List(ctx, &servers, client.Limit(serverPage), client.Continue(next))
		switch {
		case apierrors.IsForbidden(err):
			d.skipped("servers", "mcpservers", "list", "mcpservers")
			return
		case err != nil:
			d.add(doctorCheck{ID: "servers", Result: resultFail, Subject: "mcpservers", Reason: "ReadFailed",
				Fix: "check access to the API server and retry", Link: docsBase + readFailedDoc})
			return
		}
		seen += len(servers.Items)
		d.checkServerPage(servers.Items)
		if next = servers.Continue; next == "" {
			break
		}
	}
	if seen == 0 {
		d.add(doctorCheck{ID: "servers", Result: resultOK, Subject: "mcpservers", Reason: "None",
			Fix: "register one: kavryctl register <manifest>"})
	}
}

func (d *doctor) checkServerPage(servers []kavryntv1alpha1.MCPServer) {
	for _, server := range servers {
		subject := d.name(server.Namespace) + "/" + d.name(server.Name)
		ready := meta.FindStatusCondition(server.Status.Conditions, kavryntv1alpha1.ConditionReady)
		if ready == nil || ready.Status != "True" {
			reason := "Pending"
			if ready != nil {
				reason = knownReason(ready.Reason)
			}
			d.add(doctorCheck{ID: "server.ready", Result: resultFail, Subject: subject, Reason: reason,
				Fix: readyFix(reason), Link: docsBase + "docs/runbooks/KavryntMCPServerNotReady.md"})
			continue
		}
		d.add(doctorCheck{ID: "server.ready", Result: resultOK, Subject: subject, Reason: knownReason(ready.Reason)})
		discovered := meta.FindStatusCondition(server.Status.Conditions, kavryntv1alpha1.ConditionToolsDiscovered)
		if discovered != nil && discovered.Status == "True" {
			d.add(doctorCheck{ID: "server.discovery", Result: resultOK, Subject: subject, Reason: knownReason(discovered.Reason)})
			continue
		}
		reason := "Pending"
		if discovered != nil {
			reason = knownReason(discovered.Reason)
		}
		d.add(doctorCheck{ID: "server.discovery", Result: resultWarn, Subject: subject, Reason: reason,
			Fix: discoveryFix(reason), Link: docsBase + "docs/runbooks/KavryntMCPToolDiscoveryFailing.md"})
	}
}

// knownReasons are the condition reasons the Operator writes.
var knownReasons = []string{
	kavryntv1alpha1.ReasonValid, kavryntv1alpha1.ReasonInvalidSpec, kavryntv1alpha1.ReasonRoutable,
	kavryntv1alpha1.ReasonUnsupportedTransport, kavryntv1alpha1.ReasonNamespaceNotWatched,
	kavryntv1alpha1.ReasonDiscovered, kavryntv1alpha1.ReasonDiscoveryFailed, kavryntv1alpha1.ReasonNotRoutable,
	kavryntv1alpha1.ReasonCredentialUnavailable, kavryntv1alpha1.ReasonCallerTokenRequired,
	kavryntv1alpha1.ReasonEndpointNotAllowed, kavryntv1alpha1.ReasonAudienceNotAllowed,
}

// knownReason returns reason if the Operator writes it, or "Other": the
// CRD does not limit reasons, and anyone who can write the status could put
// an endpoint or control characters there (SPEC-0033-R07).
func knownReason(reason string) string {
	if slices.Contains(knownReasons, reason) {
		return reason
	}
	return "Other"
}

// readyFix says what to do for a Ready reason; the condition message is
// never copied, since it can name the endpoint (SPEC-0033-R07).
func readyFix(reason string) string {
	switch reason {
	case kavryntv1alpha1.ReasonInvalidSpec:
		return "fix the spec: kavryctl validate <manifest>"
	case kavryntv1alpha1.ReasonUnsupportedTransport:
		return "only http is routed; run a stdio server behind a Streamable HTTP bridge"
	case kavryntv1alpha1.ReasonNamespaceNotWatched:
		return "add the namespace to global.watchNamespaces"
	case kavryntv1alpha1.ReasonEndpointNotAllowed:
		return "point the endpoint at its own namespace, or allow the pair in global.endpoints.crossNamespace"
	case kavryntv1alpha1.ReasonAudienceNotAllowed:
		return "list the token exchange audience in global.tokenExchangeAudiences"
	default:
		return "kubectl describe mcpserver shows the condition"
	}
}

// discoveryFix says what to do for a ToolsDiscovered reason.
func discoveryFix(reason string) string {
	switch reason {
	case kavryntv1alpha1.ReasonCredentialUnavailable:
		return "store the server's credential in the upstream credentials Secret"
	case kavryntv1alpha1.ReasonCallerTokenRequired:
		return "expected for token exchange: tools are listed when a caller connects"
	default:
		return "check that the server answers MCP at its endpoint; kubectl describe mcpserver says why"
	}
}

func (d *doctor) printTable(w io.Writer) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "CHECK\tRESULT\tSUBJECT\tREASON")
	counts := map[string]int{}
	for _, check := range d.report.Checks {
		counts[check.Result]++
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", check.ID, check.Result, check.Subject, check.Reason)
	}
	_ = tw.Flush()
	for _, check := range d.report.Checks {
		if check.Result == resultWarn || check.Result == resultFail {
			fmt.Fprintf(w, "\n%s %s: %s\n", check.Result, check.Subject, check.Fix)
			if check.Link != "" {
				fmt.Fprintf(w, "  %s\n", check.Link)
			}
		}
	}
	fmt.Fprintf(w, "\n%d checks: %d ok, %d warn, %d fail, %d skipped\n", len(d.report.Checks),
		counts[resultOK], counts[resultWarn], counts[resultFail], counts[resultSkipped])
}

// name returns a namespace or MCPServer name, anonymized with --anonymize.
func (d *doctor) name(s string) string {
	if d.names == nil {
		return s
	}
	return d.names.token(s)
}

// anonymizer replaces names with the HMAC-SHA256 of a per-report random
// key, kept only in memory (SPEC-0033-R08).
type anonymizer struct {
	key    []byte
	tokens map[string]string
	owners map[string]string
}

func newAnonymizer() (*anonymizer, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("make the anonymization key: %w", err)
	}
	return &anonymizer{key: key, tokens: map[string]string{}, owners: map[string]string{}}, nil
}

func (a *anonymizer) token(name string) string {
	if t, ok := a.tokens[name]; ok {
		return t
	}
	mac := hmac.New(sha256.New, a.key)
	mac.Write([]byte(name))
	sum := hex.EncodeToString(mac.Sum(nil))
	// Lengthen on a collision until the token is unused.
	for n := 12; n <= len(sum); n += 4 {
		if _, taken := a.owners[sum[:n]]; !taken {
			a.tokens[name], a.owners[sum[:n]] = sum[:n], name
			return sum[:n]
		}
	}
	return sum
}
