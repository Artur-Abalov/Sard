// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/Artur-Abalov/sard/agent/internal/refusal"
	"github.com/Artur-Abalov/sard/agent/internal/repoinit"
)

// repoClassCodes is the one place a failure class becomes an exit code;
// the numbers are A2b's (docs/adr/0025-grpc-error-model.md).
var repoClassCodes = map[refusal.Class]int{
	refusal.ClassAgentError: exitAgentError,
	refusal.ClassUsage:      exitUsage,
	refusal.ClassExists:     exitIdentityExists,
	refusal.ClassTemporary:  exitTemporary,
	refusal.ClassWrite:      exitWrite,
	refusal.ClassTrust:      exitTrust,
}

// reportRepoError prints an error that is not a Failure yet: an agent error.
func reportRepoError(ctx context.Context, stderr io.Writer, sub string, err error) int {
	var f *refusal.Failure
	if f = repoinit.Interruption(ctx); f != nil || errors.As(err, &f) {
		return report(stderr, "repo "+sub, f)
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
	{exitUsage, "usage", "flags, rights (PRIVILEGES_REQUIRED, SERVICE_USER_UNKNOWN), config, unknown repository name, crypto_provider, password_file or env_file, or a password that does not open the existing repository"},
	{exitIdentityExists, "identity exists", "the repository is already initialised; nothing was changed and its repository_id is printed"},
	{exitTemporary, "temporary", "the backend is unreachable, --timeout ran out, the command was interrupted, or another init of this repository is running; it can be repeated"},
	{exitWrite, "write", "the password file could not be created, or the lock file in restic.cache_dir (LOCK_WRITE: the directory must exist and be writable)"},
}

func printRepoInitHelp(stdout io.Writer) {
	_, _ = fmt.Fprintf(stdout, `Usage: sard-agent repo init [flags] <name>

Creates the repository named <name> in the agent config (a restic
repository): its url, password_file and env_file come from the config,
never from flags. Never
reads stdin and never changes the config file. Run it through sudo (the
password file it creates then belongs to the service user, and restic runs as
that user) or as the service user itself; anyone else is refused
(PRIVILEGES_REQUIRED).

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
	{exitAgentError, "agent error", "restic is missing, too old or unusable; or the first permanent problem row is an agent error (BACKEND_REFUSED: the backend refused; RESTIC_OUTPUT_UNEXPECTED: unexpected restic output)"},
	{exitUsage, "usage", "flags, rights (PRIVILEGES_REQUIRED) or config; or the first permanent problem row is a usage problem (crypto_provider, password_file, env_file, wrong password)"},
	{exitTemporary, "temporary", "every problem row is temporary (backend unreachable, --timeout ran out), or the command was interrupted"},
}

func printRepoListHelp(stdout io.Writer) {
	_, _ = fmt.Fprint(stdout, `Usage: sard-agent repo list [flags]

Lists the repositories of the agent config and whether each one is
initialised, with the fragments of agent.d after the main config. Read-only:
never creates a repository or a file, never reads stdin, never contacts the
Sard server. A url is never printed. Run it through sudo or as the
service user (sard-agent, or service.user of the config); anyone else is refused.

Flags:
  --config string      path to the agent config (default /etc/sard/agent.yaml)
  --timeout duration   how long the whole command may take (default 2m)
  --json               print one JSON object {"repositories":[...]} with name,
                       backend, status, repository_id (null if unknown) and
                       defined_in; messages and exit codes are the same

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
