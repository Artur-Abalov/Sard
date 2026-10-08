// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"crypto/rand"
	"io"
	"os"

	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
	"github.com/Artur-Abalov/sard/agent/internal/repoconnect"
	"github.com/Artur-Abalov/sard/agent/internal/repoinit"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
	"github.com/Artur-Abalov/sard/agent/internal/secrets"
)

// hostDeps is everything the commands "sard-agent repo ..." and "sard-agent
// secret ..." reach outside their arguments; tests substitute restic, the
// clock, the user lookup, the file system, systemd, the system log and the
// terminal.
type hostDeps struct {
	clock      repoconnect.TimeClock
	exec       restic.Executor
	executable func() (string, error)
	// euid is the effective user id of the process: the right to run a
	// command comes from it (Р5).
	euid     uint32
	stat     secrets.StatFunc
	readFile func(name string) ([]byte, error)
	// readOwned reads a secret file as A1 allows it to be: a plain file of
	// the given uid, no group or other bits (secrets.ReadOwned).
	readOwned func(path string, uid uint32) ([]byte, error)
	writeNew  func(path string, data []byte) error
	random    io.Reader
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
}

func productionHostDeps() hostDeps {
	return hostDeps{
		clock:      realEnrollClock{},
		exec:       restic.ProcessExecutor{},
		executable: os.Executable,
		euid:       uint32(os.Geteuid()),
		stat:       secrets.RealStat,
		readFile:   os.ReadFile,
		readOwned:  secrets.ReadOwned,
		writeNew:   repoinit.WriteNew,
		random:     rand.Reader,
		openLock:   os.OpenFile,
		pathEnv:    os.Getenv("PATH"),

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
	}
}

func (d hostDeps) host(serviceUID uint32) repoinit.Host {
	return repoinit.Host{UID: serviceUID, Stat: d.stat, ReadFile: d.ownedReader(serviceUID)}
}

// ownedReader reads files as the service user owns them (A1).
func (d hostDeps) ownedReader(uid uint32) func(string) ([]byte, error) {
	return func(path string) ([]byte, error) { return d.readOwned(path, uid) }
}
