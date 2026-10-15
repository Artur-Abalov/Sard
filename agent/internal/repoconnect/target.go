// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package repoconnect

import (
	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
	"github.com/Artur-Abalov/sard/agent/internal/repoinit"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
)

// TargetFor is the repository as the messages of a failure describe it
// (Р34): its name, backend and password file, the address with the password
// hidden, and for an s3: or sftp: address that parses, the bucket or the
// directory that tells a refusal of the storage apart. env are the KEY=VALUE
// lines of its env file and secrets other values the messages must not show.
func TargetFor(repo config.Repository, env []string, secrets ...string) repoinit.Target {
	target := repoinit.Target{
		Name: repo.Name, Backend: repo.Backend(), PasswordFile: repo.PasswordFile,
		Scrub: repoinit.Scrubber(repo.URL, env, secrets...),
		Where: config.RedactURL(repo.URL),
	}
	switch target.Backend {
	case "s3":
		if a, f := hostsetup.CheckS3Address(repo.URL); f == nil {
			target.Remote, target.Bucket = true, a.Bucket
		}
	case "sftp":
		if a, f := hostsetup.CheckSFTPAddress(repo.URL); f == nil {
			target.Remote, target.Directory = true, a.Path
		}
	}
	return target
}

// TargetReading is TargetFor for a running agent, whose messages go to the
// server: the env file is read now, to hide its values. The password file is
// never read (restic gets it by path, ADR 0008) and its path is not kept: the
// messages name the repository instead. A file that cannot be read gives
// nothing to hide.
func TargetReading(repo config.Repository, readFile func(string) ([]byte, error)) repoinit.Target {
	var env []string
	if repo.EnvFile != "" {
		if data, err := readFile(repo.EnvFile); err == nil {
			env, _ = restic.ParseEnvFile(data)
		}
	}
	repo.PasswordFile = ""
	return TargetFor(repo, env)
}
