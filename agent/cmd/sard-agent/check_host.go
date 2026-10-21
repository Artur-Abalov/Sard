// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/hostcheck"
	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
	"github.com/Artur-Abalov/sard/agent/internal/refusal"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

// programTimeout bounds one run of a program of the host.
const programTimeout = 30 * time.Second

// program is the answer of a program of the host.
type program struct {
	out  string
	code int
	// err is why it did not run (not installed, not allowed).
	err error
}

// runProgram runs a program with a plain environment; as is the user to run
// it as, nil for the caller.
func (c *hostCheck) runProgram(as *restic.RunAs, path string, args ...string) program {
	ctx, cancel := context.WithTimeout(c.ctx, programTimeout)
	defer cancel()
	var out bytes.Buffer
	code, err := c.deps.progExec.Run(ctx, restic.Command{
		Path: path, Args: args, Env: []string{"PATH=" + c.deps.pathEnv, "LC_ALL=C", "TZ=UTC"}, RunAs: as,
		Stdout: func(line []byte) { out.Write(line); out.WriteByte('\n') },
	})
	return program{out: out.String(), code: code, err: err}
}

// serviceRunAs is who a program that the service runs is run as.
func (c *hostCheck) serviceRunAs() *restic.RunAs {
	if c.who.Service.Name == "" {
		return nil
	}
	return runAs(c.who)
}

// resticBinary is restic.binary (Ч12).
func (c *hostCheck) resticBinary() hostcheck.Line {
	cfg := c.configured()
	as := c.serviceRunAs()
	binary, err := checkRestic(c.ctx, cfg, c.opts.configPath, c.deps.executable, c.deps.exec, as)
	var f *refusal.Failure
	if errors.As(err, &f) {
		return hostcheck.ResticFailureLine(f)
	}
	if err != nil {
		return hostcheck.ResticFailureLine(refusal.Fail(refusal.ResticUnusable, "%v", err))
	}
	version, err := restic.New(restic.Options{Binary: binary, CacheDir: cfg.Restic.CacheDir, Exec: c.deps.exec, RunAs: as}, config.Repository{}).Version(c.ctx)
	if err != nil {
		return hostcheck.ResticFailureLine(refusal.Fail(refusal.ResticUnusable, "%v", err))
	}
	c.binary = binary
	return hostcheck.ResticVersionLine(binary, version, restic.Pinned)
}

// pluginLines are plugin.declared and plugin.programs of every plugin. The
// plugin that needs programs of the host looks for them itself (sdk.ProgramChecker);
// the agent knows none (R2c-3).
func (c *hostCheck) pluginLines() {
	registry := c.deps.plugins
	var declared []hostcheck.PluginInfo
	for _, name := range registry.Names() {
		p, _ := registry.Get(name)
		declared = append(declared, hostcheck.PluginInfo{Name: name, Version: p.Version()})
	}
	c.emit(hostcheck.PluginDeclaredLine(declared))
	for _, info := range declared {
		p, _ := registry.Get(info.Name)
		checker, ok := p.(sdk.ProgramChecker)
		if !ok {
			c.emit(hostcheck.NoProgramsLine(info.Name))
			continue
		}
		c.emit(hostcheck.ProgramsLine(info.Name, checker.CheckPrograms(programHost{c})))
	}
}

// programHost is what the agent offers a plugin that looks for its programs:
// the PATH of the agent and the run of a program as the service user. A program
// is looked for by running it as that user: a directory the service cannot
// enter has no programs (Ч31).
type programHost struct{ c *hostCheck }

// Path is the directories of the PATH; an empty entry stands for the current
// directory and is left out.
func (h programHost) Path() []string {
	var dirs []string
	for _, dir := range filepath.SplitList(h.c.deps.pathEnv) {
		if dir != "" {
			dirs = append(dirs, dir)
		}
	}
	return dirs
}

// Run runs the program with its standard output captured; an error means it did not start.
func (h programHost) Run(path string, args ...string) (string, error) {
	p := h.c.runProgram(h.c.serviceRunAs(), path, args...)
	return p.out, p.err
}

// selinuxLines are selinux.labels and selinux.denials (Ч19): only a host where
// SELinux enforces has anything to check.
func (c *hostCheck) selinuxLines() {
	if p := c.runProgram(nil, "getenforce"); p.err != nil || !hostcheck.SELinuxEnforcing(p.out) {
		labels, denials := hostcheck.SELinuxOffLines()
		c.emit(labels)
		c.emit(denials)
		return
	}
	c.emit(c.labelsLine())
	c.emit(c.denialsLine())
}

func (c *hostCheck) labelsLine() hostcheck.Line {
	if failed := c.configBlock(); failed != "" {
		return hostcheck.NotCheckedLine(hostcheck.CheckSELinuxLabels, "", failed)
	}
	args := append([]string{"-V"}, c.labelPaths()...)
	p := c.runProgram(nil, "matchpathcon", args...)
	if p.err != nil {
		return hostcheck.Line{Check: hostcheck.CheckSELinuxLabels, Status: hostcheck.Skip, Message: "matchpathcon is not installed",
			Hint: "install the SELinux utilities (libselinux-utils), then run `sudo sard-agent check` again"}
	}
	return hostcheck.SELinuxLabelsLine(hostcheck.ParseMatchpathcon(p.out))
}

// labelPaths are the files and directories of the agent that exist (Ч19).
func (c *hostCheck) labelPaths() []string {
	cfg := c.cfg
	layout := hostsetup.Layout{Config: c.opts.configPath}
	candidates := []string{c.opts.configPath, layout.FragmentDir(), layout.SecretsDir(), filepath.Dir(cfg.TLS.KeyFile),
		cfg.TLS.CAFile, cfg.TLS.CertFile, cfg.TLS.KeyFile, c.binary, cfg.Restic.CacheDir, c.deps.defaultCacheDir, c.stateDir()}
	candidates = append(candidates, secretFiles(cfg)[1:]...)
	for _, r := range cfg.Repositories {
		if r.Backend() == "local" {
			candidates = append(candidates, r.URL)
		}
	}
	var paths []string
	seen := map[string]bool{"": true, ".": true}
	for _, p := range candidates {
		if _, err := c.deps.stat(p); err == nil && !seen[p] {
			seen[p] = true
			paths = append(paths, p)
		}
	}
	return paths
}

func (c *hostCheck) denialsLine() hostcheck.Line {
	if c.who.Role != hostsetup.RoleRoot {
		c.needsRoot = true
		return hostcheck.NeedsRootLine(hostcheck.CheckSELinuxDenials, "", "the audit log can be read by root only")
	}
	p := c.runProgram(nil, "ausearch", "-m", "avc", "-ts", "yesterday")
	if p.err != nil {
		return hostcheck.Line{Check: hostcheck.CheckSELinuxDenials, Status: hostcheck.Skip, Message: "ausearch is not installed",
			Hint: "install the audit package, then run `sudo sard-agent check` again"}
	}
	return hostcheck.SELinuxDenialsLine(hostcheck.ParseDenials(p.out, c.now.Add(-24*time.Hour)))
}

// serviceLines are service.running and service.steps (Ч21): only systemctl
// show, nothing is started or restarted.
func (c *hostCheck) serviceLines() {
	c.emit(c.runningLine())
	c.emit(c.stepsLine())
}

func (c *hostCheck) runningLine() hostcheck.Line {
	if !c.deps.systemd.Present() {
		return hostcheck.Line{Check: hostcheck.CheckServiceRunning, Status: hostcheck.Skip, Message: "systemd does not run on this host",
			Hint: "check how the agent is started on this host"}
	}
	p := c.runProgram(nil, "systemctl", "show", "sard-agent.service", "-p", "ActiveState", "-p", "SubState", "-p", "NRestarts", "-p", "ActiveEnterTimestamp")
	state, err := hostcheck.ParseShow(p.out)
	if p.err != nil || err != nil {
		return hostcheck.Line{Check: hostcheck.CheckServiceRunning, Status: hostcheck.Warn, Message: fmt.Sprintf("systemctl show did not answer: %v", errors.Join(p.err, err)),
			Hint: "see " + hostcheck.HelpURL(hostcheck.CheckServiceRunning)}
	}
	return hostcheck.ServiceRunningLine(state, c.now)
}

// stepsLine counts the steps the executor journaled and has no result for.
func (c *hostCheck) stepsLine() hostcheck.Line {
	entries, err := c.deps.fs.ReadDir(filepath.Join(c.stateDir(), "journal"))
	if errors.Is(err, fs.ErrNotExist) {
		return hostcheck.ServiceStepsLine(0, nil)
	}
	n := 0
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") && !strings.HasPrefix(e.Name(), ".tmp-") {
			n++
		}
	}
	return hostcheck.ServiceStepsLine(n, err)
}
