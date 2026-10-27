// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"context"
	"crypto/rand"
	"io"
	"os"

	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/hostcheck"
	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
	"github.com/Artur-Abalov/sard/agent/internal/identity"
	"github.com/Artur-Abalov/sard/agent/internal/repoconnect"
	"github.com/Artur-Abalov/sard/agent/internal/repoinit"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
	"github.com/Artur-Abalov/sard/agent/internal/role"
	"github.com/Artur-Abalov/sard/agent/internal/secretbind"
	"github.com/Artur-Abalov/sard/agent/internal/secrets"
	"github.com/Artur-Abalov/sard/agent/internal/serverprobe"
	"github.com/Artur-Abalov/sard/agent/plugins"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

// hostDeps is everything the commands "sard-agent repo ..." and "sard-agent
// secret ..." reach outside their arguments; tests substitute restic, the
// clock, the user lookup, the file system, systemd, the system log and the
// terminal.
type hostDeps struct {
	clock repoconnect.TimeClock
	exec  restic.Executor
	// sshExec runs the programs of the OpenSSH client (ssh-keyscan,
	// ssh-keygen, sftp) as the service user (A8b-2).
	sshExec restic.Executor
	// progExec runs the programs of the host that sard-agent check looks at:
	// getenforce, matchpathcon, ausearch, systemctl show and the programs
	// the plugins ask for (A8d-2, R2c-3).
	progExec restic.Executor
	// plugins are the plugins of the agent: sard-agent check asks them for their
	// programs and repo restore for the files of a snapshot (R2c-3).
	plugins *sdk.Registry
	// probeServer asks the server: connect, handshake, Probe (A8d-3).
	probeServer func(ctx context.Context, r serverprobe.Request) hostcheck.ServerFacts
	// hostname is the name of this host, for the comment of a new ssh key.
	hostname   func() (string, error)
	executable func() (string, error)
	// loadConfig reads the config of sard-agent check (A8d-2).
	loadConfig func(path string) (config.Config, error)
	// pidAlive says whether a process of this host is running: a lock of
	// restic from a process that is gone is stale (A8d).
	pidAlive func(pid int) bool
	// euid is the effective user id of the process: the right to run a
	// command comes from it (Р5).
	euid     uint32
	stat     secrets.StatFunc
	readFile func(name string) ([]byte, error)
	// readOwned reads a secret file as A1 allows it to be: a plain file of
	// the given uid, no group or other bits (secrets.ReadOwned).
	readOwned func(path string, uid uint32) ([]byte, error)
	// readBinding reads a binding file as the agent does: one open that
	// follows no symlink (secretbind.ReadFile, S11).
	readBinding secretbind.ReadFunc
	writeNew    func(path string, data []byte) error
	random      io.Reader
	// openLock opens the init lock file and the config lock file.
	openLock repoinit.OpenFunc
	pathEnv  string
	// defaultConfig is the config used without --config.
	defaultConfig string
	// defaultCacheDir is restic.cache_dir when the config leaves it empty.
	defaultCacheDir string

	// lookupUser finds the service user.
	lookupUser hostsetup.LookupFunc
	// getenv reads SUDO_USER and SUDO_UID for the audit line.
	getenv func(string) string
	// processUser is the name and the uid of the process, for the audit
	// line when there is no sudo.
	processUser func() (string, uint32)
	fs          hostsetup.FS
	systemd     hostsetup.Systemd
	openAudit   hostsetup.OpenAuditor
	// dropInDir is the systemd drop-in directory of the service unit.
	dropInDir string
	stdin     io.Reader
	// terminal is the operator's terminal for secret input, nil if stdin is
	// not a terminal; out receives its prompts.
	terminal func(out io.Writer) hostsetup.Terminal
	openFile func(path string) (io.ReadCloser, error)
	// stdoutTerminal says that standard output is a terminal: the only place
	// the password of a new key may be shown (V3, K7).
	stdoutTerminal bool
	// hostRole is the role the certificate at the path says this host has;
	// a host without a usable certificate acts as a source (V3, В5).
	hostRole func(certFile string) role.Role
}

func productionHostDeps() hostDeps {
	return hostDeps{
		clock:       realEnrollClock{},
		exec:        restic.ProcessExecutor{},
		sshExec:     restic.ProcessExecutor{},
		progExec:    restic.ProcessExecutor{},
		plugins:     plugins.Registry(version),
		hostname:    os.Hostname,
		executable:  os.Executable,
		pidAlive:    processAlive,
		loadConfig:  config.Load,
		probeServer: serverprobe.Run,
		euid:        uint32(os.Geteuid()),
		stat:        secrets.RealStat,
		readFile:    os.ReadFile,
		readOwned:   secrets.ReadOwned,
		readBinding: secretbind.ReadFile,
		writeNew:    repoinit.WriteNew,
		random:      rand.Reader,
		openLock:    os.OpenFile,
		pathEnv:     os.Getenv("PATH"),

		defaultConfig:   defaultEnrollConfigPath,
		defaultCacheDir: restic.DefaultCacheDir,

		lookupUser:  hostsetup.LookupOS,
		getenv:      os.Getenv,
		processUser: hostsetup.ProcessUser,
		fs:          hostsetup.OS{},
		systemd:     hostsetup.NewExecSystemd(),
		openAudit:   hostsetup.OpenSyslog,
		dropInDir:   "/etc/systemd/system/sard-agent.service.d",
		stdin:       os.Stdin,
		terminal:    func(out io.Writer) hostsetup.Terminal { return hostsetup.StdinTerminal(os.Stdin, out) },
		openFile:    func(path string) (io.ReadCloser, error) { return os.Open(path) },

		stdoutTerminal: hostsetup.IsTerminal(os.Stdout),
		hostRole:       certificateRole(os.ReadFile),
	}
}

func (d hostDeps) host(serviceUID uint32) repoinit.Host {
	return repoinit.Host{UID: serviceUID, Stat: d.stat, ReadFile: d.ownedReader(serviceUID)}
}

// bindings are the bindings next to the config at configPath, judged as the
// agent running as uid judges them (S11).
func (d hostDeps) bindings(configPath string, uid uint32) *secretbind.Store {
	return secretbind.NewStore(hostsetup.Layout{Config: configPath}.SecretBinding, uid, d.readBinding)
}

// ownedReader reads files as the service user owns them (A1).
func (d hostDeps) ownedReader(uid uint32) func(string) ([]byte, error) {
	return func(path string) ([]byte, error) { return d.readOwned(path, uid) }
}

// certificateRole is the role of the certificate at the path; a host that has
// none, or one that cannot be read, behaves as a source (V3, В5).
func certificateRole(read func(string) ([]byte, error)) func(string) role.Role {
	return func(certFile string) role.Role {
		id, err := identity.Read(certFile, read)
		if err != nil {
			return role.Source
		}
		return id.Role
	}
}
