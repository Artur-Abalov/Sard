// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"fmt"
	"io"
)

// keyHelpPrinters are the helps of the commands of "sard-agent repo key".
var keyHelpPrinters = map[string]func(io.Writer){
	"key add":    printKeyAddHelp,
	"key list":   printKeyListHelp,
	"key remove": printKeyRemoveHelp,
}

// printKeyHelp is the help of "repo key", or of the command named.
func printKeyHelp(stdout io.Writer, sub string) {
	if printer, ok := keyHelpPrinters[sub]; ok {
		printer(stdout)
		return
	}
	_, _ = fmt.Fprint(stdout, `Usage: sard-agent repo key <command>

Commands:
  repo key add <name> --for verifier|recovery|other   issue a new key of the repository
  repo key list <name>                                list the keys of the repository
  repo key remove <name> <id>                         remove a key of the repository

Each command has its own --help.
`)
}

var (
	codeKeyUsage = usageCode("flags, rights (PRIVILEGES_REQUIRED, SERVICE_USER_UNKNOWN), REPOSITORY_UNKNOWN, config, VERIFIER_READ_ONLY (a verifier only reads), WRONG_PASSWORD, STORAGE_ACCESS_DENIED, S3_KEY_REJECTED, BUCKET_NOT_FOUND and the reasons named above")
	codeKeyAgent = repoHelpCode{exitAgentError, "agent error", "restic is missing, too old or unusable; the backend refused; SERVICE_RESTART_FAILED: the change is saved, see `journalctl -u sard-agent`"}
)

func printKeyAddHelp(stdout io.Writer) {
	printHelp(stdout, "repo key add [flags] <name> --for verifier|recovery|other", `Issues a new key of restic for the repository <name> with a random password
(32 bytes of the system generator, 43 characters of base64url). The purpose is
required: --for verifier (the key of a verification host), --for recovery
(the key the owner keeps outside this infrastructure) or --for other with a
--label. The purpose and the label are kept in the user field of the key itself
("sard:<purpose>" or "sard:<purpose>:<label>"), so repo key list shows on any
host whose key it is.

The password is shown once and stored nowhere else: on the terminal, with a
warning, or in a new file given with --to-file (an absolute path, walked
without following links, in a directory that exists; owner root, mode 0600
whatever the umask; an existing file is never overwritten). Without a terminal
and without --to-file the command issues nothing (KEY_OUTPUT_MISSING). The
password is never written to the log, the config or any other file.

Afterwards the command says what to do next: for a verifier key the command
repo add for the verifier host (the address without credentials; give the
verifier storage credentials that only read), for a recovery key where to keep
it: without a recovery key and without the password of this host the backups
are lost together with the host.

A key with the same purpose and label exists already, whoever issued it:
KEY_EXISTS; give another --label, or --if-missing to leave it as it is (the
command then says unchanged, issues nothing and writes nothing). Two hosts that
issue the same label at the same moment may both succeed.

Flags:
  --config string      path to the agent config (default /etc/sard/agent.yaml)
  --for string         verifier, recovery or other (required)
  --label string       1-64 characters of A-Z a-z 0-9 - _, the first a letter or digit
                       (required for other)
  --to-file string     write the password to this new file instead of the terminal
  --if-missing         a key with the same purpose and label is not an error
  --no-restart         do not restart the service, say how to (recovery keys only)
  --connect-timeout duration   how long the first access to the storage may take (default 30s)
  --timeout duration   how long the whole command may take (default 2m); on a timeout or an
                       interrupt the key may have been created: check with repo key list

A recovery key changes what the agent tells the server at Register, so the
command applies the change to the service as the commands that change the
config do; the other purposes do not touch the service.

`+sudoHelp, []repoHelpCode{
		codeOK, codeKeyAgent, codeKeyUsage,
		{exitTemporary, "temporary", "CONFIG_LOCKED, BACKEND_UNAVAILABLE, TIMEOUT, INTERRUPTED: it can be repeated (after a timeout check the keys first)"},
		{exitWrite, "write", "KEY_FILE_WRITE or CONFIG_WRITE: the password file or the lock file could not be written"},
	})
}

func printKeyListHelp(stdout io.Writer) {
	printHelp(stdout, "repo key list [flags] <name>", `Lists the keys of the repository <name>, in the order they were issued:
ID (8 characters, as restic prints it), PURPOSE and LABEL (- for a key that
was not issued by Sard), HOST, CREATED (RFC 3339, UTC) and CURRENT (* for the
key this host opens the repository with). No passwords. The keys are read
without the lock of the repository, so credentials that only read are enough.

Warnings go to stderr after the table and do not change the exit code:
  SINGLE_KEY       only one key: losing this host loses the backups
  NO_RECOVERY_KEY  no recovery key, with the command that issues one

Flags:
  --config string      path to the agent config (default /etc/sard/agent.yaml)
  --json               print one object {"repository","keys":[{"id","purpose","label",
                       "host","created","current"}],"warnings":["single_key",
                       "no_recovery_key"]}; id is the full id, purpose and label are
                       null for a key that was not issued by Sard
  --connect-timeout duration   how long the first access to the storage may take (default 30s)
  --timeout duration   how long the whole command may take (default 2m)

`+readOnlyHelp, []repoHelpCode{codeOK, codeKeyAgent, codeKeyUsage,
		{exitTemporary, "temporary", "BACKEND_UNAVAILABLE, TIMEOUT, INTERRUPTED"}})
}

func printKeyRemoveHelp(stdout io.Writer) {
	printHelp(stdout, "repo key remove [flags] <name> <id>", `Removes a key of the repository <name>. <id> is 8 to 64 hexadecimal characters
of the id repo key list prints; a prefix that names two keys is refused
(KEY_ID_AMBIGUOUS), one that names none removes nothing (nothing to remove).
The key of this host (KEY_IN_USE) and the last key of the repository (LAST_KEY)
are never removed. The command shows the key and asks for the answer yes;
anything else is CONFIRMATION_REJECTED. Without a terminal it needs --yes
(CONFIRMATION_REQUIRED), and it reads no input.

Removing a key does not change the repository master key: anyone who had the
key could have extracted the master key and can still decrypt existing backups.
To fully revoke access create a new repository. The command says so every time.

Flags:
  --config string      path to the agent config (default /etc/sard/agent.yaml)
  --yes                remove without asking
  --no-restart         do not restart the service, say how to (recovery keys only)
  --connect-timeout duration   how long the first access to the storage may take (default 30s)
  --timeout duration   how long the whole command may take (default 2m)

`+sudoHelp, []repoHelpCode{
		codeOK, codeKeyAgent, codeKeyUsage,
		{exitTemporary, "temporary", "CONFIG_LOCKED, BACKEND_UNAVAILABLE, TIMEOUT, INTERRUPTED"},
		{exitWrite, "write", "CONFIG_WRITE: the lock file could not be written"},
	})
}
