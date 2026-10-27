// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Artur Abalov

package sdk

import (
	"context"
	"io"
)

// SandboxPassword is the name under which a SandboxHost gives the password of
// the application of its sandbox (Host.Secret): a random one per check.
const SandboxPassword = "sard.sandbox"

// Sandboxed is implemented by a CopyRestorer whose copy runs in a throwaway
// container on the verifier, the sandbox (V5): a database restored into the
// server it needs. The verifier starts the sandbox, the plugin restores,
// opens and measures the copy in it through the SandboxHost.
type Sandboxed interface {
	// Sandbox is the sandbox a copy of the source of f needs; f is nil for a
	// snapshot without a fingerprint.
	Sandbox(f *Fingerprint) (SandboxSpec, error)
	// SandboxReady returns once the application of the sandbox serves, and
	// judges whether it can hold the copy of f: a *SandboxRefusal says why
	// not (an extension the image lacks), which is no fault of the backup.
	SandboxReady(ctx context.Context, h SandboxHost, f *Fingerprint) error
	// OpenCopy proves that the restored copy opens in the application.
	OpenCopy(ctx context.Context, h SandboxHost, c RestoredCopy) error
}

// SandboxSpec says what a sandbox runs.
type SandboxSpec struct {
	// Image is the name of an image of the verifier's list, e.g. "postgres";
	// Major the major version it must be of, empty for the newest.
	Image, Major string
	// DataDir is the directory of the container where the copy lives: the
	// verifier mounts a directory of the job there, owned by its own user.
	DataDir string
	// Env is the environment of the container; PasswordEnv, if set, names the
	// variable that gets the password of SandboxPassword.
	Env         []string
	PasswordEnv string
}

// SandboxHost is the RestoreHost of a check whose copy runs in a sandbox. Its
// Secret(SandboxPassword) is the password of the sandbox.
type SandboxHost interface {
	RestoreHost
	// Run runs a program of the sandbox to completion and returns its exit
	// code; -1 and an error when it did not run. Path is the program's path
	// in the container, or a name found by the container's PATH.
	Run(ctx context.Context, p Process) (int, error)
}

// Process is a program run in a sandbox.
type Process struct {
	Path string
	Args []string
	// Env are the variables the program gets besides the container's own.
	Env []string
	// Stdin is its standard input, nil an empty one; Stdout gets its
	// standard output as written, nil discards it; Stderr each line of its
	// standard error, without the line ending.
	Stdin  io.Reader
	Stdout io.Writer
	Stderr func(line string)
}

// SandboxRefusal is a sandbox that cannot hold the copy: the verifier cannot
// verify, the backup is not bad.
type SandboxRefusal struct {
	Reason string
}

func (e *SandboxRefusal) Error() string { return e.Reason }
