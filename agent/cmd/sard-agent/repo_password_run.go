// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"

	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
	"github.com/Artur-Abalov/sard/agent/internal/repoinit"
)

// runRepoPassword is "sard-agent repo password <name> --reveal": the
// password file, byte for byte (Р20). The backend is never called; every
// reveal is audited, whoever runs it.
func runRepoPassword(_ context.Context, args []string, stdout, stderr io.Writer, deps hostDeps) int {
	opts, code := parseRepoFlags("password", args, stderr, deps)
	if code != exitOK {
		return code
	}
	who, f := authorize(deps, "repo password", opts, false, false)
	if f != nil {
		return report(stderr, "repo password", f)
	}
	if !opts.reveal {
		return report(stderr, "repo password", repoinit.Fail(repoinit.RevealRequired, "the password is shown on the screen only on request: add --reveal"))
	}
	return revealPassword(opts, who, stdout, stderr, deps)
}

// revealPassword prints the password file and audits it.
func revealPassword(opts hostOptions, who hostsetup.Principal, stdout, stderr io.Writer, deps hostDeps) int {
	cfg, code := loadConfigFor("repo password", opts.configPath, stderr)
	if code != exitOK {
		return code
	}
	repo, _, f := findRepository(cfg, opts.name, opts.configPath)
	if f != nil {
		return report(stderr, "repo password", f)
	}
	data, err := deps.readFile(repo.PasswordFile)
	if err != nil {
		return report(stderr, "repo password", passwordFileFailure(repo.PasswordFile, err))
	}
	c := newHostCmd("repo password", opts, who, stdout, stderr, deps)
	c.record("repository", repo.Name, "password revealed")
	_, _ = stdout.Write(data)
	_, _ = fmt.Fprintln(stderr, "warning: the password of this repository is now on your screen, in your terminal history and in the output of any script that captures it")
	return exitOK
}

func passwordFileFailure(path string, err error) *repoinit.Failure {
	if errors.Is(err, fs.ErrNotExist) {
		return repoinit.Fail(repoinit.PasswordFileMissing, "the password file %s does not exist", path)
	}
	return repoinit.Fail(repoinit.PasswordFileMissing, "the password file %s cannot be read: %v", path, err)
}
