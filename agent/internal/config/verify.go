// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package config

import (
	"errors"
	"fmt"
	"path/filepath"
	"time"
)

// Defaults of a verifier (V5, decisions of the owner at checkpoint 1).
const (
	// DefaultVerifyWorkDir lies inside the StateDirectory of the packaged
	// unit, which mounts it noexec (NoExecPaths=).
	DefaultVerifyWorkDir              = "/var/lib/sard-agent/verify"
	DefaultVerifyCheckPercent         = 10
	DefaultVerifySpaceFactor          = 2.0
	DefaultVerifySpaceHeadroom uint64 = 1 << 30
)

// DefaultVerifyLimits are the stage limits without a setting.
var DefaultVerifyLimits = VerifyLimits{Prepare: 10 * time.Minute, Check: 2 * time.Hour, Restore: 4 * time.Hour, Fingerprint: time.Hour}

// ErrInvalidVerify is returned for a verify setting the agent cannot use.
var ErrInvalidVerify = errors.New("invalid verify setting")

// Verify tunes restore verification on a verifier (V5); every field is
// optional and only a verifier reads them.
type Verify struct {
	// WorkDir holds the copies, one directory per job; it must be mounted noexec.
	WorkDir string `yaml:"work_dir"`
	// CheckPercent is the share of the data restic check reads, 1..100.
	CheckPercent int `yaml:"check_percent"`
	// SpaceFactor and SpaceHeadroom are the room a copy needs: its size
	// times the factor (at least 1) plus the headroom in bytes.
	SpaceFactor   float64 `yaml:"space_factor"`
	SpaceHeadroom *uint64 `yaml:"space_headroom"`
	// StageLimits are the longest each kind of stage may take, e.g. "4h".
	StageLimits VerifyLimits `yaml:"stage_limits"`
	// Images replace the sandbox images of the release: an own registry or
	// mirror, images loaded beforehand in a closed network.
	Images []VerifyImage `yaml:"images"`
	// Pull lets a missing image be pulled by its digest; true by default.
	Pull *bool `yaml:"pull"`
	// Sandbox bounds the container of a check.
	Sandbox SandboxLimits `yaml:"sandbox"`
}

// VerifyImage is an image of verify.images.
type VerifyImage struct {
	Name    string `yaml:"name"`
	Version string `yaml:"version"`
	Digest  string `yaml:"digest"`
}

// SandboxLimits bound the container of a check: CPUs and memory as Docker
// and Podman take them ("1.5", "4g"), the number of processes.
type SandboxLimits struct {
	CPUs   string `yaml:"cpus"`
	Memory string `yaml:"memory"`
	Pids   int    `yaml:"pids"`
}

// DefaultSandboxLimits are the sandbox limits without a setting.
var DefaultSandboxLimits = SandboxLimits{CPUs: "2", Memory: "4g", Pids: 1024}

// VerifyLimits are the limits of the stages: preparation and cleanup,
// restic check, restore and start, fingerprint of the copy.
type VerifyLimits struct {
	Prepare     time.Duration `yaml:"prepare"`
	Check       time.Duration `yaml:"check"`
	Restore     time.Duration `yaml:"restore"`
	Fingerprint time.Duration `yaml:"fingerprint"`
}

// VerifySettings is the verify section with the defaults filled in.
type VerifySettings struct {
	WorkDir       string
	CheckPercent  int
	SpaceFactor   float64
	SpaceHeadroom uint64
	Limits        VerifyLimits
	// Images are those of the config; nil: the list of the release.
	Images  []VerifyImage
	Pull    bool
	Sandbox SandboxLimits
}

// VerifySettings returns the settings of a verifier.
func (c Config) VerifySettings() VerifySettings {
	v := c.Verify
	s := VerifySettings{
		WorkDir: or(v.WorkDir, DefaultVerifyWorkDir), CheckPercent: or(v.CheckPercent, DefaultVerifyCheckPercent),
		SpaceFactor: or(v.SpaceFactor, DefaultVerifySpaceFactor), SpaceHeadroom: DefaultVerifySpaceHeadroom,
		Limits: VerifyLimits{
			Prepare: or(v.StageLimits.Prepare, DefaultVerifyLimits.Prepare), Check: or(v.StageLimits.Check, DefaultVerifyLimits.Check),
			Restore: or(v.StageLimits.Restore, DefaultVerifyLimits.Restore), Fingerprint: or(v.StageLimits.Fingerprint, DefaultVerifyLimits.Fingerprint),
		},
	}
	if v.SpaceHeadroom != nil {
		s.SpaceHeadroom = *v.SpaceHeadroom
	}
	s.Images, s.Pull = v.Images, v.Pull == nil || *v.Pull
	s.Sandbox = SandboxLimits{
		CPUs: or(v.Sandbox.CPUs, DefaultSandboxLimits.CPUs), Memory: or(v.Sandbox.Memory, DefaultSandboxLimits.Memory),
		Pids: or(v.Sandbox.Pids, DefaultSandboxLimits.Pids),
	}
	return s
}

// or is value, or the default when value is zero.
func or[T comparable](value, def T) T {
	var zero T
	if value == zero {
		return def
	}
	return value
}

// validateVerify: an absolute clean work_dir apart from the restore directory
// and what restore.dir is kept apart from; a share of the data; a factor of at
// least one; no negative limit. A zero setting is the default.
func (c Config) validateVerify() error {
	if err := c.validateWorkDir(c.VerifySettings().WorkDir); err != nil {
		return err
	}
	if err := c.Verify.validate(); err != nil {
		return err
	}
	return c.Verify.validateSandbox()
}

// validate judges the settings as written; zero is the default.
func (v Verify) validate() error {
	l := v.StageLimits
	switch {
	case v.CheckPercent < 0 || v.CheckPercent > 100:
		return fmt.Errorf("%w: verify.check_percent: want 1..100, got %d", ErrInvalidVerify, v.CheckPercent)
	case v.SpaceFactor != 0 && v.SpaceFactor < 1:
		return fmt.Errorf("%w: verify.space_factor: want at least 1, got %v", ErrInvalidVerify, v.SpaceFactor)
	case min(l.Prepare, l.Check, l.Restore, l.Fingerprint) < 0:
		return fmt.Errorf("%w: verify.stage_limits: a limit is negative", ErrInvalidVerify)
	}
	return nil
}

// validateSandbox: every image of the config names itself, no negative
// limit. The agent checks the version and the digest when it builds the list.
func (v Verify) validateSandbox() error {
	for i, img := range v.Images {
		if img.Name == "" || img.Version == "" || img.Digest == "" {
			return fmt.Errorf("%w: verify.images[%d]: want name, version and digest", ErrInvalidVerify, i)
		}
	}
	if v.Sandbox.Pids < 0 {
		return fmt.Errorf("%w: verify.sandbox.pids: want a positive number, got %d", ErrInvalidVerify, v.Sandbox.Pids)
	}
	return nil
}

func (c Config) validateWorkDir(dir string) error {
	if !filepath.IsAbs(dir) || filepath.Clean(dir) != dir || dir == "/" {
		return fmt.Errorf("%w: verify.work_dir: want an absolute path without . or .. components and not /, got %q", ErrInvalidVerify, dir)
	}
	others := append(c.avoidedByRestore(), avoided{c.RestoreDir(), "restore.dir"})
	for _, other := range others {
		if overlaps(dir, other.path) {
			return fmt.Errorf("%w: verify.work_dir %s overlaps %s, %s; use a directory of its own", ErrInvalidVerify, dir, other.path, other.what)
		}
	}
	return nil
}
