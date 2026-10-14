// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import "io"

const repoCheckHelpText = `Checks whether the repositories of this host are ready to back up to and restore
from: one line per check, with a status (OK, WARN, ERROR or SKIP), the
identifier of the check, the repository and what was found; every line that is
not OK is followed by a "hint:" line with the command to run or the section of
https://github.com/Artur-Abalov/sard/blob/main/docs/operator/09-troubleshooting.md
(the identifiers of the checks are the anchors of its sections). The last line
is a summary. Without <name> every repository is checked, in the order of the
config, one after the other.

For each repository, in this order; the first ERROR stops that repository and
the rest of its checks are SKIP:
  repository.storage      the storage answers (restic cat config)
  repository.credentials  the env_file is readable and the storage accepts the keys
  repository.initialized  a repository is stored at the address
  repository.password     the password file is there, passes A1 and opens it
  repository.id           the id is the one the service announced to the server
                          (WARN if it did not, SKIP if it never registered)
  repository.locks        the locks of restic: how many, which host, how old;
                          WARN for one older than 24 hours
  repository.integrity    only with --read-data

It only reads: nothing on the host or in the storage is created, changed or
removed (restic runs with --no-lock and --no-cache), nothing is written to the system
log, and no secret is printed. restic check, which takes a lock in the storage,
runs only with --read-data. Run it through sudo, or as the service user
(sard-agent, or service.user of the config); restic runs as the service user.

Flags:
  --config string            path to the agent config (default /etc/sard/agent.yaml)
  --read-data int            also run restic check --read-data-subset=<int>%, a whole
                             number from 1 to 100 (the check then has no time limit
                             unless --timeout is given)
  --json                     print one JSON object per line, in the order of the
                             text lines: check, subject, status (ok, warning, error,
                             skipped), message and hint (null for ok); no summary
  --connect-timeout duration how long every access to the storage may take (default 10s);
                             restic is stopped and the check is an ERROR with BACKEND_UNAVAILABLE
  --timeout duration         how long the checks of one repository may take (default 2m,
                             none with --read-data); the check running is an ERROR with TIMEOUT

A WARN gives exit code 8, an ERROR gives 1; SKIP counts for neither.
`

var repoCheckHelpCodes = []repoHelpCode{
	{exitOK, "ready", "no WARN and no ERROR (SKIP lines do not count), or --help"},
	{exitAgentError, "errors", "at least one ERROR: the host is not ready"},
	{exitUsage, "usage", "flags (--read-data), rights (PRIVILEGES_REQUIRED, SERVICE_USER_UNKNOWN), config, REPOSITORY_UNKNOWN; nothing was checked"},
	{exitTemporary, "interrupted", "the command was stopped by SIGINT or SIGTERM; a failed access to a storage is an ERROR line, not this code"},
	{exitWarnings, "warnings", "at least one WARN and no ERROR"},
}

func printRepoCheckHelp(stdout io.Writer) {
	printHelp(stdout, "repo check [flags] [<name>]", repoCheckHelpText, repoCheckHelpCodes)
}

const repoUnlockHelpText = `Removes the stale locks of restic that this host left in the repository <name>:
those of this host that are older than 24 hours and whose process is gone (a
backup or a check that was killed). It never removes the lock of another host
and never a lock of a process that is running here; sard-agent repo check shows
which locks there are and tells whether this command can remove them.

Without --remove-stale the command does nothing and says so (usage error). It
changes the storage, so only root may run it (through sudo); restic runs as the
service user. A warning on stderr says that removing the lock of a running
process damages its work.

The mechanism is restic unlock (never --remove-all), which removes every lock
restic itself finds stale: older than 30 minutes on any host, or of a process of
this host that is gone. So right before it the locks are read again, and if
restic would remove a lock this command may not (one of another host, or one of
this host younger than 24 hours), nothing is removed and the command ends with
STALE_LOCKS_FOREIGN naming each such lock. A lock that becomes stale between that
reading and restic unlock is removed all the same: the race is not closed.

Nothing to remove is a success (no stale locks of this host in repository <name>),
and restic unlock is not run. Every removal is written to the system log (tag
sard-agent): repository <name> stale locks removed: <n>.

Flags:
  --config string            path to the agent config (default /etc/sard/agent.yaml)
  --remove-stale             required: remove the stale locks of this host
  --connect-timeout duration how long every access to the storage may take (default 30s)
  --timeout duration         how long the whole command may take (default 2m)
`

var repoUnlockHelpCodes = []repoHelpCode{
	{exitOK, "success", "the locks were removed, or there was nothing to remove, or --help"},
	{exitAgentError, "agent error", "restic is missing, too old or unusable; restic unlock refused"},
	{exitUsage, "usage", "flags (--remove-stale is required), rights (PRIVILEGES_REQUIRED, SERVICE_USER_UNKNOWN), config, REPOSITORY_UNKNOWN, password_file or env_file, STALE_LOCKS_FOREIGN"},
	{exitTemporary, "temporary", "the storage is unreachable (BACKEND_UNAVAILABLE), --timeout ran out (TIMEOUT) or the command was interrupted"},
}

func printRepoUnlockHelp(stdout io.Writer) {
	printHelp(stdout, "repo unlock <name> --remove-stale [flags]", repoUnlockHelpText, repoUnlockHelpCodes)
}
