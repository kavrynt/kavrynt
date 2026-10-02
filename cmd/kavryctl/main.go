package main

import (
	"os"

	"github.com/kavrynt/kavrynt/internal/kavryctl/cli"
)

func main() {
	os.Exit(cli.Execute(os.Args[1:], os.Stdout, os.Stderr))
}
