package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	kavryntv1alpha1 "github.com/kavrynt/kavrynt/api/v1alpha1"
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

// doctorCluster is a healthy installation: the CRD, a ready Gateway with
// client authentication, a ready Operator, both at kavryctl's version.
func doctorCluster(version string, gatewayArgs ...string) []client.Object {
	deployment := func(name, component string, args []string) *appsv1.Deployment {
		replicas := int32(1)
		return &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "kavrynt-system",
				Labels: map[string]string{"app.kubernetes.io/name": component, "app.kubernetes.io/instance": "kavrynt"}},
			Spec: appsv1.DeploymentSpec{Replicas: &replicas, Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
				Containers: []corev1.Container{{Name: component, Image: "docker.io/kavrynt/" + component + ":" + version, Args: args}}}}},
			Status: appsv1.DeploymentStatus{ReadyReplicas: 1},
		}
	}
	if gatewayArgs == nil {
		gatewayArgs = []string{"--auth", "--auth-resource-url", "https://mcp.example.com"}
	}
	return []client.Object{
		&apiextensionsv1.CustomResourceDefinition{ObjectMeta: metav1.ObjectMeta{Name: "mcpservers.kavrynt.io"},
			Spec: apiextensionsv1.CustomResourceDefinitionSpec{Versions: []apiextensionsv1.CustomResourceDefinitionVersion{{Name: "v1alpha1", Served: true, Storage: true}}}},
		deployment("kavrynt-gateway", "gateway", gatewayArgs),
		deployment("kavrynt-operator", "operator", nil),
	}
}

func mcpServer(namespace, name string, conditions ...metav1.Condition) *kavryntv1alpha1.MCPServer {
	return &kavryntv1alpha1.MCPServer{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec:       kavryntv1alpha1.MCPServerSpec{Version: "1.0.0", Transport: "http", Endpoint: "http://" + name + ".internal-host.svc:8080"},
		Status:     kavryntv1alpha1.MCPServerStatus{Conditions: conditions},
	}
}

func condition(kind, status, reason, message string) metav1.Condition {
	return metav1.Condition{Type: kind, Status: metav1.ConditionStatus(status), Reason: reason, Message: message}
}

// useDoctorClient serves objects through a fake client that fails the test
// on any write or any Secret access (SPEC-0033-R01), with extra interceptors.
func useDoctorClient(t *testing.T, funcs interceptor.Funcs, objects ...client.Object) {
	t.Helper()
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{kavryntv1alpha1.AddToScheme, apiextensionsv1.AddToScheme, appsv1.AddToScheme, corev1.AddToScheme} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}
	write := func() error { t.Error("doctor made a write request"); return errors.New("write") }
	guard := interceptor.Funcs{
		Create: func(context.Context, client.WithWatch, client.Object, ...client.CreateOption) error { return write() },
		Update: func(context.Context, client.WithWatch, client.Object, ...client.UpdateOption) error { return write() },
		Patch: func(context.Context, client.WithWatch, client.Object, client.Patch, ...client.PatchOption) error {
			return write()
		},
		Delete: func(context.Context, client.WithWatch, client.Object, ...client.DeleteOption) error { return write() },
		Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if _, secret := obj.(*corev1.Secret); secret {
				t.Error("doctor read a Secret")
			}
			if funcs.Get != nil {
				return funcs.Get(ctx, c, key, obj, opts...)
			}
			return c.Get(ctx, key, obj, opts...)
		},
		List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			if _, secrets := list.(*corev1.SecretList); secrets {
				t.Error("doctor listed Secrets")
			}
			if funcs.List != nil {
				return funcs.List(ctx, c, list, opts...)
			}
			return c.List(ctx, list, opts...)
		},
	}
	k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).WithInterceptorFuncs(guard).Build()
	original := newKubeClient
	newKubeClient = func(opts kubeOptions) (client.Client, string, error) { return k8sClient, opts.namespace, nil }
	t.Cleanup(func() { newKubeClient = original })
}

func doctorJSON(t *testing.T, args ...string) (doctorReport, string, int) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := Execute(append([]string{"doctor", "--output", "json"}, args...), &out, &errOut)
	var report doctorReport
	if code != 2 {
		if err := json.Unmarshal(out.Bytes(), &report); err != nil {
			t.Fatalf("not JSON: %v\n%s\n%s", err, out.String(), errOut.String())
		}
	}
	return report, out.String(), code
}

func checkOf(report doctorReport, id, subject string) doctorCheck {
	for _, c := range report.Checks {
		if c.ID == id && (subject == "" || strings.Contains(c.Subject, subject)) {
			return c
		}
	}
	return doctorCheck{}
}

// Verifies SPEC-0033-R01, SPEC-0033-R03, SPEC-0033-R04, and SPEC-0033-R06: a
// healthy installation is all ok with exit 0, read-only, as one
// kavrynt.doctor.v1 JSON object with stable check IDs.
func TestDoctorHealthyInstallation(t *testing.T) {
	ready := condition(kavryntv1alpha1.ConditionReady, "True", kavryntv1alpha1.ReasonRoutable, "")
	discovered := condition(kavryntv1alpha1.ConditionToolsDiscovered, "True", kavryntv1alpha1.ReasonDiscovered, "")
	useDoctorClient(t, interceptor.Funcs{}, append(doctorCluster(Version), mcpServer("team-a", "search", ready, discovered))...)
	report, _, code := doctorJSON(t)
	if code != 0 || report.Type != "kavrynt.doctor.v1" || report.Versions["gateway"] != Version {
		t.Fatalf("exit %d, report %+v", code, report)
	}
	for _, id := range []string{"crd", "gateway", "operator", "versions", "auth", "server.ready", "server.discovery"} {
		if c := checkOf(report, id, ""); c.Result != resultOK {
			t.Errorf("%s = %+v, want ok", id, c)
		}
	}
}

// Verifies SPEC-0033-R03, SPEC-0033-R04, SPEC-0033-R05, and SPEC-0033-R07: a
// server out of scope fails with exit 1 and a link; a failed discovery and
// open MCP routes only warn; no condition message or endpoint is printed.
func TestDoctorFailuresAndWarnings(t *testing.T) {
	ready := condition(kavryntv1alpha1.ConditionReady, "True", kavryntv1alpha1.ReasonRoutable, "")
	objects := append(doctorCluster(Version, "--strict-protocol=true"),
		mcpServer("team-a", "billing", condition(kavryntv1alpha1.ConditionReady, "False", kavryntv1alpha1.ReasonEndpointNotAllowed, "endpoint billing.team-b.svc reaches namespace team-b")),
		mcpServer("team-a", "search", ready, condition(kavryntv1alpha1.ConditionToolsDiscovered, "False", kavryntv1alpha1.ReasonDiscoveryFailed, "initialize: cannot connect to search.internal-host.svc:8080")))
	useDoctorClient(t, interceptor.Funcs{}, objects...)
	report, out, code := doctorJSON(t)
	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if c := checkOf(report, "server.ready", "billing"); c.Result != resultFail || c.Reason != kavryntv1alpha1.ReasonEndpointNotAllowed || c.Fix == "" || !strings.HasPrefix(c.Link, "https://") {
		t.Errorf("out-of-scope server: %+v", c)
	}
	if c := checkOf(report, "server.discovery", "search"); c.Result != resultWarn || c.Reason != kavryntv1alpha1.ReasonDiscoveryFailed || c.Link == "" {
		t.Errorf("failed discovery: %+v", c)
	}
	if c := checkOf(report, "auth", ""); c.Result != resultWarn || c.Link == "" {
		t.Errorf("auth off: %+v", c)
	}
	for _, leak := range []string{"internal-host", "cannot connect", "team-b.svc", ":8080", "mcp.example.com"} {
		if strings.Contains(out, leak) {
			t.Errorf("report contains %q:\n%s", leak, out)
		}
	}

	// Warnings alone exit 0, and the table shows each fix and link.
	useDoctorClient(t, interceptor.Funcs{}, append(doctorCluster(Version, "--strict-protocol=true"),
		mcpServer("team-a", "search", ready, condition(kavryntv1alpha1.ConditionToolsDiscovered, "False", kavryntv1alpha1.ReasonDiscoveryFailed, "x")))...)
	var table, errOut bytes.Buffer
	if code := Execute([]string{"doctor"}, &table, &errOut); code != 0 || !strings.Contains(table.String(), "KavryntMCPToolDiscoveryFailing.md") || !strings.Contains(table.String(), "0 fail") {
		t.Fatalf("warnings only: exit %d\n%s%s", code, table.String(), errOut.String())
	}
}

// Verifies SPEC-0033-R04: an Operator installed from the subchart alone
// (app.kubernetes.io/name k8s-operator) is found too.
func TestDoctorFindsAStandaloneOperator(t *testing.T) {
	objects := doctorCluster(Version)
	objects[2].(*appsv1.Deployment).Labels["app.kubernetes.io/name"] = "k8s-operator"
	useDoctorClient(t, interceptor.Funcs{}, objects...)
	if report, _, code := doctorJSON(t); code != 0 || checkOf(report, "operator", "").Result != resultOK {
		t.Fatalf("standalone operator: exit %d %+v", code, checkOf(report, "operator", ""))
	}
}

// Verifies SPEC-0033-R04: the Operator and Gateway at different versions
// fail; kavryctl at another version only warns.
func TestDoctorVersions(t *testing.T) {
	objects := doctorCluster("1.0.0-beta.3")
	objects[2].(*appsv1.Deployment).Spec.Template.Spec.Containers[0].Image = "docker.io/kavrynt/operator:1.0.0-beta.2"
	useDoctorClient(t, interceptor.Funcs{}, objects...)
	if report, _, code := doctorJSON(t); code != 1 || checkOf(report, "versions", "").Result != resultFail {
		t.Fatalf("mismatch: exit %d %+v", code, checkOf(report, "versions", ""))
	}
	useDoctorClient(t, interceptor.Funcs{}, doctorCluster("1.0.0-beta.3")...)
	if report, _, code := doctorJSON(t); code != 0 || checkOf(report, "versions", "").Result != resultWarn {
		t.Fatalf("kavryctl differs: exit %d %+v", code, checkOf(report, "versions", ""))
	}
}

// Verifies SPEC-0033-R02 and SPEC-0033-R03: a forbidden Deployment list skips
// those checks by name and the MCPServer checks still run; an unreachable
// cluster exits 2.
func TestDoctorSkipsWhatItMayNotRead(t *testing.T) {
	forbidden := interceptor.Funcs{List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
		if _, ok := list.(*appsv1.DeploymentList); ok {
			return apierrors.NewForbidden(schema.GroupResource{Group: "apps", Resource: "deployments"}, "", errors.New("no"))
		}
		return c.List(ctx, list, opts...)
	}}
	ready := condition(kavryntv1alpha1.ConditionReady, "True", kavryntv1alpha1.ReasonRoutable, "")
	useDoctorClient(t, forbidden, append(doctorCluster(Version), mcpServer("team-a", "search", ready))...)
	report, _, code := doctorJSON(t)
	if c := checkOf(report, "gateway", ""); c.Result != resultSkipped || !strings.Contains(c.Reason, "list deployments") {
		t.Errorf("gateway: %+v", c)
	}
	if checkOf(report, "server.ready", "search").Result != resultOK || code != 0 {
		t.Errorf("servers not checked, or exit %d", code)
	}

	down := interceptor.Funcs{Get: func(context.Context, client.WithWatch, client.ObjectKey, client.Object, ...client.GetOption) error {
		return errors.New("dial tcp 127.0.0.1:6443: connect: connection refused")
	}}
	useDoctorClient(t, down)
	if _, _, code := doctorJSON(t); code != 2 {
		t.Fatalf("unreachable cluster: exit %d, want 2", code)
	}
}

// Verifies SPEC-0033-R08: --anonymize hides namespaces and names behind
// per-report HMACs, stable within a report and different across reports.
func TestDoctorAnonymize(t *testing.T) {
	ready := condition(kavryntv1alpha1.ConditionReady, "True", kavryntv1alpha1.ReasonRoutable, "")
	useDoctorClient(t, interceptor.Funcs{}, append(doctorCluster(Version),
		mcpServer("default", "search", ready), mcpServer("default", "billing", ready))...)
	first, out, _ := doctorJSON(t, "--anonymize")
	for _, name := range []string{"default", "search", "billing", "kavrynt-system"} {
		if strings.Contains(out, `"`+name) || strings.Contains(out, "/"+name) {
			t.Errorf("anonymized report names %q:\n%s", name, out)
		}
	}
	a, b := checkOf(first, "server.ready", ""), first.Checks[len(first.Checks)-1]
	nsA, _, _ := strings.Cut(a.Subject, "/")
	nsB, _, _ := strings.Cut(b.Subject, "/")
	if len(nsA) != 12 || nsA != nsB {
		t.Fatalf("the same namespace should give one 12-character value: %q %q", a.Subject, b.Subject)
	}
	second, _, _ := doctorJSON(t, "--anonymize")
	if ns2, _, _ := strings.Cut(checkOf(second, "server.ready", "").Subject, "/"); ns2 == nsA {
		t.Fatal("two reports gave default the same value")
	}

	names, err := newAnonymizer()
	if err != nil {
		t.Fatal(err)
	}
	taken := names.token("team-a")
	names.owners[strings.Repeat("0", 12)] = "other" // not a real collision
	delete(names.owners, taken)
	names.owners[taken] = "someone-else"
	delete(names.tokens, "team-a")
	if longer := names.token("team-a"); len(longer) != 16 || !strings.HasPrefix(longer, taken) {
		t.Fatalf("a collision should lengthen the value: %q from %q", longer, taken)
	}
}
