package cli

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	kavryntv1alpha1 "github.com/kavrynt/kavrynt/api/v1alpha1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// fakeHelm records helm commands; fail makes the command named by its first
// argument fail, and after runs once an upgrade or rollback succeeds.
type fakeHelm struct {
	calls [][]string
	fail  map[string]error
	after func()
}

func (f *fakeHelm) Run(_ context.Context, args ...string) ([]byte, error) {
	f.calls = append(f.calls, args)
	if err := f.fail[args[0]]; err != nil {
		return nil, err
	}
	if (args[0] == "upgrade" || args[0] == "rollback") && f.after != nil {
		f.after()
	}
	return []byte("{}"), nil
}

// ran returns the helm command named by verb, or nil.
func (f *fakeHelm) ran(verb string) []string {
	for _, call := range f.calls {
		if call[0] == verb {
			return call
		}
	}
	return nil
}

func useHelm(t *testing.T, helm *fakeHelm) {
	t.Helper()
	original, interval := newHelm, routedPollInterval
	newHelm = func() (helmRunner, error) { return helm, nil }
	routedPollInterval = 5 * time.Millisecond
	t.Cleanup(func() { newHelm, routedPollInterval = original, interval })
}

func readyServer(namespace, name string) *kavryntv1alpha1.MCPServer {
	return &kavryntv1alpha1.MCPServer{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec:       kavryntv1alpha1.MCPServerSpec{Version: "1", Transport: "http", Endpoint: "http://" + name},
		Status: kavryntv1alpha1.MCPServerStatus{Conditions: []metav1.Condition{{
			Type: kavryntv1alpha1.ConditionReady, Status: metav1.ConditionTrue, Reason: kavryntv1alpha1.ReasonRoutable, LastTransitionTime: metav1.Now(),
		}}},
	}
}

func mcpCRD(owner string) *apiextensionsv1.CustomResourceDefinition {
	crd := &apiextensionsv1.CustomResourceDefinition{ObjectMeta: metav1.ObjectMeta{Name: crdName}}
	if owner != "" {
		crd.Labels = map[string]string{"app.kubernetes.io/managed-by": "Helm"}
		crd.Annotations = map[string]string{"meta.helm.sh/release-name": owner, "meta.helm.sh/release-namespace": defaultNamespace}
	}
	return crd
}

// Verifies SPEC-0019-R01.
func TestUpgradeNeedsHelm(t *testing.T) {
	original := newHelm
	newHelm = func() (helmRunner, error) {
		return nil, errors.New("kavryctl upgrade and rollback need the helm binary on PATH")
	}
	t.Cleanup(func() { newHelm = original })
	useFakeClient(t, defaultNamespace)
	for _, args := range [][]string{{"upgrade", "--version", "1.0.0-beta.1"}, {"rollback"}} {
		if _, errOut, code := run(t, args...); code != 1 || !strings.Contains(errOut, "need the helm binary on PATH") {
			t.Errorf("%s without helm: code=%d err=%q", args[0], code, errOut)
		}
	}
	t.Setenv("PATH", t.TempDir())
	newHelm = original
	if _, err := newHelm(); err == nil || !strings.Contains(err.Error(), "https://helm.sh/docs/intro/install/") {
		t.Fatalf("no helm on PATH: %v", err)
	}
}

// Verifies SPEC-0019-R02: each failed preflight check stops before any
// change.
func TestUpgradePreflightChangesNothing(t *testing.T) {
	for name, c := range map[string]struct {
		fail    map[string]error
		crd     *apiextensionsv1.CustomResourceDefinition
		args    []string
		message string
	}{
		"no release":       {map[string]error{"status": errors.New("release: not found")}, mcpCRD("kavrynt"), nil, "release kavrynt not found in namespace kavrynt-system"},
		"no such version":  {map[string]error{"show": errors.New("not found")}, mcpCRD("kavrynt"), nil, "version 9.9.9 not found"},
		"CRD not owned":    {nil, mcpCRD(""), nil, "kubectl label crd mcpservers.kavrynt.io app.kubernetes.io/managed-by=Helm"},
		"CRD of another":   {nil, mcpCRD("other"), nil, "--adopt-crd"},
		"version required": {nil, mcpCRD("kavrynt"), []string{"upgrade"}, "--version is required"},
	} {
		helm := &fakeHelm{fail: c.fail}
		useHelm(t, helm)
		k8sClient := useFakeClient(t, defaultNamespace, c.crd, readyServer("team-a", "search"))
		args := c.args
		if args == nil {
			args = []string{"upgrade", "--version", "9.9.9"}
		}
		_, errOut, code := run(t, args...)
		if code == 0 || !strings.Contains(errOut, c.message) {
			t.Errorf("%s: code=%d err=%q, want %q", name, code, errOut, c.message)
		}
		if helm.ran("upgrade") != nil {
			t.Errorf("%s: helm upgrade ran", name)
		}
		var crd apiextensionsv1.CustomResourceDefinition
		if err := k8sClient.Get(context.Background(), types.NamespacedName{Name: crdName}, &crd); err != nil || crd.Labels["app.kubernetes.io/managed-by"] != c.crd.Labels["app.kubernetes.io/managed-by"] {
			t.Errorf("%s: the CRD changed: %v %v", name, crd.Labels, err)
		}
	}
}

// Verifies SPEC-0019-R02 and SPEC-0019-R03: --adopt-crd adopts, the upgrade
// reuses the release's values, and every routed server is reported.
func TestUpgradeAdoptsAndReportsRoutedServers(t *testing.T) {
	helm := &fakeHelm{}
	useHelm(t, helm)
	k8sClient := useFakeClient(t, defaultNamespace, mcpCRD(""), readyServer("team-a", "search"), readyServer("team-b", "tickets"))
	out, errOut, code := run(t, "upgrade", "--version", "1.0.0-beta.1", "--adopt-crd", "--values", "local.yaml", "--context", "kind-test")
	if code != 0 {
		t.Fatalf("upgrade: code=%d out=%q err=%q", code, out, errOut)
	}
	want := []string{"upgrade", "kavrynt", defaultChart, "--version", "1.0.0-beta.1", "--reuse-values", "--values", "local.yaml",
		"--wait", "--timeout", "5m0s", "--namespace", "kavrynt-system", "--kube-context", "kind-test"}
	if got := helm.ran("upgrade"); !slices.Equal(got, want) {
		t.Fatalf("helm upgrade = %q\nwant %q", got, want)
	}
	var crd apiextensionsv1.CustomResourceDefinition
	if err := k8sClient.Get(context.Background(), types.NamespacedName{Name: crdName}, &crd); err != nil ||
		crd.Labels["app.kubernetes.io/managed-by"] != "Helm" || crd.Annotations["meta.helm.sh/release-name"] != "kavrynt" ||
		crd.Annotations["meta.helm.sh/release-namespace"] != "kavrynt-system" {
		t.Fatalf("the CRD was not adopted: %v %v %v", crd.Labels, crd.Annotations, err)
	}
	for _, line := range []string{"ok    team-a/search is routed at /mcp/team-a.search", "ok    team-b/tickets is routed at /mcp/team-b.tickets", "2 servers routed before are routed now"} {
		if !strings.Contains(out, line) {
			t.Errorf("output does not report %q:\n%s", line, out)
		}
	}
}

// Verifies SPEC-0019-R03: a server that is not routed after the upgrade is
// reported and fails the command.
func TestUpgradeFailsWhenAServerIsNoLongerRouted(t *testing.T) {
	helm := &fakeHelm{}
	useHelm(t, helm)
	k8sClient := useFakeClient(t, defaultNamespace, mcpCRD("kavrynt"), readyServer("team-a", "search"), readyServer("team-b", "tickets"))
	helm.after = func() {
		var server kavryntv1alpha1.MCPServer
		key := client.ObjectKey{Namespace: "team-b", Name: "tickets"}
		if err := k8sClient.Get(context.Background(), key, &server); err != nil {
			t.Fatal(err)
		}
		apimeta.SetStatusCondition(&server.Status.Conditions, metav1.Condition{Type: kavryntv1alpha1.ConditionReady, Status: metav1.ConditionFalse, Reason: "InvalidSpec"})
		if err := k8sClient.Status().Update(context.Background(), &server); err != nil {
			t.Fatal(err)
		}
	}
	out, errOut, code := run(t, "upgrade", "--chart", t.TempDir(), "--timeout", "50ms")
	if code != 1 || !strings.Contains(out, "FAIL  team-b/tickets was routed before and is not Ready now") ||
		!strings.Contains(out, "ok    team-a/search") || !strings.Contains(errOut, "1 of 2 servers routed before are not routed now") {
		t.Fatalf("code=%d out=%q err=%q", code, out, errOut)
	}
	if helm.ran("show") != nil {
		t.Fatal("a chart directory was looked up in a repository")
	}
}

// Verifies SPEC-0019-R04.
func TestRollbackChecksBeforeAndAfter(t *testing.T) {
	helm := &fakeHelm{}
	useHelm(t, helm)
	useFakeClient(t, defaultNamespace, readyServer("team-a", "search"))
	out, errOut, code := run(t, "rollback", "--revision", "3", "-n", "platform")
	if code != 0 || !strings.Contains(out, "1 servers routed before are routed now") {
		t.Fatalf("rollback: code=%d out=%q err=%q", code, out, errOut)
	}
	want := []string{"rollback", "kavrynt", "3", "--wait", "--timeout", "5m0s", "--namespace", "platform"}
	if got := helm.ran("rollback"); !slices.Equal(got, want) {
		t.Fatalf("helm rollback = %q, want %q", got, want)
	}
	if status := helm.ran("status"); status == nil || status[1] != "kavrynt" {
		t.Fatalf("the release was not checked first: %q", helm.calls)
	}

	missing := &fakeHelm{fail: map[string]error{"status": errors.New("not found")}}
	useHelm(t, missing)
	if _, errOut, code := run(t, "rollback"); code != 1 || !strings.Contains(errOut, "release kavrynt not found") || missing.ran("rollback") != nil {
		t.Fatalf("rollback of a missing release: code=%d err=%q calls=%q", code, errOut, missing.calls)
	}
}

// Verifies SPEC-0019-R05.
func TestDryRunChangesNothing(t *testing.T) {
	helm := &fakeHelm{}
	useHelm(t, helm)
	k8sClient := useFakeClient(t, defaultNamespace, mcpCRD(""), readyServer("team-a", "search"))
	out, errOut, code := run(t, "upgrade", "--version", "1.0.0-beta.1", "--adopt-crd", "--dry-run")
	if code != 0 || !strings.Contains(out, "would adopt the mcpservers.kavrynt.io CRD") ||
		!strings.Contains(out, "would run: helm upgrade kavrynt "+defaultChart+" --version 1.0.0-beta.1 --reuse-values") {
		t.Fatalf("upgrade --dry-run: code=%d out=%q err=%q", code, out, errOut)
	}
	out, _, code = run(t, "rollback", "--dry-run")
	if code != 0 || !strings.Contains(out, "would run: helm rollback kavrynt --wait") {
		t.Fatalf("rollback --dry-run: code=%d out=%q", code, out)
	}
	for _, call := range helm.calls {
		if call[0] == "upgrade" || call[0] == "rollback" {
			t.Fatalf("a dry run ran helm %s", call[0])
		}
	}
	var crd apiextensionsv1.CustomResourceDefinition
	if err := k8sClient.Get(context.Background(), types.NamespacedName{Name: crdName}, &crd); err != nil || crd.Labels != nil {
		t.Fatalf("a dry run changed the CRD: %v %v", crd.Labels, err)
	}
}
