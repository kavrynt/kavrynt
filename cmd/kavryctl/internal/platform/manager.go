package platform

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"
)

const (
	DefaultChart       = "oci://ghcr.io/kavrynt/charts/kavrynt"
	DefaultReleaseName = "kavrynt"
	DefaultNamespace   = "kavrynt-system"
)

type Runner interface {
	LookPath(name string) error
	Run(ctx context.Context, stdout, stderr io.Writer, name string, args ...string) error
}

type OSRunner struct{}

func (OSRunner) LookPath(name string) error {
	_, err := exec.LookPath(name)
	return err
}

func (OSRunner) Run(ctx context.Context, stdout, stderr io.Writer, name string, args ...string) error {
	command := exec.CommandContext(ctx, name, args...)
	command.Stdout = stdout
	command.Stderr = stderr
	return command.Run()
}

type Manager struct {
	runner Runner
}

func NewManager(runner Runner) *Manager {
	if runner == nil {
		runner = OSRunner{}
	}
	return &Manager{runner: runner}
}

type ChartOptions struct {
	ReleaseName string
	Namespace   string
	Chart       string
	Version     string
	KubeContext string
	ValuesFiles []string
	SetValues   []string
	Timeout     time.Duration
}

type InstallOptions struct {
	ChartOptions
	Wait   bool
	DryRun bool
}

type UninstallOptions struct {
	ReleaseName     string
	Namespace       string
	KubeContext     string
	Timeout         time.Duration
	Wait            bool
	Purge           bool
	DeleteNamespace bool
}

type StatusOptions struct {
	Namespace   string
	KubeContext string
}

func (m *Manager) Install(ctx context.Context, stdout, stderr io.Writer, opts InstallOptions) error {
	if err := m.require("helm"); err != nil {
		return err
	}
	opts.ChartOptions = normalizeChartOptions(opts.ChartOptions)

	args := []string{"upgrade", "--install", opts.ReleaseName, opts.Chart, "--namespace", opts.Namespace, "--create-namespace"}
	args = appendChartOptions(args, opts.ChartOptions)
	if opts.Wait {
		args = append(args, "--wait")
	}
	if opts.DryRun {
		args = append(args, "--dry-run")
	}
	if err := m.runner.Run(ctx, stdout, stderr, "helm", args...); err != nil {
		return fmt.Errorf("install Kavrynt: %w", err)
	}
	return nil
}

func (m *Manager) Generate(ctx context.Context, stdout, stderr io.Writer, opts ChartOptions) error {
	if err := m.require("helm"); err != nil {
		return err
	}
	opts = normalizeChartOptions(opts)

	args := []string{"template", opts.ReleaseName, opts.Chart, "--namespace", opts.Namespace, "--include-crds"}
	args = appendChartOptions(args, opts)
	if err := m.runner.Run(ctx, stdout, stderr, "helm", args...); err != nil {
		return fmt.Errorf("generate Kavrynt manifest: %w", err)
	}
	return nil
}

func (m *Manager) Status(ctx context.Context, stdout, stderr io.Writer, opts StatusOptions) error {
	if err := m.require("kubectl"); err != nil {
		return err
	}
	if strings.TrimSpace(opts.Namespace) == "" {
		opts.Namespace = DefaultNamespace
	}

	base := kubectlContextArgs(opts.KubeContext)
	commands := [][]string{
		append(append([]string{}, base...), "get", "deployments,services", "--namespace", opts.Namespace),
		append(append([]string{}, base...), "get", "mcpservers", "--all-namespaces"),
	}
	for _, args := range commands {
		if err := m.runner.Run(ctx, stdout, stderr, "kubectl", args...); err != nil {
			return fmt.Errorf("inspect Kavrynt status: %w", err)
		}
	}
	return nil
}

func (m *Manager) Uninstall(ctx context.Context, stdout, stderr io.Writer, opts UninstallOptions) error {
	if err := m.require("helm"); err != nil {
		return err
	}
	if strings.TrimSpace(opts.ReleaseName) == "" {
		opts.ReleaseName = DefaultReleaseName
	}
	if strings.TrimSpace(opts.Namespace) == "" {
		opts.Namespace = DefaultNamespace
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 5 * time.Minute
	}

	args := []string{"uninstall", opts.ReleaseName, "--namespace", opts.Namespace, "--timeout", opts.Timeout.String()}
	if opts.KubeContext != "" {
		args = append(args, "--kube-context", opts.KubeContext)
	}
	if opts.Wait {
		args = append(args, "--wait")
	}
	if err := m.runner.Run(ctx, stdout, stderr, "helm", args...); err != nil {
		return fmt.Errorf("uninstall Kavrynt: %w", err)
	}

	if !opts.Purge && !opts.DeleteNamespace {
		return nil
	}
	if err := m.require("kubectl"); err != nil {
		return err
	}
	base := kubectlContextArgs(opts.KubeContext)
	if opts.Purge {
		args := append(append([]string{}, base...), "delete", "crd", "mcpservers.kavrynt.io", "--ignore-not-found=true")
		if err := m.runner.Run(ctx, stdout, stderr, "kubectl", args...); err != nil {
			return fmt.Errorf("purge Kavrynt cluster resources: %w", err)
		}
	}
	if opts.DeleteNamespace {
		args := append(append([]string{}, base...), "delete", "namespace", opts.Namespace, "--ignore-not-found=true")
		if err := m.runner.Run(ctx, stdout, stderr, "kubectl", args...); err != nil {
			return fmt.Errorf("delete Kavrynt namespace: %w", err)
		}
	}
	return nil
}

func (m *Manager) require(name string) error {
	if err := m.runner.LookPath(name); err != nil {
		return fmt.Errorf("required command %q was not found in PATH", name)
	}
	return nil
}

func normalizeChartOptions(opts ChartOptions) ChartOptions {
	if strings.TrimSpace(opts.ReleaseName) == "" {
		opts.ReleaseName = DefaultReleaseName
	}
	if strings.TrimSpace(opts.Namespace) == "" {
		opts.Namespace = DefaultNamespace
	}
	if strings.TrimSpace(opts.Chart) == "" {
		opts.Chart = DefaultChart
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 5 * time.Minute
	}
	return opts
}

func appendChartOptions(args []string, opts ChartOptions) []string {
	if opts.Version != "" {
		args = append(args, "--version", strings.TrimPrefix(opts.Version, "v"))
		if strings.HasPrefix(opts.Chart, "oci://ghcr.io/kavrynt/") {
			version := strings.TrimPrefix(opts.Version, "v")
			for _, value := range []string{
				"registry.image.repository=ghcr.io/kavrynt/registry",
				"registry.image.tag=" + version,
				"gateway.image.repository=ghcr.io/kavrynt/gateway",
				"gateway.image.tag=" + version,
				"operator.image.repository=ghcr.io/kavrynt/k8s-operator",
				"operator.image.tag=" + version,
			} {
				args = append(args, "--set", value)
			}
		}
	}
	if opts.KubeContext != "" {
		args = append(args, "--kube-context", opts.KubeContext)
	}
	for _, file := range opts.ValuesFiles {
		args = append(args, "--values", file)
	}
	for _, value := range opts.SetValues {
		args = append(args, "--set", value)
	}
	args = append(args, "--timeout", opts.Timeout.String())
	return args
}

func kubectlContextArgs(kubeContext string) []string {
	if strings.TrimSpace(kubeContext) == "" {
		return nil
	}
	return []string{"--context", kubeContext}
}
