// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/app"
	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/pluginhost"
	"github.com/Artur-Abalov/sard/agent/internal/role"
	"github.com/Artur-Abalov/sard/agent/internal/sandbox"
	"github.com/Artur-Abalov/sard/agent/internal/verifier"
)

// statusRefresh is how often a verifier asks its container runtime again.
const statusRefresh = 10 * time.Minute

// verifying is the restore verification of a verifier's host (V5); nil on a source.
type verifying struct {
	verifier *verifier.Verifier
	status   *verifier.Status
}

// newVerifying builds the verifier of a verifier's host from the verify
// section and removes what verifications the previous run did not finish
// left on the work volume, before any step.
// A list of images of the config that is not one stops the agent.
func newVerifying(cfg config.Config, me hostIdentity, disk verifier.Disk, log *slog.Logger) (*verifying, error) {
	if me.role != role.Verifier {
		return nil, nil
	}
	s := cfg.VerifySettings()
	images, err := sandboxImages(s.Images)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(s.WorkDir, 0o700); err != nil {
		log.Warn("cannot make the work directory of verifications", "dir", s.WorkDir, "reason", err.Error())
	}
	status := &verifier.Status{WorkDir: s.WorkDir, Disk: disk, CLI: sandbox.ExecCLI{}, Images: images, Clock: verifier.SystemClock{}, Refresh: statusRefresh}
	engine := &sandbox.Engine{
		CLI: sandbox.ExecCLI{}, Images: images, Pull: s.Pull, User: fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()),
		Limits:  sandbox.Limits{CPUs: s.Sandbox.CPUs, Memory: s.Sandbox.Memory, Pids: s.Sandbox.Pids},
		Runtime: status.Runtime,
	}
	v := verifier.New(verifier.Options{
		WorkDir: s.WorkDir, MachineID: me.machineID, CheckPercent: s.CheckPercent,
		Limits: verifier.Limits(s.Limits), Space: verifier.Space{Factor: s.SpaceFactor, Headroom: s.SpaceHeadroom},
		Clock: verifier.SystemClock{}, Disk: disk, Sandboxes: verifier.EngineSandboxes(engine),
	})
	sweep(v, log)
	return &verifying{verifier: v, status: status}, nil
}

// sandboxImages are the images of verify.images, each checked, or the list
// of the release when the config names none (owner's decision 8).
func sandboxImages(configured []config.VerifyImage) ([]sandbox.Image, error) {
	if len(configured) == 0 {
		return sandbox.ReleaseImages(), nil
	}
	images := make([]sandbox.Image, len(configured))
	for i, c := range configured {
		images[i] = sandbox.Image{Name: c.Name, Version: c.Version, Digest: c.Digest}
		if err := images[i].Valid(); err != nil {
			return nil, fmt.Errorf("%w: verify.images[%d]: %w", config.ErrInvalidVerify, i, err)
		}
	}
	return images, nil
}

// sweep removes the leftovers of verifications that did not end and says which.
func sweep(v *verifier.Verifier, log *slog.Logger) {
	removed, err := v.Sweep(context.Background())
	if len(removed) > 0 {
		log.Warn("removed what verifications that did not end left", "dirs", removed)
	}
	if err != nil {
		log.Error("cannot remove what a verification left; remove it by hand", "reason", err.Error())
	}
}

// readsOnly: a verifier reads its repositories without lock and cache (V5).
func (v *verifying) readsOnly() bool { return v != nil }

// options are the options of the handlers of a verifier: it runs VERIFY steps.
func (v *verifying) options() []pluginhost.Option {
	if v == nil {
		return nil
	}
	return []pluginhost.Option{pluginhost.WithVerifier(v.verifier)}
}

// attach makes Register report what the verifier measures, and a heartbeat
// when that changed.
func (v *verifying) attach(agent *app.Agent) {
	if v == nil {
		return
	}
	agent.Verifier = v.status.Current
	agent.VerifierChanged = v.status.Changed
}

// writableCheckTimeout bounds the question of the lock to one repository.
const writableCheckTimeout = 2 * time.Minute

// warnIfWritable logs a WARN for every repository whose storage credentials
// can write (V5, K2): a verifier needs none that do. It asks the storage for a
// lock, which only such credentials get and restic removes at once. The start
// runs it in the background.
func (v *verifying) warnIfWritable(ctx context.Context, repos map[string]locker, log *slog.Logger) {
	if v == nil {
		return
	}
	for name, repo := range repos {
		warnIfCanLock(ctx, name, repo, log)
	}
}

func warnIfCanLock(ctx context.Context, name string, repo locker, log *slog.Logger) {
	limited, cancel := context.WithTimeout(ctx, writableCheckTimeout)
	defer cancel()
	if repo.CheckLock(limited) == nil {
		log.Warn("these storage credentials can write: a verifier needs none that do; give it credentials that only read", "repository", name)
	}
}

// locker asks a repository for its lock.
type locker interface {
	CheckLock(ctx context.Context) error
}

// lockers are the repositories as lockers, opened apart from the steps' own:
// with a lock, which the steps of a verifier never take.
func lockers(repos resticRepositories) map[string]locker {
	out := make(map[string]locker, len(repos))
	for name, repo := range repos {
		out[name] = repo
	}
	return out
}
