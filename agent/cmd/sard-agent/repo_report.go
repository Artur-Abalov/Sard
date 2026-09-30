// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/Artur-Abalov/sard/agent/internal/repoinit"
)

// repoClassCodes is the one place a failure class becomes an exit code;
// the numbers are A2b's (docs/adr/0025-grpc-error-model.md).
var repoClassCodes = map[repoinit.Class]int{
	repoinit.ClassAgentError: exitAgentError,
	repoinit.ClassUsage:      exitUsage,
	repoinit.ClassExists:     exitIdentityExists,
	repoinit.ClassTemporary:  exitTemporary,
	repoinit.ClassWrite:      exitWrite,
}

// reportRepoFailure prints the refusal of "sard-agent repo <sub>" and
// returns its exit code.
func reportRepoFailure(stderr io.Writer, sub string, f *repoinit.Failure) int {
	_, _ = fmt.Fprintf(stderr, "sard-agent repo %s: %s\n", sub, f)
	return repoClassCodes[f.Class]
}

// reportRepoError prints an error that is not a Failure yet: an agent error.
func reportRepoError(ctx context.Context, stderr io.Writer, sub string, err error) int {
	var f *repoinit.Failure
	if f = repoinit.Interruption(ctx); f != nil || errors.As(err, &f) {
		return reportRepoFailure(stderr, sub, f)
	}
	_, _ = fmt.Fprintf(stderr, "sard-agent repo %s: %v\n", sub, err)
	return exitAgentError
}

// repoHelpCode is one exit code of a command's --help.
type repoHelpCode struct {
	code int
	word string
	help string
}

func printRepoCodes(stdout io.Writer, codes []repoHelpCode) {
	for _, c := range codes {
		_, _ = fmt.Fprintf(stdout, "  %d  %s: %s\n", c.code, c.word, c.help)
	}
}

var repoInitHelpCodes = []repoHelpCode{
	{exitOK, "success", "the repository was created, or --help"},
	{exitAgentError, "agent error", "restic is missing, too old or unusable; the backend refused (credentials, permissions, TLS); unexpected restic output"},
	{exitUsage, "usage", "flags, config, unknown repository name, crypto_provider, password_file or env_file, or a password that does not open the existing repository"},
	{exitIdentityExists, "identity exists", "the repository is already initialised; nothing was changed and its repository_id is printed"},
	{exitTemporary, "temporary", "the backend is unreachable, --timeout ran out, the command was interrupted, or another init of this repository is running; it can be repeated"},
	{exitWrite, "write", "the password file could not be created"},
}

func printRepoHelp(stdout io.Writer, sub string) {
	if sub == "list" {
		printRepoListHelp(stdout)
		return
	}
	printRepoInitHelp(stdout)
}

func printRepoInitHelp(stdout io.Writer) {
	_, _ = fmt.Fprintf(stdout, `Usage: sard-agent repo init [flags] <name>

Creates the repository named <name> in the agent config (a restic
repository): its url, password_file and env_file come from the config,
never from flags. Never
reads stdin and never changes the config file. Run it as the user of the
sard-agent service.

Flags:
  --config string       path to the agent config (default /etc/sard/agent.yaml)
  --generate-password   create password_file with a random password if it does not exist
                        (an existing file is used as it is and never overwritten)
  --timeout duration    how long the whole command may take (default 2m)

The key of the repository exists only on this host: save a copy of the
password file outside it. Afterwards restart the sard-agent service so that
the server receives the repository_id.

Exit codes:
`)
	printRepoCodes(stdout, repoInitHelpCodes)
}

var repoListHelpCodes = []repoHelpCode{
	{exitOK, "success", "the state of every repository is known (initialised or not), or --help"},
	{exitAgentError, "agent error", "restic is missing, too old or unusable; or the first permanent problem row is an agent error (the backend refused, unexpected restic output)"},
	{exitUsage, "usage", "flags or config; or the first permanent problem row is a usage problem (crypto_provider, password_file, env_file, wrong password)"},
	{exitTemporary, "temporary", "every problem row is temporary (backend unreachable, --timeout ran out), or the command was interrupted"},
}

func printRepoListHelp(stdout io.Writer) {
	_, _ = fmt.Fprint(stdout, `Usage: sard-agent repo list [flags]

Lists the repositories of the agent config and whether each one is
initialised. Read-only: never creates a repository or a file, never reads
stdin, never contacts the Sard server. A url is never printed.

Flags:
  --config string      path to the agent config (default /etc/sard/agent.yaml)
  --timeout duration   how long the whole command may take (default 2m)

Columns, one row per repository in config order:
  NAME           the repository name in the config
  BACKEND        local, s3, sftp, rest, ...
  STATUS         initialized, not-initialized, or the reason a problem
                 prevents the check (for example WRONG_PASSWORD,
                 SECRET_FILE_REJECTED, BACKEND_UNAVAILABLE, TIMEOUT)
  REPOSITORY_ID  the repository id if STATUS is initialized, otherwise -

A problem with one repository does not stop the list: its row shows the
reason and the message goes to stderr. Not initialised is a known state,
not a problem.

Exit codes: one per run. With problem rows the code is the class of the
first problem row in config order that is not temporary; if all problems
are temporary, 6.
`)
	printRepoCodes(stdout, repoListHelpCodes)
}
