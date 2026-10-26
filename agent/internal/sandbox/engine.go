// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

// Package sandbox starts the throwaway containers in which a verifier brings
// a restored copy up (V5): Docker or Podman, driven through their CLI, with no
// network and within limits, from an image of the verifier's list run by its
// digest. Every container carries the label of its job, so that the agent
// removes what a verification left whatever happened to it.
package sandbox

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

// JobLabel is the label of every container of a verification: its job.
const JobLabel = "sard.verify.job"

// Why a sandbox does not start; none says anything about the backup.
var (
	ErrNoRuntime   = errors.New("neither Docker nor Podman answers the service user")
	ErrNoImage     = errors.New("no image of the verifier's list fits")
	ErrImageAbsent = errors.New("the image is not on this host")
	// ErrStopped is a program that did not run: its container has stopped.
	ErrStopped = errors.New("the sandbox has stopped")
)

// Limits bound a sandbox: CPUs and memory as the runtime's flags take them.
type Limits struct {
	CPUs   string
	Memory string
	Pids   int
}

// Engine starts sandboxes.
type Engine struct {
	CLI interface {
		CLI
		Streamer
	}
	Images []Image
	// Pull lets a missing image be pulled by its digest.
	Pull   bool
	Limits Limits
	// User is "uid:gid" of the agent: the sandbox writes its files as it.
	User string
	// Runtime is the runtime to use now (Detect, or what the status found).
	Runtime func(ctx context.Context) Runtime
}

// Container is a running sandbox.
type Container struct {
	cli   Streamer
	out   CLI
	rt    Runtime
	name  string
	image Image
}

func containerName(job string) string { return "sard-verify-" + job }

// Start starts the sandbox of job: the newest image of spec's name and major
// version, present or pulled, with dataDir mounted at spec.DataDir.
func (e *Engine) Start(ctx context.Context, job string, spec sdk.SandboxSpec, dataDir, password string) (*Container, error) {
	rt := e.Runtime(ctx)
	if rt.Binary == "" {
		return nil, ErrNoRuntime
	}
	img, ok := Pick(e.Images, spec.Image, spec.Major)
	if !ok {
		return nil, fmt.Errorf("%w: %s %s", ErrNoImage, spec.Image, orNewest(spec.Major))
	}
	if err := e.ensure(ctx, rt, img); err != nil {
		return nil, err
	}
	c := &Container{cli: e.CLI, out: e.CLI, rt: rt, name: containerName(job), image: img}
	run := Command{Name: rt.Binary, Args: e.runArgs(rt, job, spec, dataDir, img)}
	if spec.PasswordEnv != "" {
		run.Env = []string{spec.PasswordEnv + "=" + password}
	}
	if err := e.run(ctx, rt, run); err != nil {
		_ = c.Remove(context.WithoutCancel(ctx))
		return nil, fmt.Errorf("start the sandbox %s: %w", describe(img), err)
	}
	return c, nil
}

// run runs the container: a runtime that refuses it exits non-zero, with its
// reason on stderr, and no error of the CLI.
func (e *Engine) run(ctx context.Context, rt Runtime, cmd Command) error {
	var said []string
	cmd.Stderr = func(l string) { said = append(said, l) }
	code, err := e.CLI.Stream(ctx, cmd)
	if err == nil && code != 0 {
		err = fmt.Errorf("%s run exited %d: %s", rt.Binary, code, strings.Join(said, "; "))
	}
	return err
}

func orNewest(major string) string {
	if major == "" {
		return "(the newest)"
	}
	return major
}

func describe(img Image) string { return fmt.Sprintf("%s %s (%s)", img.Name, img.Version, img.Ref()) }

// ensure makes the image present: there already, or pulled when allowed.
func (e *Engine) ensure(ctx context.Context, rt Runtime, img Image) error {
	if _, err := e.CLI.Output(ctx, rt.Binary, "image", "inspect", "--format", "{{.Id}}", img.Ref()); err == nil {
		return nil
	}
	if !e.Pull {
		return fmt.Errorf("%w: %s, and verify.pull is off", ErrImageAbsent, describe(img))
	}
	if _, err := e.CLI.Output(ctx, rt.Binary, "pull", img.Ref()); err != nil {
		return fmt.Errorf("%w: %s, and the pull failed: %v", ErrImageAbsent, describe(img), err)
	}
	return nil
}

func (e *Engine) runArgs(rt Runtime, job string, spec sdk.SandboxSpec, dataDir string, img Image) []string {
	args := []string{
		"run", "-d", "--name", containerName(job), "--label", JobLabel + "=" + job, "--network", "none",
		"--cpus", e.Limits.CPUs, "--memory", e.Limits.Memory, "--pids-limit", strconv.Itoa(e.Limits.Pids), "--user", e.User,
	}
	if rt.Kind == agentv1.Sandbox_SANDBOX_PODMAN {
		// Rootless: the agent's uid inside is its uid outside, owner of dataDir.
		args = append(args, "--userns=keep-id")
	}
	args = append(args, "-v", dataDir+":"+spec.DataDir)
	for _, env := range spec.Env {
		args = append(args, "-e", env)
	}
	if spec.PasswordEnv != "" {
		args = append(args, "-e", spec.PasswordEnv)
	}
	return append(args, img.Ref())
}

// Image is the image the container runs.
func (c *Container) Image() Image { return c.image }

// Run runs p in the container. Its variables reach it through the
// environment of the CLI, so that no value shows in a command line. In a
// container that has stopped p does not run: -1 and ErrStopped.
func (c *Container) Run(ctx context.Context, p sdk.Process) (int, error) {
	args := []string{"exec", "-i"}
	for _, env := range p.Env {
		name, _, _ := strings.Cut(env, "=")
		args = append(args, "-e", name)
	}
	args = append(append(args, c.name, p.Path), p.Args...)
	stderr := &stopped{next: p.Stderr}
	code, err := c.cli.Stream(ctx, Command{Name: c.rt.Binary, Args: args, Env: p.Env, Stdin: p.Stdin, Stdout: p.Stdout, Stderr: stderr.line})
	if err == nil && code != 0 && stderr.refusal != "" {
		return -1, fmt.Errorf("%w: %s", ErrStopped, stderr.refusal)
	}
	return code, err
}

// stopped is the stderr of an exec: the runtime's refusal of a stopped
// container is kept, every other line is the program's.
type stopped struct {
	next    func(string)
	refusal string
}

func (s *stopped) line(l string) {
	switch {
	case stoppedSays(l):
		s.refusal = l
	case s.next != nil:
		s.next(l)
	}
}

// stoppedSays: the line is the runtime refusing an exec in a stopped
// container, Docker's words or Podman's.
func stoppedSays(l string) bool {
	return strings.HasPrefix(l, "Error response from daemon: container ") && strings.HasSuffix(l, " is not running") ||
		strings.Contains(l, "can only create exec sessions on running containers")
}

// Remove removes the container with its anonymous volumes.
func (c *Container) Remove(ctx context.Context) error {
	_, err := c.out.Output(ctx, c.rt.Binary, "rm", "-f", "-v", c.name)
	return err
}

// Sweep removes every container of a verification (JobLabel) and returns
// their ids; it runs at the start of the agent.
func (e *Engine) Sweep(ctx context.Context) ([]string, error) {
	rt := e.Runtime(ctx)
	if rt.Binary == "" {
		return nil, nil
	}
	out, err := e.CLI.Output(ctx, rt.Binary, "ps", "-aq", "--filter", "label="+JobLabel)
	if err != nil {
		return nil, err
	}
	var removed []string
	for _, id := range strings.Fields(string(out)) {
		if _, err := e.CLI.Output(ctx, rt.Binary, "rm", "-f", "-v", id); err != nil {
			return removed, fmt.Errorf("remove the sandbox %s: %w", id, err)
		}
		removed = append(removed, id)
	}
	return removed, nil
}
