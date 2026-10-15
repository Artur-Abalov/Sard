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
	"time"

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
}

func realStatusDeps() statusDeps {
	return statusDeps{readFile: os.ReadFile, stat: os.Stat}
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
	return exitOK
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
