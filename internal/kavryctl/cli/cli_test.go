package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	auditv1 "github.com/kavrynt/kavrynt/api/audit/v1"
	kavryntv1alpha1 "github.com/kavrynt/kavrynt/api/v1alpha1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

const httpManifest = `apiVersion: kavrynt.io/v1alpha1
kind: MCPServer
metadata:
  name: payments
spec:
  version: 0.1.0
  transport: http
  endpoint: http://payments.team-a.svc:8080/mcp
`

func TestRegisterListInspectUnregister(t *testing.T) {
	k8sClient := useFakeClient(t, "team-a")
	path := writeManifest(t, httpManifest)

	out, errOut, code := run(t, "register", path)
	if code != 0 || !strings.Contains(out, "registered MCPServer team-a/payments@0.1.0") {
		t.Fatalf("register: code=%d out=%q err=%q", code, out, errOut)
	}

	var stored kavryntv1alpha1.MCPServer
	if err := k8sClient.Get(context.Background(), types.NamespacedName{Namespace: "team-a", Name: "payments"}, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.Spec.Endpoint != "http://payments.team-a.svc:8080/mcp" {
		t.Fatalf("stored endpoint = %q", stored.Spec.Endpoint)
	}

	updated := strings.Replace(httpManifest, "version: 0.1.0", "version: 0.2.0", 1)
	out, errOut, code = run(t, "register", writeManifest(t, updated))
	if code != 0 || !strings.Contains(out, "updated MCPServer team-a/payments@0.2.0") {
		t.Fatalf("re-register: code=%d out=%q err=%q", code, out, errOut)
	}

	out, errOut, code = run(t, "list")
	// No Operator runs here, so the server is not Ready and has no route yet.
	if code != 0 || !strings.Contains(out, "payments") || strings.Contains(out, "/mcp/") || !strings.Contains(out, "0.2.0") {
		t.Fatalf("list: code=%d out=%q err=%q", code, out, errOut)
	}

	out, errOut, code = run(t, "inspect", "payments")
	if code != 0 || !strings.Contains(out, `"kind": "MCPServer"`) || !strings.Contains(out, `"version": "0.2.0"`) {
		t.Fatalf("inspect: code=%d out=%q err=%q", code, out, errOut)
	}

	out, errOut, code = run(t, "unregister", "payments")
	if code != 0 || !strings.Contains(out, "unregistered MCPServer team-a/payments") {
		t.Fatalf("unregister: code=%d out=%q err=%q", code, out, errOut)
	}

	out, _, code = run(t, "list")
	if code != 0 || !strings.Contains(out, "no MCPServers found") {
		t.Fatalf("list after unregister: code=%d out=%q", code, out)
	}
}

func TestListAllNamespacesShowsReadiness(t *testing.T) {
	useFakeClient(t, "default",
		&kavryntv1alpha1.MCPServer{
			ObjectMeta: metav1.ObjectMeta{Name: "a", Namespace: "team-a"},
			Spec:       kavryntv1alpha1.MCPServerSpec{Version: "1", Transport: "http", Endpoint: "http://a"},
			Status: kavryntv1alpha1.MCPServerStatus{
				Conditions: []metav1.Condition{
					{Type: kavryntv1alpha1.ConditionReady, Status: metav1.ConditionTrue, Reason: "Routable", LastTransitionTime: metav1.Now()},
					{Type: kavryntv1alpha1.ConditionToolsDiscovered, Status: metav1.ConditionTrue, Reason: "Discovered", LastTransitionTime: metav1.Now()},
				},
				Tools: []kavryntv1alpha1.MCPServerTool{{Name: "echo"}, {Name: "search"}},
			},
		},
		&kavryntv1alpha1.MCPServer{
			ObjectMeta: metav1.ObjectMeta{Name: "b", Namespace: "team-b"},
			Spec:       kavryntv1alpha1.MCPServerSpec{Version: "1", Transport: "stdio", Command: "b"},
		},
		&kavryntv1alpha1.MCPServer{
			ObjectMeta: metav1.ObjectMeta{Name: "c", Namespace: "team-c"},
			Spec:       kavryntv1alpha1.MCPServerSpec{Version: "1", Transport: "http", Endpoint: "http://c"},
			Status: kavryntv1alpha1.MCPServerStatus{
				Conditions: []metav1.Condition{
					{Type: kavryntv1alpha1.ConditionReady, Status: metav1.ConditionTrue, Reason: "Routable", LastTransitionTime: metav1.Now()},
					{Type: kavryntv1alpha1.ConditionToolsDiscovered, Status: metav1.ConditionFalse, Reason: kavryntv1alpha1.ReasonDiscoveryFailed, LastTransitionTime: metav1.Now()},
				},
			},
		},
	)

	out, errOut, code := run(t, "list", "-A")
	if code != 0 {
		t.Fatalf("list -A: code=%d err=%q", code, errOut)
	}
	for _, want := range []string{"team-a", "team-b", "True", "Unknown", "/mcp/team-a.a"} {
		if !strings.Contains(out, want) {
			t.Fatalf("list -A output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "/mcp/team-b.b") {
		t.Fatalf("list -A shows a route for a server that is not Ready:\n%s", out)
	}
	// Verifies SPEC-0027-R02: TOOLS is the count when discovered, the reason
	// when discovery failed, and - before discovery ran.
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		switch {
		case len(fields) > 0 && fields[0] == "team-a" && fields[6] != "2":
			t.Fatalf("team-a TOOLS = %q, want 2:\n%s", fields[6], out)
		case len(fields) > 0 && fields[0] == "team-b" && fields[6] != "-":
			t.Fatalf("team-b TOOLS = %q, want - before discovery:\n%s", fields[6], out)
		case len(fields) > 0 && fields[0] == "team-c" && fields[6] != kavryntv1alpha1.ReasonDiscoveryFailed:
			t.Fatalf("team-c TOOLS = %q, want %s:\n%s", fields[6], kavryntv1alpha1.ReasonDiscoveryFailed, out)
		}
	}

	out, _, _ = run(t, "list")
	if !strings.Contains(out, "no MCPServers found") {
		t.Fatalf("list in default namespace should be empty:\n%s", out)
	}
}

func TestRegisterRejectsNamespaceConflict(t *testing.T) {
	useFakeClient(t, "default")
	path := writeManifest(t, strings.Replace(httpManifest, "name: payments", "name: payments\n  namespace: team-a", 1))

	_, errOut, code := run(t, "register", "-n", "team-b", path)
	if code != 2 || !strings.Contains(errOut, "conflicts with --namespace") {
		t.Fatalf("code=%d err=%q", code, errOut)
	}
}

func TestValidateRejectsInvalidManifest(t *testing.T) {
	path := writeManifest(t, strings.Replace(httpManifest, "transport: http", "transport: grpc", 1))
	_, errOut, code := run(t, "validate", path)
	if code != 1 || !strings.Contains(errOut, "spec.transport must be one of") {
		t.Fatalf("code=%d err=%q", code, errOut)
	}
}

// stdio manifests stay valid, but the Gateway never routes them, so
// validate and register say so on stderr (issue #87).
func TestStdioManifestsWarnThatTheyAreNotRouted(t *testing.T) {
	useFakeClient(t, "team-a")
	stdio := writeManifest(t, "apiVersion: kavrynt.io/v1alpha1\nkind: MCPServer\nmetadata:\n  name: files\nspec:\n  version: 0.1.0\n  transport: stdio\n  command: npx\n")
	const warning = "warning: the Gateway does not proxy stdio servers"
	for _, command := range []string{"validate", "register"} {
		_, errOut, code := run(t, command, stdio)
		if code != 0 || !strings.Contains(errOut, warning) {
			t.Errorf("%s stdio: code=%d err=%q", command, code, errOut)
		}
		if _, errOut, _ := run(t, command, writeManifest(t, httpManifest)); strings.Contains(errOut, "warning") {
			t.Errorf("%s http warned: %q", command, errOut)
		}
	}
}

func TestUnregisterMissingServerFails(t *testing.T) {
	useFakeClient(t, "default")
	_, errOut, code := run(t, "unregister", "missing")
	if code != 1 || !strings.Contains(errOut, "not found") {
		t.Fatalf("code=%d err=%q", code, errOut)
	}
}

func TestUnknownCommand(t *testing.T) {
	_, errOut, code := run(t, "init")
	if code != 2 || !strings.Contains(errOut, `unknown command "init"`) {
		t.Fatalf("code=%d err=%q", code, errOut)
	}
}

func useFakeClient(t *testing.T, defaultNamespace string, objects ...client.Object) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := kavryntv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := apiextensionsv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&kavryntv1alpha1.MCPServer{}).
		WithObjects(objects...).
		Build()

	original := newKubeClient
	newKubeClient = func(opts kubeOptions) (client.Client, string, error) {
		if opts.namespace != "" {
			return k8sClient, opts.namespace, nil
		}
		return k8sClient, defaultNamespace, nil
	}
	t.Cleanup(func() { newKubeClient = original })
	return k8sClient
}

func writeManifest(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "server.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func run(t *testing.T, args ...string) (string, string, int) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := Execute(args, &out, &errOut)
	return out.String(), errOut.String(), code
}

// Verifies SPEC-0004-R05.
func TestInspectShowsReportedServerInfo(t *testing.T) {
	useFakeClient(t, "team-a", &kavryntv1alpha1.MCPServer{
		ObjectMeta: metav1.ObjectMeta{Name: "payments", Namespace: "team-a"},
		Spec:       kavryntv1alpha1.MCPServerSpec{Version: "0.1.0", Transport: "http", Endpoint: "http://payments"},
		Status: kavryntv1alpha1.MCPServerStatus{
			ServerInfo: &kavryntv1alpha1.MCPServerInfo{Name: "payments-mcp", Version: "0.2.0"},
		},
	})
	out, errOut, code := run(t, "inspect", "payments")
	if code != 0 || !strings.Contains(out, `"serverInfo": {`) || !strings.Contains(out, `"name": "payments-mcp"`) || !strings.Contains(out, `"version": "0.2.0"`) {
		t.Fatalf("inspect: code=%d out=%q err=%q", code, out, errOut)
	}
}

// Verifies SPEC-0013-R07.
func TestAuditVerify(t *testing.T) {
	prev := auditv1.GenesisHash
	var lines []string
	for i := 1; i <= 3; i++ {
		e := auditv1.Event{Type: auditv1.Type, Instance: "gw/1", Seq: uint64(i), PrevHash: prev, Decision: auditv1.DecisionAllow}
		e.Hash = auditv1.ComputeHash(e)
		prev = e.Hash
		data, _ := json.Marshal(e)
		lines = append(lines, string(data))
	}
	dir := t.TempDir()
	good := filepath.Join(dir, "good.log")
	if err := os.WriteFile(good, []byte("2026/10/24 INFO starting\n"+strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := Execute([]string{"audit", "verify", good}, &stdout, &stderr); code != 0 || !strings.Contains(stdout.String(), "3 events from 1 instance: every chain verifies") {
		t.Fatalf("good log: exit %d, %s %s", code, stdout.String(), stderr.String())
	}

	tampered := strings.Join([]string{lines[0], strings.Replace(lines[1], `"decision":"allow"`, `"decision":"deny"`, 1), lines[2]}, "\n")
	stdout.Reset()
	if code := runAudit([]string{"verify"}, strings.NewReader(tampered), &stdout, &stderr); code != 1 || !strings.Contains(stdout.String(), "BREAK instance gw/1 seq 2") || !strings.Contains(stdout.String(), "3 events from 1 instance: 1 break\n") {
		t.Fatalf("tampered log: exit %d, %s", code, stdout.String())
	}
	// Verifies SPEC-0035-R05: a clean run says how many events carried
	// fields this version does not know.
	newer := strings.Replace(lines[0], `"issuer":"",`, `"issuer":"","future_field":"x",`, 1)
	newer = rehashLine(t, newer, auditv1.GenesisHash)
	stdout.Reset()
	if code := runAudit([]string{"verify"}, strings.NewReader(newer+"\n"), &stdout, &stderr); code != 0 ||
		!strings.Contains(stdout.String(), "1 event from 1 instance: every chain verifies") ||
		!strings.Contains(stdout.String(), "1 event carried fields this kavryctl does not know") {
		t.Fatalf("newer event: exit %d, %s %s", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	if code := Execute([]string{"audit", "verify", good}, &stdout, &stderr); code != 0 || strings.Contains(stdout.String(), "does not know") {
		t.Fatalf("current events reported unknown fields: %s", stdout.String())
	}
	if code := runAudit([]string{"verify"}, strings.NewReader("no events here\n"), &stdout, &stderr); code != 1 {
		t.Fatal("a log without events verified")
	}
	if code := Execute([]string{"audit"}, &stdout, &stderr); code != 2 {
		t.Fatal("audit without verify accepted")
	}
}

// rehashLine recomputes the hash of a written line, as the Gateway that
// wrote it would have, over the line without its hash member.
func rehashLine(t *testing.T, line, prev string) string {
	t.Helper()
	body, _, ok := strings.Cut(line, `,"hash":"`)
	if !ok {
		t.Fatalf("no hash member: %s", line)
	}
	sum := sha256.Sum256([]byte(prev + body + "}"))
	return body + `,"hash":"` + hex.EncodeToString(sum[:]) + `"}`
}

// Verifies SPEC-0037-R01, SPEC-0037-R02, and SPEC-0037-R03: an empty list
// names its namespace and kavryctl list -A, counts servers elsewhere when it
// may, and stays quiet about them when it may not.
func TestEmptyListPointsToOtherNamespaces(t *testing.T) {
	server := func(namespace, name string) *kavryntv1alpha1.MCPServer {
		return &kavryntv1alpha1.MCPServer{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
			Spec:       kavryntv1alpha1.MCPServerSpec{Version: "1.0.0", Transport: "http", Endpoint: "http://" + name},
		}
	}
	useFakeClient(t, "default", server("team-a", "search"), server("team-b", "billing"), server("team-c", "files"))
	out, errOut, code := run(t, "list")
	if code != 0 || strings.TrimSpace(out) != "no MCPServers found in namespace default; 3 in other namespaces: kavryctl list -A" || errOut != "" {
		t.Fatalf("list: code=%d out=%q err=%q", code, out, errOut)
	}

	scheme := runtime.NewScheme()
	if err := kavryntv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	refusing := fake.NewClientBuilder().WithScheme(scheme).WithObjects(server("team-a", "search")).
		WithInterceptorFuncs(interceptor.Funcs{List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			if (&client.ListOptions{}).ApplyOptions(opts).Namespace == "" {
				return apierrors.NewForbidden(schema.GroupResource{Group: "kavrynt.io", Resource: "mcpservers"}, "", errors.New("cluster-wide list not allowed"))
			}
			return c.List(ctx, list, opts...)
		}}).Build()
	if msg := emptyListMessage(context.Background(), refusing, "default"); msg != "no MCPServers found in namespace default (kavryctl list -A lists every namespace)" {
		t.Fatalf("refused cluster-wide list: %q", msg)
	}

	useFakeClient(t, "default")
	if out, _, code := run(t, "list", "-A"); code != 0 || strings.TrimSpace(out) != "no MCPServers found" {
		t.Fatalf("list -A with no servers: code=%d out=%q", code, out)
	}
}
