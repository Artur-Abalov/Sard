// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import "io"

const checkHelpText = `Checks whether this host is ready to back up and restore: one line per check,
with a status (OK, WARN, ERROR or SKIP), the identifier of the check, its
subject (a repository, a secret or a plugin; "-" for the host) and what was
found. Every line that is not OK is followed by a "hint:" line with the command
to run or the section of
https://github.com/Artur-Abalov/sard/blob/main/docs/operator/09-troubleshooting.md
(the identifiers of the checks are the anchors of its sections). The last line
is a summary.

It only reads: nothing on the host or in the storage is created, changed or
removed (restic runs with --no-lock and --no-cache; systemctl is asked to show,
never to start), nothing is written to the system log, and no secret is
printed. Run it through sudo to see everything. As the service user (sard-agent,
or service.user of the config) it checks what that user can see and marks the
rest "needs root". Anyone else is refused.

In this order: service.user, config.load, config.names, config.references,
files.permissions, restic.binary, tls.identity, tls.chain, tls.expiry, tls.role,
server.reachable, server.tls, server.accepted, clock.skew, then for every
repository repository.storage, repository.credentials, repository.initialized,
repository.password, repository.id, repository.locks (see "sard-agent repo
check --help"; restic check with --read-data is only run there), then
secret.readable of every secret, plugin.declared, plugin.programs of every
plugin, restore.locations, selinux.labels, selinux.denials, service.running and
service.steps. The check of the server (server.*, clock.skew) is left out by this
version of the agent: those lines are SKIP.

Flags:
  --config string            path to the agent config (default /etc/sard/agent.yaml)
  --json                     print one JSON object per line, in the order of the
                             text lines: check, subject, status (ok, warning, error,
                             skipped), message and hint (null for ok); no summary
  --offline                  make no network connection and run restic on no
                             repository: server.*, clock.skew and every repository
                             check are SKIP "offline" (restic.binary still runs
                             restic version)
  --connect-timeout duration how long every access to a storage may take (default 10s);
                             the check is an ERROR with BACKEND_UNAVAILABLE

A WARN gives exit code 8, an ERROR gives 1; SKIP counts for neither (a host without
the programs of PostgreSQL gets 0). The data of the repositories is read only by
sard-agent repo check --read-data.
`

var checkHelpCodes = []repoHelpCode{
	{exitOK, "ready", "no WARN and no ERROR (SKIP lines do not count), or --help"},
	{exitAgentError, "errors", "at least one ERROR: the host is not ready"},
	{exitUsage, "usage", "flags, rights (PRIVILEGES_REQUIRED); nothing was checked"},
	{exitTemporary, "interrupted", "the command was stopped by SIGINT or SIGTERM; a failed access to a storage is an ERROR line, not this code"},
	{exitWarnings, "warnings", "at least one WARN and no ERROR"},
}

func printCheckHelp(stdout io.Writer) {
	printHelp(stdout, "check [flags]", checkHelpText, checkHelpCodes)
}
