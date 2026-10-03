package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	kavryntv1alpha1 "github.com/kavrynt/kavrynt/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
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
	if code != 0 || !strings.Contains(out, "payments") || !strings.Contains(out, "/mcp/team-a.payments") || !strings.Contains(out, "0.2.0") {
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
			Status: kavryntv1alpha1.MCPServerStatus{Conditions: []metav1.Condition{{
				Type: kavryntv1alpha1.ConditionReady, Status: metav1.ConditionTrue, Reason: "Routable", LastTransitionTime: metav1.Now(),
			}}},
		},
		&kavryntv1alpha1.MCPServer{
			ObjectMeta: metav1.ObjectMeta{Name: "b", Namespace: "team-b"},
			Spec:       kavryntv1alpha1.MCPServerSpec{Version: "1", Transport: "stdio", Command: "b"},
		},
	)

	out, errOut, code := run(t, "list", "-A")
	if code != 0 {
		t.Fatalf("list -A: code=%d err=%q", code, errOut)
	}
	for _, want := range []string{"team-a", "team-b", "True", "Unknown"} {
		if !strings.Contains(out, want) {
			t.Fatalf("list -A output missing %q:\n%s", want, out)
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
