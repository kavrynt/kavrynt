package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/kavrynt/kavryctl/internal/manifest"
	"github.com/kavrynt/kavryctl/internal/platform"
	"github.com/kavrynt/kavryctl/internal/registry"
	"github.com/kavrynt/kavryctl/internal/remote"
)

var (
	Version            = "0.1.0-dev"
	Commit             = "unknown"
	BuildDate          = "unknown"
	newPlatformManager = func() *platform.Manager { return platform.NewManager(nil) }
)

func Execute(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printUsage(stderr)
		return 2
	}

	switch args[0] {
	case "version":
		fmt.Fprintf(stdout, "kavryctl %s\ncommit %s\nbuilt %s\n", Version, Commit, BuildDate)
		return 0
	case "help", "-h", "--help":
		printUsage(stdout)
		return 0
	case "init":
		return runInit(args[1:], stdout, stderr)
	case "validate":
		return runValidate(args[1:], stdout, stderr)
	case "register":
		return runRegister(args[1:], stdout, stderr)
	case "unregister":
		return runUnregister(args[1:], stdout, stderr)
	case "list":
		return runList(args[1:], stdout, stderr)
	case "inspect":
		return runInspect(args[1:], stdout, stderr)
	case "install":
		return runInstall(args[1:], stdout, stderr)
	case "status":
		return runStatus(args[1:], stdout, stderr)
	case "uninstall":
		return runUninstall(args[1:], stdout, stderr)
	case "manifest":
		return runManifest(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown command %q\n\n", args[0])
		printUsage(stderr)
		return 2
	}
}

type stringSliceFlag []string

func (values *stringSliceFlag) String() string { return strings.Join(*values, ",") }

func (values *stringSliceFlag) Set(value string) error {
	*values = append(*values, value)
	return nil
}

func runInstall(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("install", stderr)
	opts := platform.InstallOptions{Wait: true}
	addChartFlags(fs, &opts.ChartOptions)
	fs.BoolVar(&opts.Wait, "wait", true, "Wait until Kavrynt workloads are ready")
	fs.BoolVar(&opts.DryRun, "dry-run", false, "Render installation actions without applying them")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(stderr, "install does not accept positional arguments")
		return 2
	}

	if err := newPlatformManager().Install(context.Background(), stdout, stderr, opts); err != nil {
		fmt.Fprintf(stderr, "install failed: %v\n", err)
		return 1
	}
	if !opts.DryRun {
		fmt.Fprintf(stdout, "Kavrynt is installed in namespace %s.\nRun: kavryctl status --namespace %s\n", opts.Namespace, opts.Namespace)
	}
	return 0
}

func runStatus(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("status", stderr)
	opts := platform.StatusOptions{Namespace: platform.DefaultNamespace}
	fs.StringVar(&opts.Namespace, "namespace", opts.Namespace, "Kubernetes namespace")
	fs.StringVar(&opts.KubeContext, "context", "", "Kubernetes context")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(stderr, "status does not accept positional arguments")
		return 2
	}
	if err := newPlatformManager().Status(context.Background(), stdout, stderr, opts); err != nil {
		fmt.Fprintf(stderr, "status failed: %v\n", err)
		return 1
	}
	return 0
}

func runUninstall(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("uninstall", stderr)
	opts := platform.UninstallOptions{
		ReleaseName: platform.DefaultReleaseName,
		Namespace:   platform.DefaultNamespace,
		Timeout:     5 * time.Minute,
		Wait:        true,
	}
	fs.StringVar(&opts.ReleaseName, "release-name", opts.ReleaseName, "Helm release name")
	fs.StringVar(&opts.Namespace, "namespace", opts.Namespace, "Kubernetes namespace")
	fs.StringVar(&opts.KubeContext, "context", "", "Kubernetes context")
	fs.DurationVar(&opts.Timeout, "timeout", opts.Timeout, "Time to wait for uninstall")
	fs.BoolVar(&opts.Wait, "wait", opts.Wait, "Wait until resources are removed")
	fs.BoolVar(&opts.Purge, "purge", false, "Also remove Kavrynt CRDs")
	fs.BoolVar(&opts.DeleteNamespace, "delete-namespace", false, "Also remove the Kavrynt namespace")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(stderr, "uninstall does not accept positional arguments")
		return 2
	}
	if err := newPlatformManager().Uninstall(context.Background(), stdout, stderr, opts); err != nil {
		fmt.Fprintf(stderr, "uninstall failed: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "Kavrynt was uninstalled from namespace %s.\n", opts.Namespace)
	return 0
}

func runManifest(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "generate" {
		fmt.Fprintln(stderr, "usage: kavryctl manifest generate [flags]")
		return 2
	}
	fs := newFlagSet("manifest generate", stderr)
	opts := platform.ChartOptions{}
	addChartFlags(fs, &opts)
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(stderr, "manifest generate does not accept positional arguments")
		return 2
	}
	if err := newPlatformManager().Generate(context.Background(), stdout, stderr, opts); err != nil {
		fmt.Fprintf(stderr, "manifest generation failed: %v\n", err)
		return 1
	}
	return 0
}

func addChartFlags(fs *flag.FlagSet, opts *platform.ChartOptions) {
	opts.ReleaseName = platform.DefaultReleaseName
	opts.Namespace = platform.DefaultNamespace
	opts.Chart = platform.DefaultChart
	opts.Version = releaseVersion(Version)
	opts.Timeout = 5 * time.Minute
	fs.StringVar(&opts.ReleaseName, "release-name", opts.ReleaseName, "Helm release name")
	fs.StringVar(&opts.Namespace, "namespace", opts.Namespace, "Kubernetes namespace")
	fs.StringVar(&opts.Chart, "chart", opts.Chart, "Helm chart path or OCI reference")
	fs.StringVar(&opts.Version, "version", opts.Version, "Kavrynt chart and image version")
	fs.StringVar(&opts.KubeContext, "context", "", "Kubernetes context")
	fs.Var((*stringSliceFlag)(&opts.ValuesFiles), "values", "Values file (repeatable)")
	fs.Var((*stringSliceFlag)(&opts.ValuesFiles), "f", "Values file (repeatable shorthand)")
	fs.Var((*stringSliceFlag)(&opts.SetValues), "set", "Set a Helm value (repeatable)")
	fs.DurationVar(&opts.Timeout, "timeout", opts.Timeout, "Time to wait for Kubernetes operations")
}

func releaseVersion(version string) string {
	version = strings.TrimSpace(strings.TrimPrefix(version, "v"))
	if version == "" || strings.Contains(version, "dev") || version == "unknown" {
		return ""
	}
	return version
}

func runInit(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("init", stderr)
	homeFlag := fs.String("home", "", "Kavrynt home directory")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(stderr, "init does not accept positional arguments")
		return 2
	}

	home := registry.Home(*homeFlag)
	if err := registry.Init(home); err != nil {
		fmt.Fprintf(stderr, "init failed: %v\n", err)
		return 1
	}

	fmt.Fprintf(stdout, "initialized registry at %s\n", registry.Path(home))
	return 0
}

func runValidate(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("validate", stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: kavryctl validate <manifest.json>")
		return 2
	}

	path := fs.Arg(0)
	m, err := manifest.Load(path)
	if err != nil {
		fmt.Fprintf(stderr, "validation failed: %v\n", err)
		return 1
	}

	fmt.Fprintf(stdout, "valid MCP server manifest: %s\n", m.Metadata.Name)
	return 0
}

func runRegister(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("register", stderr)
	homeFlag := fs.String("home", "", "Kavrynt home directory")
	registryFlag := fs.String("registry", "", "Remote Kavrynt Registry URL")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: kavryctl register [--home DIR] [--registry URL] <manifest.json>")
		return 2
	}

	path := fs.Arg(0)
	m, err := manifest.Load(path)
	if err != nil {
		fmt.Fprintf(stderr, "registration failed: %v\n", err)
		return 1
	}

	registryURL, err := remoteRegistryURL(*registryFlag, *homeFlag)
	if err != nil {
		fmt.Fprintf(stderr, "registration failed: %v\n", err)
		return 2
	}
	if registryURL != "" {
		client, err := remote.NewClient(registryURL)
		if err != nil {
			fmt.Fprintf(stderr, "registration failed: %v\n", err)
			return 2
		}
		server, created, err := client.Register(m)
		if err != nil {
			fmt.Fprintf(stderr, "registration failed: %v\n", err)
			return 1
		}
		action := "updated"
		if created {
			action = "registered"
		}
		fmt.Fprintf(stdout, "%s MCP server %s@%s in Registry %s\n", action, server.ID, server.Manifest.Spec.Version, registryURL)
		return 0
	}

	home := registry.Home(*homeFlag)
	source, err := filepath.Abs(path)
	if err != nil {
		source = path
	}
	server, created, err := registry.Register(home, m, source, time.Now())
	if err != nil {
		fmt.Fprintf(stderr, "registration failed: %v\n", err)
		return 1
	}

	action := "updated"
	if created {
		action = "registered"
	}
	fmt.Fprintf(stdout, "%s MCP server %s@%s\n", action, server.ID, server.Manifest.Spec.Version)
	return 0
}

func runUnregister(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("unregister", stderr)
	homeFlag := fs.String("home", "", "Kavrynt home directory")
	registryFlag := fs.String("registry", "", "Remote Kavrynt Registry URL")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: kavryctl unregister [--home DIR] [--registry URL] <server-id>")
		return 2
	}

	name := fs.Arg(0)
	registryURL, err := remoteRegistryURL(*registryFlag, *homeFlag)
	if err != nil {
		fmt.Fprintf(stderr, "unregister failed: %v\n", err)
		return 2
	}
	if registryURL != "" {
		client, err := remote.NewClient(registryURL)
		if err != nil {
			fmt.Fprintf(stderr, "unregister failed: %v\n", err)
			return 2
		}
		if err := client.Unregister(name); err != nil {
			fmt.Fprintf(stderr, "unregister failed: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "unregistered MCP server %s from Registry %s\n", name, registryURL)
		return 0
	}

	if err := registry.Unregister(registry.Home(*homeFlag), name); err != nil {
		fmt.Fprintf(stderr, "unregister failed: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "unregistered MCP server %s\n", name)
	return 0
}

func runList(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("list", stderr)
	homeFlag := fs.String("home", "", "Kavrynt home directory")
	registryFlag := fs.String("registry", "", "Remote Kavrynt Registry URL")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(stderr, "list does not accept positional arguments")
		return 2
	}

	registryURL, err := remoteRegistryURL(*registryFlag, *homeFlag)
	if err != nil {
		fmt.Fprintf(stderr, "list failed: %v\n", err)
		return 2
	}
	if registryURL != "" {
		client, err := remote.NewClient(registryURL)
		if err != nil {
			fmt.Fprintf(stderr, "list failed: %v\n", err)
			return 2
		}
		servers, err := client.List()
		if err != nil {
			fmt.Fprintf(stderr, "list failed: %v\n", err)
			return 1
		}
		if len(servers) == 0 {
			fmt.Fprintln(stdout, "no MCP servers registered")
			return 0
		}
		tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "NAME\tVERSION\tTRANSPORT\tUPDATED")
		for _, server := range servers {
			fmt.Fprintf(
				tw,
				"%s\t%s\t%s\t%s\n",
				server.ID,
				server.Manifest.Spec.Version,
				server.Manifest.Spec.Transport,
				server.UpdatedAt.Format(time.RFC3339),
			)
		}
		_ = tw.Flush()
		return 0
	}

	servers, err := registry.List(registry.Home(*homeFlag))
	if err != nil {
		fmt.Fprintf(stderr, "list failed: %v\n", err)
		return 1
	}

	if len(servers) == 0 {
		fmt.Fprintln(stdout, "no MCP servers registered")
		return 0
	}

	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tVERSION\tTRANSPORT\tUPDATED")
	for _, server := range servers {
		fmt.Fprintf(
			tw,
			"%s\t%s\t%s\t%s\n",
			server.ID,
			server.Manifest.Spec.Version,
			server.Manifest.Spec.Transport,
			server.UpdatedAt.Format(time.RFC3339),
		)
	}
	_ = tw.Flush()
	return 0
}

func runInspect(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("inspect", stderr)
	homeFlag := fs.String("home", "", "Kavrynt home directory")
	registryFlag := fs.String("registry", "", "Remote Kavrynt Registry URL")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: kavryctl inspect [--home DIR] [--registry URL] <server-id>")
		return 2
	}

	registryURL, err := remoteRegistryURL(*registryFlag, *homeFlag)
	if err != nil {
		fmt.Fprintf(stderr, "inspect failed: %v\n", err)
		return 2
	}
	if registryURL != "" {
		client, err := remote.NewClient(registryURL)
		if err != nil {
			fmt.Fprintf(stderr, "inspect failed: %v\n", err)
			return 2
		}
		server, err := client.Inspect(fs.Arg(0))
		if err != nil {
			fmt.Fprintf(stderr, "inspect failed: %v\n", err)
			return 1
		}
		data, err := json.MarshalIndent(server, "", "  ")
		if err != nil {
			fmt.Fprintf(stderr, "inspect failed: %v\n", err)
			return 1
		}
		fmt.Fprintln(stdout, string(data))
		return 0
	}

	server, err := registry.Inspect(registry.Home(*homeFlag), fs.Arg(0))
	if err != nil {
		fmt.Fprintf(stderr, "inspect failed: %v\n", err)
		return 1
	}

	data, err := json.MarshalIndent(server, "", "  ")
	if err != nil {
		fmt.Fprintf(stderr, "inspect failed: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, string(data))
	return 0
}

func newFlagSet(name string, stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	return fs
}

func remoteRegistryURL(explicitURL, explicitHome string) (string, error) {
	if explicitURL != "" && explicitHome != "" {
		return "", fmt.Errorf("--registry and --home cannot be used together")
	}
	if explicitURL != "" {
		return explicitURL, nil
	}
	if explicitHome != "" {
		return "", nil
	}
	return os.Getenv("KAVRYNT_REGISTRY_URL"), nil
}

func printUsage(w io.Writer) {
	fmt.Fprint(w, strings.TrimSpace(`
kavryctl installs and manages the Kavrynt MCP control plane.

Usage:
  kavryctl version
  kavryctl install [--version VERSION] [--values FILE] [--set KEY=VALUE]
  kavryctl status [--namespace NAMESPACE]
  kavryctl manifest generate [--version VERSION] [--values FILE]
  kavryctl uninstall [--purge] [--delete-namespace]
  kavryctl init [--home DIR]
  kavryctl validate <manifest.json>
  kavryctl register [--home DIR] [--registry URL] <manifest.json>
  kavryctl unregister [--home DIR] [--registry URL] <server-id>
  kavryctl list [--home DIR] [--registry URL]
  kavryctl inspect [--home DIR] [--registry URL] <server-id>

Environment:
  KAVRYNT_HOME          Defaults to .kavrynt in the current directory.
  KAVRYNT_REGISTRY_URL  Remote Registry API URL for register/unregister/list/inspect.
`)+"\n")
}
