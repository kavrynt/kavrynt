package cli

// kavryctl upgrade and rollback (SPEC-0019): the tested Helm upgrade and
// rollback path, with checks before (nothing changes when one fails) and
// after (every server routed before is routed again).

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	kavryntv1alpha1 "github.com/kavrynt/kavrynt/api/v1alpha1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	defaultRelease   = "kavrynt"
	defaultNamespace = "kavrynt-system"
	defaultChart     = "oci://ghcr.io/kavrynt/charts/kavrynt"
	crdName          = "mcpservers.kavrynt.io"
)

// routedPollInterval is how often the routed servers are checked after a
// change; tests shorten it.
var routedPollInterval = 2 * time.Second

// helmRunner runs the helm binary. Tests replace newHelm.
type helmRunner interface {
	Run(ctx context.Context, args ...string) (stdout []byte, err error)
}

type execHelm struct{ path string }

func (h execHelm) Run(ctx context.Context, args ...string) ([]byte, error) {
	var stdout, stderr bytes.Buffer
	// #nosec G204 -- the helm binary from PATH with arguments kavryctl builds; no shell is involved.
	cmd := exec.CommandContext(ctx, h.path, args...)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return stdout.Bytes(), errors.New(message)
	}
	return stdout.Bytes(), nil
}

// newHelm finds the helm binary (SPEC-0019-R01).
var newHelm = func() (helmRunner, error) {
	path, err := exec.LookPath("helm")
	if err != nil {
		return nil, errors.New("kavryctl upgrade and rollback need the helm binary on PATH: https://helm.sh/docs/intro/install/")
	}
	return execHelm{path: path}, nil
}

// releaseOptions are the flags shared by upgrade and rollback.
type releaseOptions struct {
	kube    kubeOptions
	release string
	timeout time.Duration
	dryRun  bool
}

func (o *releaseOptions) bind(fs *flag.FlagSet) {
	o.kube.bind(fs)
	fs.StringVar(&o.release, "release", defaultRelease, "Helm release name")
	fs.DurationVar(&o.timeout, "timeout", 5*time.Minute, "How long to wait for the rollout and for servers to be routed again")
	fs.BoolVar(&o.dryRun, "dry-run", false, "Run the checks and print the Helm command without changing anything")
}

// helmArgs adds the release namespace and cluster flags to a helm command.
func (o *releaseOptions) helmArgs(args ...string) []string {
	args = append(args, "--namespace", o.kube.namespace)
	if o.kube.context != "" {
		args = append(args, "--kube-context", o.kube.context)
	}
	if o.kube.kubeconfig != "" {
		args = append(args, "--kubeconfig", o.kube.kubeconfig)
	}
	return args
}

// session is what a release command needs once its flags are parsed.
type session struct {
	opts   releaseOptions
	helm   helmRunner
	kube   client.Client
	stdout io.Writer
	stderr io.Writer
}

func newSession(opts releaseOptions, stdout, stderr io.Writer) (*session, error) {
	if opts.kube.namespace == "" {
		opts.kube.namespace = defaultNamespace
	}
	helm, err := newHelm()
	if err != nil {
		return nil, err
	}
	kube, _, err := newKubeClient(opts.kube)
	if err != nil {
		return nil, err
	}
	return &session{opts: opts, helm: helm, kube: kube, stdout: stdout, stderr: stderr}, nil
}

func (s *session) check(format string, args ...any) {
	fmt.Fprintf(s.stdout, "ok    "+format+"\n", args...)
}

// checkRelease confirms the release exists (SPEC-0019-R02).
func (s *session) checkRelease(ctx context.Context) error {
	if _, err := s.helm.Run(ctx, s.opts.helmArgs("status", s.opts.release, "--output", "json")...); err != nil {
		return fmt.Errorf("release %s not found in namespace %s: %v", s.opts.release, s.opts.kube.namespace, err)
	}
	s.check("release %s exists in namespace %s", s.opts.release, s.opts.kube.namespace)
	return nil
}

// checkHelmVersion requires Helm 3.14 or newer, the first with
// --reset-then-reuse-values.
func (s *session) checkHelmVersion(ctx context.Context) error {
	out, err := s.helm.Run(ctx, "version", "--template", "{{.Version}}")
	if err != nil {
		return fmt.Errorf("helm version: %v", err)
	}
	version := strings.TrimSpace(string(out))
	var major, minor int
	if _, err := fmt.Sscanf(version, "v%d.%d", &major, &minor); err != nil {
		return fmt.Errorf("cannot read the helm version %q", version)
	}
	if major < 3 || (major == 3 && minor < 14) {
		return fmt.Errorf("helm %s is too old: kavryctl upgrade needs Helm 3.14 or newer (--reset-then-reuse-values)", version)
	}
	s.check("helm %s", version)
	return nil
}

// routedServers returns the Ready MCPServers, the ones the Gateway routes.
func (s *session) routedServers(ctx context.Context) (map[string]string, error) {
	var servers kavryntv1alpha1.MCPServerList
	if err := s.kube.List(ctx, &servers); err != nil {
		return nil, fmt.Errorf("list MCPServers: %w", err)
	}
	routed := map[string]string{}
	for _, server := range servers.Items {
		if apimeta.IsStatusConditionTrue(server.Status.Conditions, kavryntv1alpha1.ConditionReady) {
			routed[server.Namespace+"/"+server.Name] = server.RouteName()
		}
	}
	return routed, nil
}

// confirmRouted waits until every server routed before is routed again and
// reports each; it fails naming those that are not (SPEC-0019-R03).
func (s *session) confirmRouted(ctx context.Context, before map[string]string) error {
	names := make([]string, 0, len(before))
	for name := range before {
		names = append(names, name)
	}
	sort.Strings(names)
	deadline := time.Now().Add(s.opts.timeout)
	var missing []string
	for {
		now, err := s.routedServers(ctx)
		if err != nil {
			return err
		}
		missing = missing[:0]
		for _, name := range names {
			if _, ok := now[name]; !ok {
				missing = append(missing, name)
			}
		}
		if len(missing) == 0 || !time.Now().Before(deadline) {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(routedPollInterval):
		}
	}
	for _, name := range names {
		if !contains(missing, name) {
			fmt.Fprintf(s.stdout, "ok    %s is routed at /mcp/%s\n", name, before[name])
		}
	}
	for _, name := range missing {
		fmt.Fprintf(s.stdout, "FAIL  %s was routed before and is not Ready now\n", name)
	}
	if len(missing) > 0 {
		return fmt.Errorf("%d of %d servers routed before are not routed now", len(missing), len(names))
	}
	fmt.Fprintf(s.stdout, "%d servers routed before are routed now\n", len(names))
	return nil
}

func contains(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}

// crdOwned reports whether the release owns the MCPServer CRD; a missing CRD
// counts as owned, since the chart installs it.
func (s *session) crdOwned(ctx context.Context) (*apiextensionsv1.CustomResourceDefinition, bool, error) {
	var crd apiextensionsv1.CustomResourceDefinition
	if err := s.kube.Get(ctx, types.NamespacedName{Name: crdName}, &crd); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, true, nil
		}
		return nil, false, fmt.Errorf("read the %s CRD: %w", crdName, err)
	}
	owned := crd.Labels["app.kubernetes.io/managed-by"] == "Helm" &&
		crd.Annotations["meta.helm.sh/release-name"] == s.opts.release &&
		crd.Annotations["meta.helm.sh/release-namespace"] == s.opts.kube.namespace
	return &crd, owned, nil
}

// adoptCRD gives the release the CRD, as the chart README's kubectl
// commands do.
func (s *session) adoptCRD(ctx context.Context, crd *apiextensionsv1.CustomResourceDefinition) error {
	patch := client.MergeFrom(crd.DeepCopy())
	if crd.Labels == nil {
		crd.Labels = map[string]string{}
	}
	if crd.Annotations == nil {
		crd.Annotations = map[string]string{}
	}
	crd.Labels["app.kubernetes.io/managed-by"] = "Helm"
	crd.Annotations["meta.helm.sh/release-name"] = s.opts.release
	crd.Annotations["meta.helm.sh/release-namespace"] = s.opts.kube.namespace
	if err := s.kube.Patch(ctx, crd, patch); err != nil {
		return fmt.Errorf("adopt the %s CRD: %w", crdName, err)
	}
	return nil
}

func runUpgrade(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("upgrade", stderr)
	var opts releaseOptions
	opts.bind(fs)
	chart := fs.String("chart", defaultChart, "Chart reference, or a chart directory")
	version := fs.String("version", "", "Chart version to upgrade to (required unless --chart is a directory)")
	values := fs.String("values", "", "Values file applied on top of the release's values")
	adopt := fs.Bool("adopt-crd", false, "Let the release take over an MCPServer CRD installed by 0.0.5-beta.1 or earlier")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: kavryctl upgrade --version <version> [--release kavrynt] [-n kavrynt-system] [--chart REF|DIR] [--values FILE] [--adopt-crd] [--dry-run]")
		return 2
	}
	info, statErr := os.Stat(*chart)
	localChart := statErr == nil && info.IsDir()
	if *version == "" && !localChart {
		fmt.Fprintln(stderr, "upgrade: --version is required for a chart from a repository")
		return 2
	}
	s, err := newSession(opts, stdout, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "upgrade: %v\n", err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), opts.timeout+2*time.Minute)
	defer cancel()

	// Preflight: nothing changes until every check passes (SPEC-0019-R02).
	if err := s.checkHelmVersion(ctx); err != nil {
		fmt.Fprintf(stderr, "upgrade: %v\n", err)
		return 1
	}
	if err := s.checkRelease(ctx); err != nil {
		fmt.Fprintf(stderr, "upgrade: %v\n", err)
		return 1
	}
	if *version != "" {
		if _, err := s.helm.Run(ctx, "show", "chart", *chart, "--version", *version); err != nil {
			fmt.Fprintf(stderr, "upgrade: chart %s version %s not found: %v\n", *chart, *version, err)
			return 1
		}
		s.check("chart %s version %s exists", *chart, *version)
	}
	crd, owned, err := s.crdOwned(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "upgrade: %v\n", err)
		return 1
	}
	switch {
	case owned:
		s.check("the %s CRD belongs to release %s", crdName, opts.release)
	case !*adopt:
		fmt.Fprintf(stderr, `upgrade: the %s CRD was installed outside release %s (0.0.5-beta.1 or earlier), so Helm would refuse to update it.
Nothing was changed. Run again with --adopt-crd, or adopt it yourself:
  kubectl label crd %s app.kubernetes.io/managed-by=Helm --overwrite
  kubectl annotate crd %s meta.helm.sh/release-name=%s meta.helm.sh/release-namespace=%s --overwrite
`, crdName, opts.release, crdName, crdName, opts.release, s.opts.kube.namespace)
		return 1
	}
	before, err := s.routedServers(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "upgrade: %v\n", err)
		return 1
	}
	s.check("%d servers are routed now", len(before))

	command := []string{"upgrade", opts.release, *chart}
	if *version != "" {
		command = append(command, "--version", *version)
	}
	// --reuse-values would keep the previous chart's defaults, including its
	// image tags, so the upgrade would not take effect (SPEC-0019-R03).
	command = append(command, "--reset-then-reuse-values")
	if *values != "" {
		command = append(command, "--values", *values)
	}
	command = s.opts.helmArgs(append(command, "--wait", "--timeout", opts.timeout.String())...)
	if opts.dryRun {
		if !owned {
			fmt.Fprintf(stdout, "would adopt the %s CRD for release %s\n", crdName, opts.release)
		}
		fmt.Fprintf(stdout, "would run: helm %s\n", strings.Join(command, " "))
		return 0
	}
	if !owned {
		if err := s.adoptCRD(ctx, crd); err != nil {
			fmt.Fprintf(stderr, "upgrade: %v\n", err)
			return 1
		}
		s.check("adopted the %s CRD for release %s", crdName, opts.release)
	}
	fmt.Fprintf(stdout, "running: helm %s\n", strings.Join(command, " "))
	if _, err := s.helm.Run(ctx, command...); err != nil {
		fmt.Fprintf(stderr, "upgrade: helm upgrade failed: %v\n", err)
		return 1
	}
	if err := s.confirmRouted(ctx, before); err != nil {
		fmt.Fprintf(stderr, "upgrade: %v\n", err)
		return 1
	}
	return 0
}

func runRollback(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("rollback", stderr)
	var opts releaseOptions
	opts.bind(fs)
	revision := fs.Int("revision", 0, "Release revision to roll back to (default: the previous one)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 || *revision < 0 {
		fmt.Fprintln(stderr, "usage: kavryctl rollback [--revision N] [--release kavrynt] [-n kavrynt-system] [--dry-run]")
		return 2
	}
	s, err := newSession(opts, stdout, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "rollback: %v\n", err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), opts.timeout+2*time.Minute)
	defer cancel()

	if err := s.checkRelease(ctx); err != nil {
		fmt.Fprintf(stderr, "rollback: %v\n", err)
		return 1
	}
	before, err := s.routedServers(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "rollback: %v\n", err)
		return 1
	}
	s.check("%d servers are routed now", len(before))

	command := []string{"rollback", opts.release}
	if *revision > 0 {
		command = append(command, strconv.Itoa(*revision))
	}
	command = s.opts.helmArgs(append(command, "--wait", "--timeout", opts.timeout.String())...)
	if opts.dryRun {
		fmt.Fprintf(stdout, "would run: helm %s\n", strings.Join(command, " "))
		return 0
	}
	fmt.Fprintf(stdout, "running: helm %s\n", strings.Join(command, " "))
	if _, err := s.helm.Run(ctx, command...); err != nil {
		fmt.Fprintf(stderr, "rollback: helm rollback failed: %v\n", err)
		return 1
	}
	if err := s.confirmRouted(ctx, before); err != nil {
		fmt.Fprintf(stderr, "rollback: %v\n", err)
		return 1
	}
	return 0
}
