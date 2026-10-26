// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package sandbox

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"

	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

// CLI runs a command of a container runtime and returns its stdout.
type CLI interface {
	Output(ctx context.Context, name string, args ...string) ([]byte, error)
}

// Command is a command of a container runtime with streams.
type Command struct {
	Name string
	Args []string
	// Env are variables added to the environment of the CLI.
	Env    []string
	Stdin  io.Reader
	Stdout io.Writer
	// Stderr gets each line of the standard error.
	Stderr func(line string)
}

// Streamer runs a command to completion and returns its exit code; -1 and an
// error when it did not start or was killed.
type Streamer interface {
	Stream(ctx context.Context, cmd Command) (int, error)
}

// ExecCLI runs the binaries of PATH.
type ExecCLI struct{}

// Output implements CLI.
func (ExecCLI) Output(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).Output()
}

// Stream implements Streamer: the process gets the agent's environment and
// cmd.Env; a non-zero exit code is no error.
func (ExecCLI) Stream(ctx context.Context, cmd Command) (int, error) {
	c := exec.CommandContext(ctx, cmd.Name, cmd.Args...)
	c.Env = append(os.Environ(), cmd.Env...)
	c.Stdin, c.Stdout = cmd.Stdin, cmd.Stdout
	stderr, err := c.StderrPipe()
	if err != nil {
		return -1, err
	}
	if err := c.Start(); err != nil {
		return -1, err
	}
	lines(stderr, cmd.Stderr)
	err = c.Wait()
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() >= 0 {
		return exit.ExitCode(), nil
	}
	if err != nil {
		return -1, err
	}
	return 0, nil
}

// lines hands each line of r to each; nil reads it to the end only.
func lines(r io.Reader, each func(string)) {
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 64*1024), 1024*1024)
	for s.Scan() {
		if each != nil {
			each(s.Text())
		}
	}
	_, _ = io.Copy(io.Discard, r)
}

// Runtime is the container runtime a verifier runs its sandbox with.
type Runtime struct {
	Kind agentv1.Sandbox
	// Binary is the CLI, "podman" or "docker"; empty without a runtime.
	Binary string
	// Version is what the runtime reports: Podman's client, Docker's daemon.
	Version string
}

// probes are the runtimes in the order they are tried: rootless Podman first,
// as recommended; Docker by its daemon, which a service user outside the group
// docker cannot reach.
var probes = []struct {
	kind   agentv1.Sandbox
	binary string
	format string
}{
	{agentv1.Sandbox_SANDBOX_PODMAN, "podman", "{{.Client.Version}}"},
	{agentv1.Sandbox_SANDBOX_DOCKER, "docker", "{{.Server.Version}}"},
}

// Detect finds the first runtime that answers; none is SANDBOX_NONE.
func Detect(ctx context.Context, cli CLI) Runtime {
	for _, p := range probes {
		out, err := cli.Output(ctx, p.binary, "version", "--format", p.format)
		if version := strings.TrimSpace(string(out)); err == nil && version != "" {
			return Runtime{Kind: p.kind, Binary: p.binary, Version: version}
		}
	}
	return Runtime{Kind: agentv1.Sandbox_SANDBOX_NONE}
}

// Image is an image of the verifier's list: a name, the semantic version it
// is of, and the digest it is run by.
type Image struct {
	Name, Version, Digest string
}

// Ref is the reference the runtime pulls and runs it by: name@digest.
func (i Image) Ref() string { return i.Name + "@" + i.Digest }

// Present returns the images of list the runtime has locally.
func (r Runtime) Present(ctx context.Context, cli CLI, list []Image) []Image {
	if r.Binary == "" {
		return nil
	}
	var present []Image
	for _, img := range list {
		if _, err := cli.Output(ctx, r.Binary, "image", "inspect", "--format", "{{.Id}}", img.Ref()); err == nil {
			present = append(present, img)
		}
	}
	return present
}
