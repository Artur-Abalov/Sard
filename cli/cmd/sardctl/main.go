// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

// Command sardctl is the Sard command-line client.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/Artur-Abalov/sard/cli/internal/commands"
)

// version is set at build time: -ldflags "-X main.version=...".
var version = "dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	root := commands.NewRoot(version, stdout)
	root.SetArgs(args)
	if err := root.Execute(); err != nil {
		_, _ = fmt.Fprintln(stderr, "sardctl:", err)
		return 1
	}
	return 0
}
