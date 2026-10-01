// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

// Package secrets checks that the agent's secret files (В20,
// docs/specs/agent/agent-enroll.feature, rule "@a1") are not readable by
// anyone but the user running the agent, before the agent connects to
// anything.
package secrets

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"syscall"

	"github.com/Artur-Abalov/sard/agent/internal/config"
)

// groupOtherBits: any of these set means the file is open beyond its owner.
const groupOtherBits = 0o077

// Info is what Stat reports about a secret file.
type Info struct {
	Mode fs.FileMode
	UID  uint32
	Size int64
}

// StatFunc looks up Info for path; injectable so tests do not depend on
// the uid running them. RealStat is the production implementation.
type StatFunc func(path string) (Info, error)

// RealStat looks up a file's mode and owning uid on this host.
func RealStat(path string) (Info, error) {
	info, err := os.Stat(path)
	if err != nil {
		return Info{}, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return Info{}, fmt.Errorf("secrets: %s: cannot determine the file owner on this platform", path)
	}
	return Info{Mode: info.Mode(), UID: stat.Uid, Size: info.Size()}, nil
}

// Error is CheckAll's typed refusal: it names the offending config key,
// the file's path, and either its current mode (Mode set) or its actual
// owner (Owner/WantOwner set).
type Error struct {
	Key        string
	Path       string
	Mode       fs.FileMode // the file's current mode, when that is the violation
	Owner      uint32      // the file's actual owner uid, when that is the violation
	WantOwner  uint32
	modeIsSet  bool
	ownerIsSet bool
	err        error
}

func (e *Error) Error() string {
	switch {
	case e.modeIsSet:
		return fmt.Sprintf("secret file %s (%s) has mode %v: it must not be readable or writable by group or others (owner bits only)", e.Key, e.Path, e.Mode)
	case e.ownerIsSet:
		return fmt.Sprintf("secret file %s (%s) is owned by uid %d, not the agent's uid %d", e.Key, e.Path, e.Owner, e.WantOwner)
	default:
		return fmt.Sprintf("secret file %s (%s): %v", e.Key, e.Path, e.err)
	}
}

func (e *Error) Unwrap() error { return e.err }

// entry is one secret file named by a config key.
type entry struct {
	key  string
	path string
}

// secretEntries lists every secret file config.Config names (В20):
// tls.key_file, each repository's password_file and (if set) env_file,
// every secrets.<name> and scripts.<name>. tls.cert_file and tls.ca_file
// are not secret and are not listed.
func secretEntries(cfg config.Config) []entry {
	var entries []entry
	if cfg.TLS.KeyFile != "" {
		entries = append(entries, entry{"tls.key_file", cfg.TLS.KeyFile})
	}
	for i, r := range cfg.Repositories {
		entries = append(entries, entry{fmt.Sprintf("repositories[%d].password_file", i), r.PasswordFile})
		if r.EnvFile != "" {
			entries = append(entries, entry{fmt.Sprintf("repositories[%d].env_file", i), r.EnvFile})
		}
	}
	for _, name := range cfg.SecretNames() {
		entries = append(entries, entry{"secrets." + name, cfg.Secrets[name]})
	}
	for _, name := range cfg.ScriptNames() {
		entries = append(entries, entry{"scripts." + name, cfg.Scripts[name]})
	}
	return entries
}

// CheckAll verifies every secret file in cfg has no group or other
// permission bits and is owned by agentUID. It returns the first
// violation found, naming the config key, path, current mode or owner,
// and the requirement (В20).
func CheckAll(cfg config.Config, agentUID uint32, stat StatFunc) error {
	for _, e := range secretEntries(cfg) {
		if err := CheckFile(e.key, e.path, agentUID, stat); err != nil {
			return err
		}
	}
	return nil
}

// CheckFile is CheckAll for one file, key naming it in the message. A
// missing file passes: whatever uses it reports that.
func CheckFile(key, path string, agentUID uint32, stat StatFunc) error {
	info, err := stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	switch {
	case err != nil:
		return &Error{Key: key, Path: path, err: err}
	case info.Mode.Perm()&groupOtherBits != 0:
		return &Error{Key: key, Path: path, Mode: info.Mode.Perm(), modeIsSet: true}
	case info.UID != agentUID:
		return &Error{Key: key, Path: path, Owner: info.UID, WantOwner: agentUID, ownerIsSet: true}
	}
	return nil
}
