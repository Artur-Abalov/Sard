// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package verifier_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/sandbox"
	"github.com/Artur-Abalov/sard/agent/internal/verifier"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
	"google.golang.org/protobuf/proto"
)

type runtimeCLI struct {
	version string
	images  map[string]bool
	calls   int
}

func (c *runtimeCLI) Output(_ context.Context, name string, args ...string) ([]byte, error) {
	c.calls++
	if args[0] == "version" && name == "docker" && c.version != "" {
		return []byte(c.version), nil
	}
	if args[0] == "image" && c.images[args[len(args)-1]] {
		return []byte("sha256:0f"), nil
	}
	return nil, errors.New("exit status 125")
}

func newStatus(disk *fakeDisk, cli *runtimeCLI, clock *fakeClock) *verifier.Status {
	return &verifier.Status{
		WorkDir: workDir, Disk: disk, CLI: cli, Clock: clock, Refresh: 10 * time.Minute,
		Images: []sandbox.Image{{Name: "postgres", Version: "17.6", Digest: "sha256:aa"}, {Name: "postgres", Version: "18.0", Digest: "sha256:bb"}},
	}
}

func TestTheStatusOfAVerifierIsItsRoomItsSandboxAndItsImages(t *testing.T) {
	disk := &fakeDisk{volume: verifier.Volume{Free: 5 << 30, NoExec: true}}
	cli := &runtimeCLI{version: "29.9.2\n", images: map[string]bool{"postgres@sha256:aa": true}}

	got := newStatus(disk, cli, &fakeClock{now: t0}).Current(t.Context())

	free := uint64(5 << 30)
	want := &agentv1.VerifierStatus{
		WorkFreeBytes: &free, Sandbox: agentv1.Sandbox_SANDBOX_DOCKER, SandboxVersion: "29.9.2",
		Images: []*agentv1.SandboxImage{{Name: "postgres", Version: "17.6", Digest: "sha256:aa"}},
	}
	if !proto.Equal(got, want) {
		t.Fatalf("status = %v", got)
	}
}

func TestAWorkVolumeThatCannotBeMeasuredHasNoFreeSpace(t *testing.T) {
	disk := &fakeDisk{volumeErr: errors.New("no such file")}

	got := newStatus(disk, &runtimeCLI{}, &fakeClock{now: t0}).Current(t.Context())

	if got.WorkFreeBytes != nil || got.GetSandbox() != agentv1.Sandbox_SANDBOX_NONE || len(got.GetImages()) != 0 {
		t.Fatalf("status = %v", got)
	}
}

func TestAHeartbeatCarriesTheStatusOnlyWhenItChanged(t *testing.T) {
	disk := &fakeDisk{volume: verifier.Volume{Free: 100 << 30}}
	clock := &fakeClock{now: t0}
	cli := &runtimeCLI{version: "29.9.2"}
	s := newStatus(disk, cli, clock)
	_ = s.Current(t.Context())

	if got := s.Changed(t.Context()); got != nil {
		t.Fatalf("unchanged: %v", got)
	}
	disk.volume.Free -= 1 << 20 // less than 1%: the same for the server
	if got := s.Changed(t.Context()); got != nil {
		t.Fatalf("a small change: %v", got)
	}
	disk.volume.Free = 50 << 30
	if got := s.Changed(t.Context()); got.GetWorkFreeBytes() != 50<<30 {
		t.Fatalf("half the room: %v", got)
	}
	if got := s.Changed(t.Context()); got != nil {
		t.Fatalf("sent once: %v", got)
	}
}

// The runtime and its images are asked at most once per refresh interval:
// a CLI per heartbeat would be a process every 30 seconds.
func TestTheSandboxIsAskedAgainOnlyAfterTheRefreshInterval(t *testing.T) {
	disk := &fakeDisk{volume: verifier.Volume{Free: 1 << 30}}
	clock := &fakeClock{now: t0}
	cli := &runtimeCLI{}
	s := newStatus(disk, cli, clock)
	_ = s.Current(t.Context())
	asked := cli.calls

	cli.version = "29.9.2"
	clock.Advance(10*time.Minute - time.Second)
	if got := s.Changed(t.Context()); got != nil || cli.calls != asked {
		t.Fatalf("asked before the interval: %v, %d calls", got, cli.calls-asked)
	}
	clock.Advance(time.Second)
	got := s.Changed(t.Context())
	if got.GetSandbox() != agentv1.Sandbox_SANDBOX_DOCKER || !strings.HasPrefix(got.GetSandboxVersion(), "29.9") {
		t.Fatalf("after the interval: %v", got)
	}
}

func TestTheRuntimeOfTheSandboxesIsTheOneTheStatusFound(t *testing.T) {
	cli := &runtimeCLI{version: "29.9.2"}
	s := newStatus(&fakeDisk{}, cli, &fakeClock{now: t0})

	if rt := s.Runtime(t.Context()); rt.Binary != "docker" || rt.Version != "29.9.2" {
		t.Fatalf("runtime %+v", rt)
	}
	asked := cli.calls
	if _ = s.Runtime(t.Context()); cli.calls != asked {
		t.Fatalf("asked again within the refresh: %d calls", cli.calls-asked)
	}
}
