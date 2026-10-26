// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package verifier

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"path"

	"github.com/Artur-Abalov/sard/agent/internal/sandbox"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

// Sandboxes start the sandbox of a job and remove those left over.
type Sandboxes interface {
	Start(ctx context.Context, job string, spec sdk.SandboxSpec, dataDir, password string) (Sandbox, error)
	// Sweep removes the sandboxes of every job; it runs at start.
	Sweep(ctx context.Context) ([]string, error)
}

// Sandbox is a running sandbox.
type Sandbox interface {
	Image() sandbox.Image
	Run(ctx context.Context, p sdk.Process) (int, error)
	Remove(ctx context.Context) error
}

// EngineSandboxes are the sandboxes of a sandbox.Engine.
func EngineSandboxes(e *sandbox.Engine) Sandboxes { return engineSandboxes{e} }

type engineSandboxes struct{ e *sandbox.Engine }

func (s engineSandboxes) Start(ctx context.Context, job string, spec sdk.SandboxSpec, dataDir, password string) (Sandbox, error) {
	c, err := s.e.Start(ctx, job, spec, dataDir, password)
	if err != nil {
		return nil, err
	}
	return c, nil
}

func (s engineSandboxes) Sweep(ctx context.Context) ([]string, error) { return s.e.Sweep(ctx) }

// sandboxed is the plugin as a Sandboxed one; false for one whose copy needs
// no sandbox (files).
func (x *run) sandboxed() (sdk.Sandboxed, bool) {
	sb, ok := x.plugin.(sdk.Sandboxed)
	return sb, ok
}

// startSandbox: the container of the copy is up, from an image of the list,
// and its application can hold the copy (level 0: the environment).
func (x *run) startSandbox(ctx context.Context) outcome {
	sb, _ := x.sandboxed()
	spec, err := sb.Sandbox(x.reference)
	if err != nil {
		return cannotRun("the plugin names no sandbox for this copy: %v", err)
	}
	if x.opts.Sandboxes == nil {
		return cannotRun("this verifier has no sandbox runtime configured")
	}
	data := path.Join(x.dir, "data")
	if err := x.opts.Disk.MakeDir(data); err != nil {
		return cannotRun("cannot make %s: %v", data, err)
	}
	x.password = newPassword()
	box, err := x.opts.Sandboxes.Start(ctx, x.step.GetVerify().GetJobId(), spec, data, x.password)
	if err != nil {
		return cannotRun("the sandbox did not start: %v", err)
	}
	x.box = box
	img := box.Image()
	o := x.ready(ctx, sb)
	o.digest = img.Digest
	return o
}

// ready waits for the application of the sandbox and its judgement of the copy.
func (x *run) ready(ctx context.Context, sb sdk.Sandboxed) outcome {
	img := x.box.Image()
	err := sb.SandboxReady(ctx, x.sandboxHost(), x.reference)
	var refusal *sdk.SandboxRefusal
	switch {
	case errors.As(err, &refusal):
		return cannotRun("the sandbox %s %s cannot hold the copy: %s", img.Name, img.Version, refusal.Reason)
	case err != nil:
		return cannotRun("the sandbox %s %s did not come up: %v", img.Name, img.Version, err)
	}
	return passed("the sandbox %s %s (%s) serves, without network", img.Name, img.Version, img.Digest)
}

// newPassword is a random password of the sandbox's application, for one check.
func newPassword() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b) // never fails (crypto/rand)
	return hex.EncodeToString(b)
}

// openCopy: the restored copy opens in the application of the sandbox (level 3).
func (x *run) openCopy(ctx context.Context, sb sdk.Sandboxed) outcome {
	if err := sb.OpenCopy(ctx, x.sandboxHost(), x.copied); err != nil {
		return failed("the restored copy does not open in %s %s: %v", x.box.Image().Name, x.box.Image().Version, err)
	}
	return passed("the copy opens in %s %s", x.box.Image().Name, x.box.Image().Version)
}

// removeSandbox removes the container of the job; "" when there is none or it went.
func (x *run) removeSandbox(ctx context.Context) string {
	if x.box == nil {
		return ""
	}
	if err := x.box.Remove(ctx); err != nil {
		return fmt.Sprintf("cannot remove the sandbox, remove it by hand (label %s=%s): %v", sandbox.JobLabel, x.step.GetVerify().GetJobId(), err)
	}
	return ""
}

// sandboxHost is the host of a plugin whose copy runs in the sandbox.
type sandboxHost struct {
	copyHost
}

var _ sdk.SandboxHost = sandboxHost{}

func (x *run) sandboxHost() sandboxHost { return sandboxHost{copyHost{x}} }

// Secret gives the password of the sandbox, and nothing else.
func (h sandboxHost) Secret(name string) ([]byte, error) {
	if name == sdk.SandboxPassword {
		return []byte(h.x.password), nil
	}
	return nil, errNoSecret
}

// Run runs a program in the sandbox.
func (h sandboxHost) Run(ctx context.Context, p sdk.Process) (int, error) { return h.x.box.Run(ctx, p) }
