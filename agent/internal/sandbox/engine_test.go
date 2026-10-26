// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package sandbox_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/sandbox"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

// engineCLI answers the commands of a docker CLI: the images it has, the
// containers it runs; streamed commands are recorded with their environment.
type engineCLI struct {
	cli
	images  map[string]bool
	pulls   map[string]bool
	ps      string
	psErr   error
	rmErr   map[string]error
	runErr  error
	runCode int
	gone    string
	streams []sandbox.Command
	code    int
}

func (c *engineCLI) Output(ctx context.Context, name string, args ...string) ([]byte, error) {
	line := strings.Join(append([]string{name}, args...), " ")
	c.calls = append(c.calls, line)
	switch {
	case slices.Contains(args, "inspect"):
		if c.images[args[len(args)-1]] {
			return []byte("sha256:0f"), nil
		}
		return nil, errors.New("No such image")
	case args[0] == "pull":
		if c.pulls[args[1]] {
			c.images[args[1]] = true
			return nil, nil
		}
		return nil, errors.New("toomanyrequests")
	case args[0] == "ps":
		return []byte(c.ps), c.psErr
	case args[0] == "rm":
		return nil, c.rmErr[args[len(args)-1]]
	}
	return nil, errors.New("unexpected " + line)
}

func (c *engineCLI) Stream(_ context.Context, cmd sandbox.Command) (int, error) {
	c.streams = append(c.streams, cmd)
	if cmd.Args[0] == "run" {
		if c.runCode != 0 && cmd.Stderr != nil {
			cmd.Stderr("docker: Error response from daemon: invalid value for --cpus")
		}
		return c.runCode, c.runErr
	}
	if cmd.Stdout != nil {
		_, _ = io.WriteString(cmd.Stdout, "out")
	}
	if cmd.Stdin != nil {
		data, _ := io.ReadAll(cmd.Stdin)
		_, _ = cmd.Stdout.Write(data)
	}
	if c.gone != "" {
		cmd.Stderr(c.gone)
		return 1, nil
	}
	if cmd.Stderr != nil {
		cmd.Stderr("a line")
	}
	return c.code, nil
}

var images = []sandbox.Image{
	{Name: "postgres", Version: "16.15", Digest: "sha256:16"},
	{Name: "postgres", Version: "17.9", Digest: "sha256:179"},
	{Name: "postgres", Version: "17.11", Digest: "sha256:1711"},
	{Name: "postgres", Version: "18.6", Digest: "sha256:18"},
	{Name: "other", Version: "99.0", Digest: "sha256:99"},
}

func engine(c *engineCLI, kind agentv1.Sandbox) *sandbox.Engine {
	binary := map[agentv1.Sandbox]string{agentv1.Sandbox_SANDBOX_DOCKER: "docker", agentv1.Sandbox_SANDBOX_PODMAN: "podman"}[kind]
	return &sandbox.Engine{
		CLI: c, Images: images, Pull: true, User: "65532:65532",
		Limits:  sandbox.Limits{CPUs: "2", Memory: "2g", Pids: 512},
		Runtime: func(context.Context) sandbox.Runtime { return sandbox.Runtime{Kind: kind, Binary: binary} },
	}
}

var spec = sdk.SandboxSpec{
	Image: "postgres", Major: "17", DataDir: "/var/lib/postgresql/data",
	Env: []string{"PGDATA=/var/lib/postgresql/data/pgdata"}, PasswordEnv: "POSTGRES_PASSWORD",
}

func TestTheSandboxRunsTheNewestImageOfTheMajorVersionWithoutNetworkWithinLimits(t *testing.T) {
	c := &engineCLI{images: map[string]bool{"postgres@sha256:1711": true}}

	box, err := engine(c, agentv1.Sandbox_SANDBOX_DOCKER).Start(t.Context(), "job-1", spec, "/verify/job-1/data", "s3cret")

	if err != nil {
		t.Fatal(err)
	}
	if box.Image() != images[2] {
		t.Errorf("image %+v", box.Image())
	}
	run := c.streams[0]
	want := []string{
		"run", "-d", "--name", "sard-verify-job-1", "--label", "sard.verify.job=job-1", "--network", "none",
		"--cpus", "2", "--memory", "2g", "--pids-limit", "512", "--user", "65532:65532",
		"-v", "/verify/job-1/data:/var/lib/postgresql/data", "-e", "PGDATA=/var/lib/postgresql/data/pgdata",
		"-e", "POSTGRES_PASSWORD", "postgres@sha256:1711",
	}
	if run.Name != "docker" || !slices.Equal(run.Args, want) {
		t.Fatalf("run %s %q", run.Name, run.Args)
	}
	// The password reaches the container through the environment of the CLI, never its command line.
	if !slices.Equal(run.Env, []string{"POSTGRES_PASSWORD=s3cret"}) || strings.Contains(strings.Join(run.Args, " "), "s3cret") {
		t.Errorf("env %q", run.Env)
	}
}

func TestRootlessPodmanKeepsTheUserOfTheAgent(t *testing.T) {
	c := &engineCLI{images: map[string]bool{"postgres@sha256:1711": true}}

	if _, err := engine(c, agentv1.Sandbox_SANDBOX_PODMAN).Start(t.Context(), "j", spec, "/d", "p"); err != nil {
		t.Fatal(err)
	}
	if run := c.streams[0]; run.Name != "podman" || !slices.Contains(run.Args, "--userns=keep-id") {
		t.Fatalf("run %s %q", run.Name, run.Args)
	}
}

func TestWithoutAMajorVersionTheNewestImageOfTheNameRuns(t *testing.T) {
	c := &engineCLI{images: map[string]bool{"postgres@sha256:18": true}}
	any := spec
	any.Major = ""

	box, err := engine(c, agentv1.Sandbox_SANDBOX_DOCKER).Start(t.Context(), "j", any, "/d", "p")

	if err != nil || box.Image().Version != "18.6" {
		t.Fatalf("image %+v, %v", box, err)
	}
}

func TestAnImageMissingFromTheHostIsPulledByItsDigest(t *testing.T) {
	c := &engineCLI{images: map[string]bool{}, pulls: map[string]bool{"postgres@sha256:1711": true}}

	if _, err := engine(c, agentv1.Sandbox_SANDBOX_DOCKER).Start(t.Context(), "j", spec, "/d", "p"); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(c.calls, "docker pull postgres@sha256:1711") {
		t.Fatalf("calls %q", c.calls)
	}
}

func TestASandboxThatCannotBeStartedSaysWhy(t *testing.T) {
	cases := map[string]struct {
		e    func(*engineCLI) *sandbox.Engine
		spec sdk.SandboxSpec
		want error
	}{
		"no runtime": {func(c *engineCLI) *sandbox.Engine { return engine(c, agentv1.Sandbox_SANDBOX_NONE) }, spec, sandbox.ErrNoRuntime},
		"not in the list": {func(c *engineCLI) *sandbox.Engine { return engine(c, agentv1.Sandbox_SANDBOX_DOCKER) },
			sdk.SandboxSpec{Image: "postgres", Major: "12"}, sandbox.ErrNoImage},
		"pull forbidden": {func(c *engineCLI) *sandbox.Engine {
			e := engine(c, agentv1.Sandbox_SANDBOX_DOCKER)
			e.Pull = false
			return e
		}, spec, sandbox.ErrImageAbsent},
		"pull failed": {func(c *engineCLI) *sandbox.Engine { return engine(c, agentv1.Sandbox_SANDBOX_DOCKER) }, spec, sandbox.ErrImageAbsent},
	}
	for name, tc := range cases {
		c := &engineCLI{images: map[string]bool{}, pulls: map[string]bool{}}
		if _, err := tc.e(c).Start(t.Context(), "j", tc.spec, "/d", "p"); !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestTheNamesInTheReasonsAreTheImageAndItsVersion(t *testing.T) {
	c := &engineCLI{images: map[string]bool{}, pulls: map[string]bool{}}
	e := engine(c, agentv1.Sandbox_SANDBOX_DOCKER)
	e.Pull = false

	_, err := e.Start(t.Context(), "j", spec, "/d", "p")

	if err == nil || !strings.Contains(err.Error(), "postgres 17.11 (postgres@sha256:1711)") {
		t.Fatalf("err = %v", err)
	}
	if _, err := e.Start(t.Context(), "j", sdk.SandboxSpec{Image: "mysql"}, "/d", "p"); err == nil || !strings.Contains(err.Error(), "mysql (the newest)") {
		t.Fatalf("err = %v", err)
	}
	if slices.ContainsFunc(c.calls, func(l string) bool { return strings.HasPrefix(l, "docker pull") }) {
		t.Fatalf("pulled with verify.pull off: %q", c.calls)
	}
}

func TestARunThatFailsLeavesNoContainer(t *testing.T) {
	c := &engineCLI{images: map[string]bool{"postgres@sha256:1711": true}, runErr: errors.New("exit status 125")}

	if _, err := engine(c, agentv1.Sandbox_SANDBOX_DOCKER).Start(t.Context(), "j", spec, "/d", "p"); err == nil {
		t.Fatal("a failed run passed")
	}
	if !slices.Contains(c.calls, "docker rm -f -v sard-verify-j") {
		t.Fatalf("calls %q", c.calls)
	}
}

func TestAProcessRunsInTheContainerWithItsStreams(t *testing.T) {
	c := &engineCLI{images: map[string]bool{"postgres@sha256:1711": true}, code: 3}
	box, _ := engine(c, agentv1.Sandbox_SANDBOX_DOCKER).Start(t.Context(), "j", spec, "/d", "p")
	var out bytes.Buffer
	var lines []string

	code, err := box.Run(t.Context(), sdk.Process{
		Path: "/usr/lib/postgresql/17/bin/psql", Args: []string{"-X"}, Env: []string{"PGPASSWORD=x", "LC_MESSAGES=C"},
		Stdin: strings.NewReader("in"), Stdout: &out, Stderr: func(l string) { lines = append(lines, l) },
	})

	exec := c.streams[1]
	want := []string{"exec", "-i", "-e", "PGPASSWORD", "-e", "LC_MESSAGES", "sard-verify-j", "/usr/lib/postgresql/17/bin/psql", "-X"}
	if err != nil || code != 3 || !slices.Equal(exec.Args, want) || !slices.Equal(exec.Env, []string{"PGPASSWORD=x", "LC_MESSAGES=C"}) {
		t.Fatalf("code %d, err %v, exec %q env %q", code, err, exec.Args, exec.Env)
	}
	if out.String() != "outin" || !slices.Equal(lines, []string{"a line"}) {
		t.Fatalf("out %q, lines %q", out.String(), lines)
	}
}

func TestAContainerIsRemovedWithItsVolumes(t *testing.T) {
	c := &engineCLI{images: map[string]bool{"postgres@sha256:1711": true}}
	box, _ := engine(c, agentv1.Sandbox_SANDBOX_DOCKER).Start(t.Context(), "j", spec, "/d", "p")

	if err := box.Remove(t.Context()); err != nil || c.calls[len(c.calls)-1] != "docker rm -f -v sard-verify-j" {
		t.Fatalf("err %v, calls %q", err, c.calls)
	}
}

// V5, check 7: at start, every container of a verification is removed.
func TestASweepRemovesTheContainersOfEveryJob(t *testing.T) {
	c := &engineCLI{ps: "aa11\nbb22\n"}

	removed, err := engine(c, agentv1.Sandbox_SANDBOX_DOCKER).Sweep(t.Context())

	want := []string{"docker ps -aq --filter label=sard.verify.job", "docker rm -f -v aa11", "docker rm -f -v bb22"}
	if err != nil || !slices.Equal(removed, []string{"aa11", "bb22"}) || !slices.Equal(c.calls, want) {
		t.Fatalf("removed %q, err %v, calls %q", removed, err, c.calls)
	}
	if removed, err := engine(c, agentv1.Sandbox_SANDBOX_NONE).Sweep(t.Context()); err != nil || removed != nil {
		t.Fatalf("without a runtime: %v, %v", removed, err)
	}
}

func TestASweepThatCannotListOrRemoveSaysSo(t *testing.T) {
	c := &engineCLI{psErr: errors.New("permission denied")}
	if removed, err := engine(c, agentv1.Sandbox_SANDBOX_DOCKER).Sweep(t.Context()); err == nil || removed != nil {
		t.Fatalf("a failed listing: %q, %v", removed, err)
	}

	c = &engineCLI{ps: "aa11\nbb22\n", rmErr: map[string]error{"bb22": errors.New("device busy")}}
	removed, err := engine(c, agentv1.Sandbox_SANDBOX_DOCKER).Sweep(t.Context())

	if err == nil || !strings.Contains(err.Error(), "bb22") || !slices.Equal(removed, []string{"aa11"}) {
		t.Fatalf("removed %q, err %v", removed, err)
	}
}

// A runtime that refuses the container exits non-zero without an error of
// the CLI: no container, and the reason of the runtime in the failure.
func TestARunThatExitsNonZeroStartsNoSandboxAndSaysWhy(t *testing.T) {
	c := &engineCLI{images: map[string]bool{"postgres@sha256:1711": true}, runCode: 125}

	box, err := engine(c, agentv1.Sandbox_SANDBOX_DOCKER).Start(t.Context(), "j", spec, "/d", "p")

	if box != nil || err == nil || !strings.Contains(err.Error(), "exited 125") || !strings.Contains(err.Error(), "invalid value for --cpus") {
		t.Fatalf("box %v, err %v", box, err)
	}
	if !slices.Contains(c.calls, "docker rm -f -v sard-verify-j") {
		t.Fatalf("calls %q", c.calls)
	}
}

// A container that stopped runs nothing: the runtime's refusal is no exit
// code of the program but an error, for Docker and for Podman.
func TestAProgramOfAStoppedContainerDidNotRun(t *testing.T) {
	for _, said := range []string{
		"Error response from daemon: container 0f is not running",
		"Error: can only create exec sessions on running containers: container state improper",
	} {
		c := &engineCLI{images: map[string]bool{"postgres@sha256:1711": true}}
		box, _ := engine(c, agentv1.Sandbox_SANDBOX_DOCKER).Start(t.Context(), "j", spec, "/d", "p")
		c.gone = said
		var lines []string

		code, err := box.Run(t.Context(), sdk.Process{Path: "psql", Stderr: func(l string) { lines = append(lines, l) }})

		if code != -1 || !errors.Is(err, sandbox.ErrStopped) || !strings.Contains(err.Error(), said) {
			t.Errorf("%q: code %d, err %v", said, code, err)
		}
		if len(lines) != 0 {
			t.Errorf("%q: the runtime's refusal reached the program's stderr: %q", said, lines)
		}
	}
}
