package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/kavrynt/kavrynt/internal/kavryctl/manifest"
	"github.com/kavrynt/kavrynt/internal/kavryctl/registry"
	"github.com/kavrynt/kavrynt/internal/kavryctl/remote"
)

var (
	Version   = "0.1.0-dev"
	Commit    = "unknown"
	BuildDate = "unknown"
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
	default:
		fmt.Fprintf(stderr, "unknown command %q\n\n", args[0])
		printUsage(stderr)
		return 2
	}
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
		fmt.Fprintf(stdout, "%s MCP server %s@%s in Registry %s\n", action, server.Manifest.Metadata.Name, server.Manifest.Spec.Version, registryURL)
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
	fmt.Fprintf(stdout, "%s MCP server %s@%s\n", action, server.Manifest.Metadata.Name, server.Manifest.Spec.Version)
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
		fmt.Fprintln(stderr, "usage: kavryctl unregister [--home DIR] [--registry URL] <name>")
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
				server.Manifest.Metadata.Name,
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
			server.Manifest.Metadata.Name,
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
		fmt.Fprintln(stderr, "usage: kavryctl inspect [--home DIR] [--registry URL] <name>")
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
kavryctl manages Kavrynt MCP server registration.

Usage:
  kavryctl version
  kavryctl init [--home DIR]
  kavryctl validate <manifest.json>
  kavryctl register [--home DIR] [--registry URL] <manifest.json>
  kavryctl unregister [--home DIR] [--registry URL] <name>
  kavryctl list [--home DIR] [--registry URL]
  kavryctl inspect [--home DIR] [--registry URL] <name>

Environment:
  KAVRYNT_HOME          Defaults to .kavrynt in the current directory.
  KAVRYNT_REGISTRY_URL  Remote Registry API URL for register/unregister/list/inspect.
`)+"\n")
}
