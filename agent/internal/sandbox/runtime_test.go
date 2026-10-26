// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package sandbox_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/sandbox"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

// cli answers the commands of a container runtime by their arguments.
type cli struct {
	answers map[string]string // "docker version ..." -> output
	calls   []string
}

func (c *cli) Output(_ context.Context, name string, args ...string) ([]byte, error) {
	line := strings.Join(append([]string{name}, args...), " ")
	c.calls = append(c.calls, line)
	out, ok := c.answers[line]
	if !ok {
		return nil, errors.New("exit status 125")
	}
	return []byte(out), nil
}

const (
	podmanVersion = "podman version --format {{.Client.Version}}"
	dockerVersion = "docker version --format {{.Server.Version}}"
)

// Owner's decision at checkpoint 1: rootless Podman is recommended, so it is
// asked first; Docker counts only when its daemon answers the service user.
func TestPodmanIsTheSandboxWhenItAnswers(t *testing.T) {
	c := &cli{answers: map[string]string{podmanVersion: "5.4.0\n", dockerVersion: "29.9.2\n"}}

	rt := sandbox.Detect(t.Context(), c)

	if rt != (sandbox.Runtime{Kind: agentv1.Sandbox_SANDBOX_PODMAN, Binary: "podman", Version: "5.4.0"}) {
		t.Fatalf("runtime = %+v", rt)
	}
}

func TestDockerIsTheSandboxWhenPodmanDoesNotAnswer(t *testing.T) {
	c := &cli{answers: map[string]string{dockerVersion: "29.9.2\n"}}

	rt := sandbox.Detect(t.Context(), c)

	if rt != (sandbox.Runtime{Kind: agentv1.Sandbox_SANDBOX_DOCKER, Binary: "docker", Version: "29.9.2"}) {
		t.Fatalf("runtime = %+v", rt)
	}
	if !slices.Equal(c.calls, []string{podmanVersion, dockerVersion}) {
		t.Errorf("calls %q", c.calls)
	}
}

// A docker CLI whose daemon refuses the service user (not in the group
// docker) prints no server version: there is no sandbox.
func TestNoAnsweringRuntimeIsNoSandbox(t *testing.T) {
	for _, answers := range []map[string]string{{}, {dockerVersion: "\n"}} {
		if rt := sandbox.Detect(t.Context(), &cli{answers: answers}); rt != (sandbox.Runtime{Kind: agentv1.Sandbox_SANDBOX_NONE}) {
			t.Errorf("%v: runtime = %+v", answers, rt)
		}
	}
}

func TestTheImagesOfTheListThatAreOnTheHostAreReported(t *testing.T) {
	list := []sandbox.Image{
		{Name: "postgres", Version: "17.6", Digest: "sha256:aa"},
		{Name: "postgres", Version: "18.0", Digest: "sha256:bb"},
	}
	c := &cli{answers: map[string]string{"docker image inspect --format {{.Id}} postgres@sha256:aa": "sha256:0f\n"}}
	rt := sandbox.Runtime{Kind: agentv1.Sandbox_SANDBOX_DOCKER, Binary: "docker"}

	got := rt.Present(t.Context(), c, list)

	if !slices.Equal(got, list[:1]) {
		t.Fatalf("present = %+v", got)
	}
	if none := (sandbox.Runtime{Kind: agentv1.Sandbox_SANDBOX_NONE}).Present(t.Context(), c, list); none != nil {
		t.Fatalf("without a runtime: %+v", none)
	}
}

// yes is a container CLI that answers every command.
type yes struct{}

func (yes) Output(context.Context, string, ...string) ([]byte, error) { return []byte("ok"), nil }

func TestWithoutARuntimeNoImageIsAskedFor(t *testing.T) {
	list := []sandbox.Image{{Name: "postgres", Version: "17.6", Digest: "sha256:aa"}}
	if got := (sandbox.Runtime{Kind: agentv1.Sandbox_SANDBOX_NONE}).Present(t.Context(), yes{}, list); got != nil {
		t.Fatalf("present = %v", got)
	}
}
