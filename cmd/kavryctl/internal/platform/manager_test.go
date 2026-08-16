package platform

import (
	"bytes"
	"context"
	"errors"
	"io"
	"reflect"
	"testing"
	"time"
)

type invocation struct {
	name string
	args []string
}

type fakeRunner struct {
	missing map[string]bool
	calls   []invocation
}

func (runner *fakeRunner) LookPath(name string) error {
	if runner.missing[name] {
		return errors.New("not found")
	}
	return nil
}

func (runner *fakeRunner) Run(_ context.Context, _, _ io.Writer, name string, args ...string) error {
	runner.calls = append(runner.calls, invocation{name: name, args: append([]string(nil), args...)})
	return nil
}

func TestInstallUsesMatchingChartAndImages(t *testing.T) {
	runner := &fakeRunner{}
	manager := NewManager(runner)
	opts := InstallOptions{
		ChartOptions: ChartOptions{
			Version:     "v0.4.0",
			KubeContext: "staging",
			ValuesFiles: []string{"team.yaml"},
			SetValues:   []string{"gateway.replicaCount=2"},
			Timeout:     10 * time.Minute,
		},
		Wait: true,
	}

	if err := manager.Install(context.Background(), &bytes.Buffer{}, &bytes.Buffer{}, opts); err != nil {
		t.Fatal(err)
	}
	want := invocation{name: "helm", args: []string{
		"upgrade", "--install", "kavrynt", DefaultChart, "--namespace", "kavrynt-system", "--create-namespace",
		"--version", "0.4.0",
		"--set", "registry.image.repository=ghcr.io/kavrynt/registry",
		"--set", "registry.image.tag=0.4.0",
		"--set", "gateway.image.repository=ghcr.io/kavrynt/gateway",
		"--set", "gateway.image.tag=0.4.0",
		"--set", "operator.image.repository=ghcr.io/kavrynt/k8s-operator",
		"--set", "operator.image.tag=0.4.0",
		"--kube-context", "staging", "--values", "team.yaml", "--set", "gateway.replicaCount=2",
		"--timeout", "10m0s", "--wait",
	}}
	if !reflect.DeepEqual(runner.calls, []invocation{want}) {
		t.Fatalf("unexpected calls:\n got: %#v\nwant: %#v", runner.calls, []invocation{want})
	}
}

func TestGenerateFromLocalChartDoesNotOverrideImages(t *testing.T) {
	runner := &fakeRunner{}
	manager := NewManager(runner)
	opts := ChartOptions{Chart: "../../../charts/kavrynt", Version: "0.4.0"}

	if err := manager.Generate(context.Background(), io.Discard, io.Discard, opts); err != nil {
		t.Fatal(err)
	}
	want := invocation{name: "helm", args: []string{
		"template", "kavrynt", "../../../charts/kavrynt", "--namespace", "kavrynt-system", "--include-crds",
		"--version", "0.4.0", "--timeout", "5m0s",
	}}
	if !reflect.DeepEqual(runner.calls, []invocation{want}) {
		t.Fatalf("unexpected calls:\n got: %#v\nwant: %#v", runner.calls, []invocation{want})
	}
}

func TestStatusChecksWorkloadsAndMCPServers(t *testing.T) {
	runner := &fakeRunner{}
	manager := NewManager(runner)

	if err := manager.Status(context.Background(), io.Discard, io.Discard, StatusOptions{KubeContext: "dev"}); err != nil {
		t.Fatal(err)
	}
	want := []invocation{
		{name: "kubectl", args: []string{"--context", "dev", "get", "deployments,services", "--namespace", "kavrynt-system"}},
		{name: "kubectl", args: []string{"--context", "dev", "get", "mcpservers", "--all-namespaces"}},
	}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("unexpected calls:\n got: %#v\nwant: %#v", runner.calls, want)
	}
}

func TestUninstallCanPurgeClusterResources(t *testing.T) {
	runner := &fakeRunner{}
	manager := NewManager(runner)
	opts := UninstallOptions{Purge: true, DeleteNamespace: true, Wait: true}

	if err := manager.Uninstall(context.Background(), io.Discard, io.Discard, opts); err != nil {
		t.Fatal(err)
	}
	want := []invocation{
		{name: "helm", args: []string{"uninstall", "kavrynt", "--namespace", "kavrynt-system", "--timeout", "5m0s", "--wait"}},
		{name: "kubectl", args: []string{"delete", "crd", "mcpservers.kavrynt.io", "--ignore-not-found=true"}},
		{name: "kubectl", args: []string{"delete", "namespace", "kavrynt-system", "--ignore-not-found=true"}},
	}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("unexpected calls:\n got: %#v\nwant: %#v", runner.calls, want)
	}
}

func TestInstallReportsMissingHelm(t *testing.T) {
	manager := NewManager(&fakeRunner{missing: map[string]bool{"helm": true}})
	err := manager.Install(context.Background(), io.Discard, io.Discard, InstallOptions{})
	if err == nil || err.Error() != `required command "helm" was not found in PATH` {
		t.Fatalf("unexpected error: %v", err)
	}
}
