// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

// "sard-agent status --config C": who this agent is, from its own
// certificate, offline (docs/specs/agent/agent-role.feature, С6). It reads
// the config and tls.cert_file only: no key, no network, no file modes.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/certrenew"
	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/identity"
)

func isStatusCommand(args []string) bool {
	return len(args) > 0 && args[0] == "status"
}

// statusDeps is what the command reaches outside its arguments.
type statusDeps struct {
	readFile func(name string) ([]byte, error)
	stat     func(name string) (os.FileInfo, error)
	// now is the agent's clock: the days left and the state depend on it.
	now func() time.Time
}

func realStatusDeps() statusDeps {
	return statusDeps{readFile: os.ReadFile, stat: os.Stat, now: time.Now}
}

func runStatus(args []string, stdout, stderr io.Writer, deps statusDeps) int {
	configPath, code := statusFlags(args, stderr)
	if code != exitOK {
		return code
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "sard-agent:", err)
		return exitError
	}
	if !deps.enrolled(cfg.TLS) {
		_, _ = fmt.Fprintln(stderr, "sard-agent: not enrolled: run sard-agent enroll")
		return exitError
	}
	id, err := identity.Read(cfg.TLS.CertFile, deps.readFile)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "sard-agent:", err)
		return exitError
	}
	_, _ = fmt.Fprintf(stdout, "agent_id: %s\ntenant_id: %s\nrole: %s\ncertificate_not_after: %s\n",
		id.AgentID, id.TenantID, id.Role, id.NotAfter.UTC().Format(time.RFC3339))
	_, _ = fmt.Fprint(stdout, renewalLines(id, statusCert{configPath: configPath, cfg: cfg}, deps))
	return exitOK
}

// statusCert is what the lines about the certificate need besides the certificate.
type statusCert struct {
	configPath string
	cfg        config.Config
}

// renewalLines are the lines after the four of С6 (П26): the days left, the
// state of the certificate and what goes with the state.
func renewalLines(id identity.Identity, c statusCert, deps statusDeps) string {
	now := deps.now()
	days := max(0, int(id.NotAfter.Sub(now)/(24*time.Hour)))
	state := certificateState(id, now, c.cfg, deps)
	out := fmt.Sprintf("certificate_days_left: %d\ncertificate_state: %s\n", days, state.name)
	switch state.name {
	case stateFailing:
		out += fmt.Sprintf("renewal_failures: %d\nrenewal_last_error: %s\nrenewal_next_attempt: %s\n",
			state.failing.Failures, state.failing.LastError, state.failing.NextAttempt.UTC().Format(time.RFC3339))
	case stateExpired:
		out += "re_enroll: " + reenrollCommand(c.configPath, c.cfg) + "\n"
	}
	if state.unsupported {
		out += "certificate_renewal: unsupported by server\n"
	}
	return out
}

const (
	stateOK      = "ok"
	stateDue     = "renewal-due"
	stateFailing = "renewal-failing"
	stateExpired = "expired"
)

type certState struct {
	name    string
	failing certrenew.State
	// unsupported: the server cannot renew certificates (the state file says so).
	unsupported bool
}

// certificateState: expired; else failing when the state file is about this
// certificate; else due when the threshold has come; else ok. A state file
// that is missing, unreadable, broken or about another certificate is as if
// there was none.
func certificateState(id identity.Identity, now time.Time, cfg config.Config, deps statusDeps) certState {
	if !now.Before(id.NotAfter) {
		return certState{name: stateExpired}
	}
	path := filepath.Join(executorStateDir(cfg.Executor.StateDir), certrenew.StateFile)
	st, ok := certrenew.ReadState(deps.readFile, path)
	ok = ok && st.Serial == id.Serial
	if ok && !st.Unsupported {
		return certState{name: stateFailing, failing: st}
	}
	state := certState{name: stateOK, unsupported: ok}
	if !now.Before(certrenew.Threshold(id.NotBefore, id.NotAfter)) {
		state.name = stateDue
	}
	return state
}

// statusFlags returns the config path, or exitUsage after saying what is wrong.
func statusFlags(args []string, stderr io.Writer) (string, int) {
	flags := flag.NewFlagSet("sard-agent status", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "path to the agent YAML config")
	if err := flags.Parse(args); err != nil {
		return "", exitUsage
	}
	if *configPath == "" || flags.NArg() > 0 {
		_, _ = fmt.Fprintln(stderr, "sard-agent status: --config <path> is required and no other arguments are accepted")
		flags.Usage()
		return "", exitUsage
	}
	return *configPath, exitOK
}

// enrolled: the agent has a certificate or a key file; a config with neither
// path set has none.
func (d statusDeps) enrolled(files config.TLS) bool {
	for _, path := range []string{files.CertFile, files.KeyFile} {
		if path == "" {
			continue
		}
		if _, err := d.stat(path); !errors.Is(err, fs.ErrNotExist) {
			return true
		}
	}
	return false
}
