package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	kavryntv1alpha1 "github.com/kavrynt/kavrynt/api/v1alpha1"
	"github.com/kavrynt/kavrynt/internal/kavryctl/manifest"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

// useBackupClient serves objects through a fake client that fails the test
// on any Secret access (SPEC-0034-R02), with extra interceptors.
func useBackupClient(t *testing.T, funcs interceptor.Funcs, objects ...client.Object) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{kavryntv1alpha1.AddToScheme, apiextensionsv1.AddToScheme, appsv1.AddToScheme, corev1.AddToScheme} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}
	get := funcs.Get
	funcs.Get = func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
		if _, secret := obj.(*corev1.Secret); secret {
			t.Error("read a Secret")
		}
		if get != nil {
			return get(ctx, c, key, obj, opts...)
		}
		return c.Get(ctx, key, obj, opts...)
	}
	list := funcs.List
	funcs.List = func(ctx context.Context, c client.WithWatch, l client.ObjectList, opts ...client.ListOption) error {
		if _, secrets := l.(*corev1.SecretList); secrets {
			t.Error("listed Secrets")
		}
		if list != nil {
			return list(ctx, c, l, opts...)
		}
		return c.List(ctx, l, opts...)
	}
	k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&kavryntv1alpha1.MCPServer{}).
		WithObjects(objects...).WithInterceptorFuncs(funcs).Build()
	original, originalContext := newKubeClient, kubeContextName
	newKubeClient = func(opts kubeOptions) (client.Client, string, error) {
		namespace := opts.namespace
		if namespace == "" {
			namespace = "default"
		}
		return k8sClient, namespace, nil
	}
	kubeContextName = func(kubeOptions) string { return "lab-context" }
	t.Cleanup(func() { newKubeClient, kubeContextName = original, originalContext })
	return k8sClient
}

func namespace(name string) *corev1.Namespace {
	return &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
}

// sourceCluster has two servers, one with a credential and one with an
// environment value and kubectl's last-applied annotation, and an Operator.
func sourceCluster() []client.Object {
	search := mcpServer("team-a", "search", condition(kavryntv1alpha1.ConditionReady, "True", kavryntv1alpha1.ReasonRoutable, ""))
	search.Spec.Credential = &kavryntv1alpha1.MCPServerCredential{Header: "Authorization", Scheme: "Bearer"}
	billing := mcpServer("team-b", "billing")
	billing.Labels = map[string]string{"team": "b"}
	billing.Annotations = map[string]string{lastAppliedAnnotation: `{"secret":"x"}`, "owner": "billing-team"}
	billing.Spec.Environment = map[string]string{"API_KEY": "s3cret-value", "REGION": "eu"}
	objects := []client.Object{namespace("team-a"), namespace("team-b"), billing, search}
	return append(objects, doctorCluster("1.0.0-beta.4")[2])
}

func run2(args ...string) (string, string, int) {
	var out, errOut bytes.Buffer
	code := Execute(args, &out, &errOut)
	return out.String(), errOut.String(), code
}

// Verifies SPEC-0034-R01, SPEC-0034-R02, and SPEC-0034-R03: the export holds
// only the allowed fields, in order, with environment values empty and
// listed, a header naming the context, the Operator version, and the
// credential keys, and documents that pass kavryctl validate.
func TestExport(t *testing.T) {
	useBackupClient(t, interceptor.Funcs{}, sourceCluster()...)
	out, errOut, code := run2("export", "-A")
	if code != 0 || errOut != "" {
		t.Fatalf("export: exit %d %s", code, errOut)
	}
	for _, want := range []string{"# Source context: lab-context", "# Operator version: 1.0.0-beta.4", "team-a.search, team-a.search.origin",
		redactedAnnotation + ": API_KEY,REGION", "API_KEY: \"\"", "owner: billing-team", "team: b"} {
		if !strings.Contains(out, want) {
			t.Errorf("export lacks %q:\n%s", want, out)
		}
	}
	for _, leak := range []string{"s3cret-value", "status:", "resourceVersion", "uid:", "managedFields", lastAppliedAnnotation, "https://", "6443"} {
		if strings.Contains(out, leak) {
			t.Errorf("export contains %q:\n%s", leak, out)
		}
	}
	docs := manifest.SplitDocuments([]byte(out))
	if len(docs) != 2 || !strings.Contains(string(docs[0]), "name: search") {
		t.Fatalf("want team-a/search first of two documents, got %d:\n%s", len(docs), out)
	}
	for _, doc := range docs {
		if _, err := manifest.Parse(doc); err != nil {
			t.Errorf("document does not validate: %v", err)
		}
	}

	file := filepath.Join(t.TempDir(), "backup.yaml")
	if _, errOut, code := run2("export", "-A", "--include-environment", "-o", file); code != 0 || !strings.Contains(errOut, "may hold credentials") {
		t.Fatalf("--include-environment: exit %d %q", code, errOut)
	}
	info, err := os.Stat(file)
	data, _ := os.ReadFile(file)
	if err != nil || info.Mode().Perm() != 0o600 || !strings.Contains(string(data), "s3cret-value") || strings.Contains(string(data), redactedAnnotation) {
		t.Fatalf("export file: %v %v\n%s", err, info.Mode(), data)
	}
}

// Verifies SPEC-0034-R04, SPEC-0034-R05, and SPEC-0034-R07: an export
// restores into an empty cluster, imports again as unchanged, and a
// differing server refuses the whole file unless --overwrite.
func TestImportRoundTrip(t *testing.T) {
	useBackupClient(t, interceptor.Funcs{}, sourceCluster()...)
	file := filepath.Join(t.TempDir(), "backup.yaml")
	if _, errOut, code := run2("export", "-A", "--include-environment", "-o", file); code != 0 {
		t.Fatal(errOut)
	}

	target := useBackupClient(t, interceptor.Funcs{}, namespace("team-a"), namespace("team-b"))
	if out, _, code := run2("import", "--wait", "0", file); code != 0 || !strings.Contains(out, "created team-a/search") || !strings.Contains(out, "created team-b/billing") {
		t.Fatalf("first import: exit %d\n%s", code, out)
	}
	billing := &kavryntv1alpha1.MCPServer{}
	if err := target.Get(context.Background(), client.ObjectKey{Namespace: "team-b", Name: "billing"}, billing); err != nil || billing.Spec.Environment["API_KEY"] != "s3cret-value" || billing.Labels["team"] != "b" {
		t.Fatalf("restored server: %v %+v", err, billing)
	}
	if out, _, code := run2("import", "--wait", "0", file); code != 0 || strings.Count(out, "unchanged") != 2 {
		t.Fatalf("second import: exit %d\n%s", code, out)
	}

	// Someone changed billing's endpoint: the import refuses, and the new
	// server in the same file is not created either.
	billing.Spec.Endpoint = "http://billing.team-b.svc:9090"
	if err := target.Update(context.Background(), billing); err != nil {
		t.Fatal(err)
	}
	if err := target.Delete(context.Background(), mcpServer("team-a", "search")); err != nil {
		t.Fatal(err)
	}
	out, _, code := run2("import", "--wait", "0", file)
	if code != 1 || !strings.Contains(out, "refused team-b/billing") || !strings.Contains(out, "nothing was changed") {
		t.Fatalf("differing server: exit %d\n%s", code, out)
	}
	if err := target.Get(context.Background(), client.ObjectKey{Namespace: "team-a", Name: "search"}, &kavryntv1alpha1.MCPServer{}); !apierrors.IsNotFound(err) {
		t.Fatalf("a refused import created a server: %v", err)
	}
	if out, _, code := run2("import", "--overwrite", "--wait", "0", file); code != 0 || !strings.Contains(out, "overwrote team-b/billing") || !strings.Contains(out, "created team-a/search") {
		t.Fatalf("--overwrite: exit %d\n%s", code, out)
	}
}

// Verifies SPEC-0034-R04 and SPEC-0034-R07: an invalid document, two
// documents for one server, a redacted environment, and a missing
// namespace each refuse the whole file before anything is written.
func TestImportRefusesBeforeWriting(t *testing.T) {
	valid := "apiVersion: kavrynt.io/v1alpha1\nkind: MCPServer\nmetadata: {name: search, namespace: team-a}\nspec: {version: 1.0.0, transport: http, endpoint: http://search.team-a.svc:8080}\n"
	cases := map[string]struct{ second, want string }{
		"invalid":           {"apiVersion: kavrynt.io/v1alpha1\nkind: MCPServer\nmetadata: {name: bad, namespace: team-a}\nspec: {version: 1.0.0, transport: ftp}\n", "document 2"},
		"duplicate":         {valid, "named by two documents"},
		"redacted":          {"apiVersion: kavrynt.io/v1alpha1\nkind: MCPServer\nmetadata: {name: env, namespace: team-a, annotations: {kavrynt.io/redacted-environment: API_KEY}}\nspec: {version: 1.0.0, transport: http, endpoint: http://env.team-a.svc:8080, environment: {API_KEY: \"\"}}\n", "fill in API_KEY"},
		"missing namespace": {"apiVersion: kavrynt.io/v1alpha1\nkind: MCPServer\nmetadata: {name: x, namespace: team-z}\nspec: {version: 1.0.0, transport: http, endpoint: http://x.team-z.svc:8080}\n", "namespace team-z does not exist"},
	}
	for name, tc := range cases {
		target := useBackupClient(t, interceptor.Funcs{}, namespace("team-a"))
		file := filepath.Join(t.TempDir(), "in.yaml")
		if err := os.WriteFile(file, []byte(valid+"---\n"+tc.second), 0o600); err != nil {
			t.Fatal(err)
		}
		out, _, code := run2("import", "--wait", "0", file)
		var list kavryntv1alpha1.MCPServerList
		_ = target.List(context.Background(), &list)
		if code != 1 || !strings.Contains(out, tc.want) || len(list.Items) != 0 {
			t.Errorf("%s: exit %d, %d servers written\n%s", name, code, len(list.Items), out)
		}
	}
}

// Verifies SPEC-0034-R06: --dry-run reports what would happen and changes
// nothing.
func TestImportDryRun(t *testing.T) {
	useBackupClient(t, interceptor.Funcs{}, sourceCluster()...)
	file := filepath.Join(t.TempDir(), "backup.yaml")
	if _, errOut, code := run2("export", "-A", "--include-environment", "-o", file); code != 0 {
		t.Fatal(errOut)
	}
	target := useBackupClient(t, interceptor.Funcs{}, namespace("team-a"), namespace("team-b"))
	out, _, code := run2("import", "--dry-run", file)
	var list kavryntv1alpha1.MCPServerList
	_ = target.List(context.Background(), &list)
	if code != 0 || strings.Count(out, "would create") != 2 || len(list.Items) != 0 {
		t.Fatalf("dry run: exit %d, %d servers written\n%s", code, len(list.Items), out)
	}
}

// Verifies SPEC-0034-R04: a server changed between the check and the
// write is refused and named.
func TestImportConflict(t *testing.T) {
	existing := mcpServer("team-a", "search")
	// The check's dry run passes; someone changes the server before the
	// real update.
	conflict := interceptor.Funcs{Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
		if slices.Contains((&client.UpdateOptions{}).ApplyOptions(opts).DryRun, metav1.DryRunAll) {
			return nil
		}
		return apierrors.NewConflict(schema.GroupResource{Group: "kavrynt.io", Resource: "mcpservers"}, "search", errors.New("changed"))
	}}
	useBackupClient(t, conflict, namespace("team-a"), existing)
	file := filepath.Join(t.TempDir(), "in.yaml")
	doc := "apiVersion: kavrynt.io/v1alpha1\nkind: MCPServer\nmetadata: {name: search, namespace: team-a}\nspec: {version: 2.0.0, transport: http, endpoint: http://search.team-a.svc:8080}\n"
	if err := os.WriteFile(file, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, _, code := run2("import", "--overwrite", "--wait", "0", file); code != 1 || !strings.Contains(out, "changed by someone else") {
		t.Fatalf("conflict: exit %d\n%s", code, out)
	}
}

// Verifies SPEC-0034-R08: after importing, the servers that are not Ready
// are reported with their reason, a Ready server without its credential
// names the Secret keys to add, and a Ready server counts without
// ToolsDiscovered, which the Operator leaves out when discovery is off.
func TestImportReportsReadiness(t *testing.T) {
	status := map[string][]metav1.Condition{
		"search":  {condition(kavryntv1alpha1.ConditionReady, "True", kavryntv1alpha1.ReasonRoutable, ""), condition(kavryntv1alpha1.ConditionToolsDiscovered, "False", kavryntv1alpha1.ReasonCredentialUnavailable, "")},
		"billing": {condition(kavryntv1alpha1.ConditionReady, "False", kavryntv1alpha1.ReasonEndpointNotAllowed, "endpoint reaches team-x")},
		"docs":    {condition(kavryntv1alpha1.ConditionReady, "True", kavryntv1alpha1.ReasonRoutable, "")},
	}
	withStatus := interceptor.Funcs{Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
		if err := c.Get(ctx, key, obj, opts...); err != nil {
			return err
		}
		if server, ok := obj.(*kavryntv1alpha1.MCPServer); ok {
			server.Status.Conditions = status[key.Name]
		}
		return nil
	}}
	useBackupClient(t, withStatus, namespace("team-a"))
	file := filepath.Join(t.TempDir(), "in.yaml")
	doc := "apiVersion: kavrynt.io/v1alpha1\nkind: MCPServer\nmetadata: {name: %s, namespace: team-a}\nspec: {version: 1.0.0, transport: http, endpoint: http://%s.team-a.svc:8080}\n"
	data := strings.ReplaceAll(doc, "%s", "search") + "---\n" + strings.ReplaceAll(doc, "%s", "billing") + "---\n" + strings.ReplaceAll(doc, "%s", "docs")
	if err := os.WriteFile(file, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	out, _, code := run2("import", "--wait", "1s", file)
	if code != 0 || !strings.Contains(out, "add team-a.search and team-a.search.origin") ||
		!strings.Contains(out, "team-a/billing is not Ready after 1s: "+kavryntv1alpha1.ReasonEndpointNotAllowed) || strings.Contains(out, "team-x") ||
		strings.Contains(out, "team-a/docs is") {
		t.Fatalf("readiness report: exit %d\n%s", code, out)
	}
}

// Verifies SPEC-0034-R01 and SPEC-0034-R09: spec.args are written empty and
// counted unless --include-args, which warns; -o makes an existing file
// private before writing.
func TestExportRedactsArgsAndKeepsFilesPrivate(t *testing.T) {
	stdio := mcpServer("team-a", "tool")
	stdio.Spec = kavryntv1alpha1.MCPServerSpec{Version: "1.0.0", Transport: "stdio", Command: "tool", Args: []string{"--token=args-s3cret", "--verbose"}}
	useBackupClient(t, interceptor.Funcs{}, namespace("team-a"), stdio)
	out, _, code := run2("export", "-n", "team-a")
	if code != 0 || strings.Contains(out, "args-s3cret") || !strings.Contains(out, redactedArgsAnnotation+": \"2\"") {
		t.Fatalf("default export: exit %d\n%s", code, out)
	}
	if out, errOut, _ := run2("export", "-n", "team-a", "--include-args"); !strings.Contains(out, "args-s3cret") || !strings.Contains(errOut, "may hold credentials") {
		t.Fatalf("--include-args: %s %s", out, errOut)
	}

	file := filepath.Join(t.TempDir(), "existing.yaml")
	if err := os.WriteFile(file, []byte(strings.Repeat("old content\n", 100)), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, errOut, code := run2("export", "-n", "team-a", "-o", file); code != 0 {
		t.Fatal(errOut)
	}
	info, _ := os.Stat(file)
	data, _ := os.ReadFile(file)
	if info.Mode().Perm() != 0o600 || strings.Contains(string(data), "old content") {
		t.Fatalf("existing file: mode %v\n%s", info.Mode(), data)
	}
}

// Verifies SPEC-0034-R04, SPEC-0034-R05, SPEC-0034-R09, and SPEC-0034-R10:
// redacted args are refused; an overwrite the API server refuses in the
// dry run stops the whole import; a file over 16 MiB is refused unread.
func TestImportChecksBeforeOverwriting(t *testing.T) {
	refuseUpdate := interceptor.Funcs{Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
		if slices.Contains((&client.UpdateOptions{}).ApplyOptions(opts).DryRun, metav1.DryRunAll) {
			return apierrors.NewInvalid(schema.GroupKind{Group: "kavrynt.io", Kind: "MCPServer"}, "search", nil)
		}
		return c.Update(ctx, obj, opts...)
	}}
	target := useBackupClient(t, refuseUpdate, namespace("team-a"), mcpServer("team-a", "search"))
	dir := t.TempDir()
	file := filepath.Join(dir, "in.yaml")
	doc := "apiVersion: kavrynt.io/v1alpha1\nkind: MCPServer\nmetadata: {name: %s, namespace: team-a}\nspec: {version: 2.0.0, transport: http, endpoint: http://%s.team-a.svc:8080}\n"
	data := strings.ReplaceAll(doc, "%s", "search") + "---\n" + strings.ReplaceAll(doc, "%s", "new")
	if err := os.WriteFile(file, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	out, _, code := run2("import", "--overwrite", "--wait", "0", file)
	if code != 1 || !strings.Contains(out, "refuses the overwrite") {
		t.Fatalf("refused overwrite: exit %d\n%s", code, out)
	}
	if err := target.Get(context.Background(), client.ObjectKey{Namespace: "team-a", Name: "new"}, &kavryntv1alpha1.MCPServer{}); !apierrors.IsNotFound(err) {
		t.Fatalf("a refused overwrite let another document be written: %v", err)
	}

	args := "apiVersion: kavrynt.io/v1alpha1\nkind: MCPServer\nmetadata: {name: tool, namespace: team-a, annotations: {kavrynt.io/redacted-args: \"1\"}}\nspec: {version: 1.0.0, transport: stdio, command: tool, args: [\"\"]}\n"
	if err := os.WriteFile(file, []byte(args), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, _, code := run2("import", "--wait", "0", file); code != 1 || !strings.Contains(out, "spec.args were redacted") {
		t.Fatalf("redacted args: exit %d\n%s", code, out)
	}

	big := filepath.Join(dir, "big.yaml")
	if err := os.WriteFile(big, bytes.Repeat([]byte("#\n"), maxImportBytes/2+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, errOut, code := run2("import", big); code != 1 || !strings.Contains(errOut, "larger than 16 MiB") {
		t.Fatalf("large file: exit %d %s", code, errOut)
	}
}

// Verifies SPEC-0034-R08: after an overwrite, a Ready condition of the old
// generation does not count.
func TestImportWaitsForTheNewGeneration(t *testing.T) {
	stale := interceptor.Funcs{Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
		if err := c.Get(ctx, key, obj, opts...); err != nil {
			return err
		}
		if server, ok := obj.(*kavryntv1alpha1.MCPServer); ok {
			server.Generation = 2
			ready := condition(kavryntv1alpha1.ConditionReady, "True", kavryntv1alpha1.ReasonRoutable, "")
			ready.ObservedGeneration = 1
			server.Status.Conditions = []metav1.Condition{ready, condition(kavryntv1alpha1.ConditionToolsDiscovered, "True", kavryntv1alpha1.ReasonDiscovered, "")}
		}
		return nil
	}}
	useBackupClient(t, stale, namespace("team-a"), mcpServer("team-a", "search"))
	file := filepath.Join(t.TempDir(), "in.yaml")
	doc := "apiVersion: kavrynt.io/v1alpha1\nkind: MCPServer\nmetadata: {name: search, namespace: team-a}\nspec: {version: 2.0.0, transport: http, endpoint: http://search.team-a.svc:8080}\n"
	if err := os.WriteFile(file, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, _, _ := run2("import", "--overwrite", "--wait", "1s", file); !strings.Contains(out, "team-a/search is not Ready after 1s: Pending") || strings.Contains(out, "every imported MCPServer is Ready") {
		t.Fatalf("stale Ready counted:\n%s", out)
	}
}
