// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

// Package commands defines the sardctl command tree. New subcommands are
// added in their own file and registered in NewRoot.
package commands

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"
)

// NewRoot returns the sardctl root command writing to out. Errors are
// returned from Execute, never printed by cobra.
func NewRoot(version string, out io.Writer) *cobra.Command {
	root := &cobra.Command{
		Use:           "sardctl",
		Short:         "sardctl manages a Sard backup orchestrator from the command line",
		SilenceUsage:  true,
		SilenceErrors: true,
		// Without a subcommand, print help.
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	root.SetOut(out)
	root.AddCommand(newVersion(version))
	return root
}

func newVersion(version string) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the sardctl version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "sardctl %s\n", version)
			return err
		},
	}
}
