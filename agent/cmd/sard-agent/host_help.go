// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"fmt"
	"io"
)

// applyingHelp is what every command that changes the config says about
// making the change take effect (Р8).
const applyingHelp = `Applying the change: the command restarts sard-agent.service so that the agent
reads it, unless a step is running (accepted commands in the journal of
executor.state_dir), the service is not active, or there is no systemd: then
it says what to do instead. --no-restart never restarts. A step accepted in
the moment between the check of the journal and the restart fails (D13).
`

const sudoHelp = "Run it through sudo (as root); the files it creates belong to the service user\n(sard-agent, or service.user of the config). Written to the system log (tag\nsard-agent): every change.\n"

const readOnlyHelp = "Read-only: run it through sudo or as the service user (sard-agent, or\nservice.user of the config); anyone else is refused (PRIVILEGES_REQUIRED).\n"

var (
	codeOK       = repoHelpCode{exitOK, "success", "the command did what it was asked, or there was nothing to do (unchanged, nothing to remove), or --help"}
	codeRestart  = repoHelpCode{exitAgentError, "agent error", "SERVICE_RESTART_FAILED: the change is saved, see `journalctl -u sard-agent`"}
	codeConflict = repoHelpCode{exitIdentityExists, "identity exists", "REPOSITORY_CONFLICT: the name is connected to another address; run repo remove first"}
	codeLocked   = repoHelpCode{exitTemporary, "temporary", "CONFIG_LOCKED: another command is changing the config; repeat it"}
	codeWrite    = repoHelpCode{exitWrite, "write", "CONFIG_WRITE: a file or directory could not be written"}
)

func usageCode(what string) repoHelpCode {
	return repoHelpCode{exitUsage, "usage", what}
}

// printHelp writes a command's help: usage, text, exit codes.
func printHelp(stdout io.Writer, usage, text string, codes []repoHelpCode) {
	_, _ = fmt.Fprintf(stdout, "Usage: sard-agent %s\n\n%s\nExit codes:\n", usage, text)
	printRepoCodes(stdout, codes)
}

const secretSetHelpText = `Stores a secret value in a file of the service user and defines the secret in
a fragment of agent.d next to the main config; the main config is never
changed. The value never comes from an argument: give it on the terminal
(asked twice, no echo), with --stdin or with --from-file.

Flags:
  --config string      path to the agent config (default /etc/sard/agent.yaml)
  --stdin              read the value from standard input to its end, byte for byte
  --from-file string   read the value from a file, byte for byte
  --no-restart         do not restart the service, say how to
  (without --stdin and --from-file, with no terminal: SECRET_SOURCE_MISSING)

The name is 1-64 characters of A-Z a-z 0-9 - _, the first a letter or digit.
(a name that starts with - is read as a flag: put it after --).
The value is not empty and up to 65536 bytes. With printf '%s' no line break
is stored. The same value again changes nothing (unchanged).

` + sudoHelp + "\n" + applyingHelp

func printSecretHelp(stdout io.Writer, sub string) {
	switch sub {
	case "set":
		printHelp(stdout, "secret set [flags] <name>", secretSetHelpText, []repoHelpCode{
			codeOK, codeRestart,
			usageCode("flags, rights (PRIVILEGES_REQUIRED, SERVICE_USER_UNKNOWN), NAME_INVALID, config, DEFINED_IN_CONFIG, PATH_IN_USE, SECRET_SOURCE_MISSING, SECRET_SOURCE_CONFLICT, SECRET_EMPTY, SECRET_TOO_LARGE, SECRET_MISMATCH"),
			codeLocked, codeWrite,
		})
	case "list":
		printHelp(stdout, "secret list [flags]", `Lists the names of the secrets and the file of the config that defines each
(NAME, DEFINED_IN, by name). No value file is opened or checked.

Flags:
  --config string   path to the agent config (default /etc/sard/agent.yaml)
  --json            print one JSON object {"secrets":[{"name","defined_in"}]}

`+readOnlyHelp, []repoHelpCode{codeOK, usageCode("flags, rights (PRIVILEGES_REQUIRED), config, DUPLICATE_NAME")})
	default:
		printHelp(stdout, "secret remove [flags] <name>", `Removes the fragment of a secret and its value file. A secret defined in the
main config is never removed (DEFINED_IN_CONFIG). Removing what is not there
is not an error (nothing to remove).

Flags:
  --config string   path to the agent config (default /etc/sard/agent.yaml)
  --no-restart      do not restart the service, say how to

`+sudoHelp+"\n"+applyingHelp, []repoHelpCode{
			codeOK, codeRestart, usageCode("flags, rights (PRIVILEGES_REQUIRED, SERVICE_USER_UNKNOWN), config, DEFINED_IN_CONFIG"), codeLocked, codeWrite,
		})
	}
}

const repoAddHelpText = `Connects a restic repository: creates it, or attaches one that exists, and
writes a fragment of agent.d (the main config is never changed). <address> is
a local path (an absolute path of a directory on this host), an s3: address:
s3:https://<host>[:<port>]/<bucket>[/<path>] or s3:<host>/<bucket>[/<path>]
(http works too, with a warning: no TLS), or an sftp: address:
sftp:[<user>@]<host>:<path> or sftp://[<user>@]<host>[:<port>]/<path>. Other
kinds (rest:, b2:, ...) are refused (BACKEND_NOT_SUPPORTED); an address holds
no credentials (ADDRESS_INVALID).

A local path: missing parent directories are created for root, the repository
directory for the service user (0700). On a systemd host a drop-in of
sard-agent.service gets ReadWritePaths for the directory: without it
ProtectSystem=strict would hang restic on its lock. A backup on this same host
is lost with the host: the command warns.

An s3: storage: the keys go into secrets/restic-<name>.env (owner: the service
user, mode 0600) as AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY and, with
--region, AWS_DEFAULT_REGION; restic gets them only as environment. The key id
is not a secret, the secret key is never the value of a flag: it comes from
--secret-key-stdin, --secret-key-from-file or the terminal (asked twice, no
echo); one line break at its end is dropped, any other control character is
SECRET_INVALID. The command asks the storage with restic cat config as the
service user; a storage that refuses is told apart by its cause
(STORAGE_ACCESS_DENIED: a wrong secret may look like this too, and the key
needs read, write and delete in the bucket; S3_KEY_REJECTED; BUCKET_NOT_FOUND:
the bucket is not created by the command unless the key may create buckets;
BACKEND_UNAVAILABLE). New keys for a name that is connected are checked the
same way and replace the env file ("credentials updated", no restart).

With --provider aws or b2 the address is s3:<bucket>[/<path>], without a scheme
or a host, and the command builds the rest from --region, which --provider needs:
aws gives s3:https://s3.<region>.amazonaws.com/<bucket>[/<path>], b2 gives
s3:https://s3.<region>.backblazeb2.com/<bucket>[/<path>]. The region is not
checked against a list: a region that does not exist fails at the storage
(BACKEND_UNAVAILABLE). The env file gets AWS_DEFAULT_REGION=<region>.

An sftp: storage: the connection is made with the ssh key of the service user,
in the ssh directory of the home directory that passwd gives it (known_hosts,
config, id_ed25519 and id_ed25519.pub in ~/.ssh), which the command sets up for
you: nothing is run by hand on this host. The OpenSSH client
(ssh, sftp, ssh-keygen, ssh-keyscan) must be in the PATH of restic
(SSH_CLIENT_MISSING). The host key of the server is never accepted silently:
give its fingerprint with --host-key-fingerprint SHA256:<43 characters>, or
answer exactly yes at the terminal when the command shows it; without either
the command ends with HOST_KEY_UNCONFIRMED and reads no input. A fingerprint
that matches no key of the server is HOST_KEY_MISMATCH, an answer that is not
yes is HOST_KEY_REJECTED, and a key that differs from the one known_hosts holds
is HOST_KEY_CHANGED (exit code 5): if the server was reinstalled, repeat the
command with --replace-host-key, which still needs the confirmation. The key
of the service user (ed25519, no passphrase) is made by ssh-keygen as the
service user, only after the host key is trusted, and its public part is
printed: add it to authorized_keys of the user on the server. A key the server
does not accept is SSH_KEY_NOT_AUTHORIZED; the files written so far stay and a
repeat of the same command goes on. A file of ~/.ssh that is a link, has more
than one name, is not a regular file of the service user or root, or is larger
than 1 MiB is SSH_FILE_REJECTED and is not touched. repo remove leaves ~/.ssh
alone.

An empty storage gets a repository with a generated password in
secrets/restic-<name>.pass. A repository that exists needs its password:
--password-stdin, --password-from-file, or the terminal (asked twice, no echo).
The password is never an argument. The key of the repository exists only on
this host: the command asks for a copy of the password file. When creating the
repository fails, the env file and the password file are kept and used when
the command is repeated.

Flags:
  --config string              path to the agent config (default /etc/sard/agent.yaml)
  --access-key-id string       s3: the key id (required for an s3: address)
  --secret-key-stdin           s3: read the secret key from standard input
  --secret-key-from-file string  s3: read it from a file
  --provider string            s3: aws or b2, builds the address from --region (--provider needs --region)
  --region string              s3: AWS_DEFAULT_REGION (1-64 characters of a-z 0-9 -)
  --host-key-fingerprint string  sftp: the fingerprint (SHA256:...) that confirms the host key
  --replace-host-key           sftp: replace a host key that changed (still needs the confirmation)
  --password-stdin             read the password of an existing repository from standard input
  --password-from-file string  read it from a file
  --no-restart                 do not restart the service, say how to
  --connect-timeout duration   how long every network call may take (restic cat config,
                               ssh-keyscan, the ssh login check; default 30s); the program
                               is stopped, the command ends with BACKEND_UNAVAILABLE
  --timeout duration           how long the whole command may take (default 2m); the time
                               spent waiting for the operator at the terminal is not counted

The standard input gives one value: not --secret-key-stdin with --password-stdin.
A local path must not pass through a symbolic link, in any component, and must
be given in its resolved form (/run, not /var/run): the command refuses a link
(LOCAL_PATH_INVALID) so that nobody can redirect the creation of directories
and the change of owner, which it makes as root, to another place.

The same name and address again changes nothing (unchanged).

Run it through sudo (as root); the files it creates belong to the service user
(sard-agent, or service.user of the config). Every change is written to the
system log (tag sard-agent).

` + applyingHelp

var repoHelpPrinters = map[string]func(io.Writer){
	"list":     printRepoListHelp,
	"add":      printRepoAddHelp,
	"show":     printRepoShowHelp,
	"remove":   printRepoRemoveHelp,
	"password": printRepoPasswordHelp,
}

func printRepoHelp(stdout io.Writer, sub string) {
	if printer, ok := repoHelpPrinters[sub]; ok {
		printer(stdout)
		return
	}
	printRepoInitHelp(stdout)
}

func printRepoAddHelp(stdout io.Writer) {
	printHelp(stdout, "repo add [flags] <name> <address>", repoAddHelpText, []repoHelpCode{
		codeOK, codeRestart,
		usageCode("flags, rights (PRIVILEGES_REQUIRED, SERVICE_USER_UNKNOWN), NAME_INVALID, BACKEND_NOT_SUPPORTED, ADDRESS_INVALID, LOCAL_PATH_INVALID, config, DEFINED_IN_CONFIG, PATH_IN_USE, SECRET_SOURCE_MISSING, SECRET_SOURCE_CONFLICT, SECRET_INVALID, SECRET_FILE_REJECTED, WRONG_PASSWORD, S3_KEY_REJECTED, STORAGE_ACCESS_DENIED, BUCKET_NOT_FOUND, SSH_HOME_INVALID, SSH_FILE_REJECTED, SSH_KEY_NOT_AUTHORIZED, HOST_KEY_UNCONFIRMED"),
		codeConflict,
		{exitTrust, "trust", "HOST_KEY_MISMATCH, HOST_KEY_REJECTED, HOST_KEY_CHANGED: the host key of an sftp: server was not confirmed or is not the known one"},
		codeLocked, codeWrite,
	})
}

func printRepoRemoveHelp(stdout io.Writer) {
	printHelp(stdout, "repo remove [flags] <name>", `Takes a repository out of this host's configuration: removes its fragment, its
env_file (if it lies in the secrets directory and nothing else uses it) and its
systemd drop-in. restic is not called, the data in the storage is not touched,
and the password file stays: adding the same address again attaches the
repository with it. Sources on the server that refer to the name will be refused.
A repository of the main config is never removed (DEFINED_IN_CONFIG).

Flags:
  --config string   path to the agent config (default /etc/sard/agent.yaml)
  --no-restart      do not restart the service, say how to

`+sudoHelp+"\n"+applyingHelp, []repoHelpCode{
		codeOK, codeRestart, usageCode("flags, rights (PRIVILEGES_REQUIRED, SERVICE_USER_UNKNOWN), config, DEFINED_IN_CONFIG"), codeLocked, codeWrite,
	})
}

func printRepoPasswordHelp(stdout io.Writer) {
	printHelp(stdout, "repo password <name> --reveal [flags]", `Prints the password file of a repository to standard output, byte for byte, with
a warning on stderr: the password is then on the screen, in the history of the
terminal and in the output of scripts. Without --reveal nothing is printed
(REVEAL_REQUIRED). The backend is not called. Every reveal is written to the
system log (tag sard-agent), whoever runs it.

Flags:
  --config string   path to the agent config (default /etc/sard/agent.yaml)
  --reveal          really print the password

`+readOnlyHelp, []repoHelpCode{codeOK, usageCode("flags, rights (PRIVILEGES_REQUIRED), REVEAL_REQUIRED, config, REPOSITORY_UNKNOWN, PASSWORD_FILE_MISSING")})
}

func printRepoShowHelp(stdout io.Writer) {
	printHelp(stdout, "repo show [flags] <name>", `Shows one repository of the config: name, backend, address (a password in it
is hidden as ***), status (as in repo list), repository_id, password_file,
env_file and defined_in (the config file or fragment that defines it). One
restic cat config; the exit code is the class of the status, as for a row of
repo list.

Flags:
  --config string      path to the agent config (default /etc/sard/agent.yaml)
  --timeout duration   how long the whole command may take (default 2m)
  --json               print one JSON object (null for a missing value)

`+readOnlyHelp, []repoHelpCode{
		codeOK, {exitAgentError, "agent error", "restic is missing or unusable; the first permanent problem is an agent error"},
		usageCode("flags, rights (PRIVILEGES_REQUIRED), config, REPOSITORY_UNKNOWN, or a usage problem in the status"),
		{exitTemporary, "temporary", "the backend is unreachable, --timeout ran out, or the command was interrupted"},
	})
}
