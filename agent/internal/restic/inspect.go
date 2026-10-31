// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package restic

import (
	"bytes"
	"context"
	"fmt"
	"strings"
)

// WithReadOnly returns a copy of the CLI whose repository commands get
// --no-lock and --no-cache: they take no lock in the repository and leave
// nothing in the cache directory (Ч23). The CLI itself is not changed.
func (c *CLI) WithReadOnly() *CLI {
	opts := c.opts
	opts.ReadOnly = true
	return &CLI{opts: opts, repo: c.repo}
}

// readOnlyFlags are what a read-only CLI adds to every repository command.
var readOnlyFlags = []string{"--no-lock", "--no-cache"}

// ListLocks runs `restic list locks` and returns the ids of the locks.
func (c *CLI) ListLocks(ctx context.Context) ([]string, error) {
	var out bytes.Buffer
	if _, err := c.WithReadOnly().runRepo(ctx, call{args: []string{"list", "locks"}, stdout: collect(&out)}); err != nil {
		return nil, err
	}
	return strings.Fields(out.String()), nil
}

// CatLock runs `restic cat lock` and returns the JSON of the lock. The id
// goes after "--", so one starting with "-" is not a flag.
func (c *CLI) CatLock(ctx context.Context, id string) ([]byte, error) {
	var out bytes.Buffer
	if _, err := c.WithReadOnly().runRepo(ctx, call{args: []string{"cat", "lock"}, tail: []string{"--", id}, stdout: collect(&out)}); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// Unlock runs `restic unlock`, without --remove-all: restic removes the
// locks it considers stale (older than 30 minutes, or of a process of this
// host that is gone).
func (c *CLI) Unlock(ctx context.Context) error {
	_, err := c.runRepo(ctx, call{args: []string{"unlock"}})
	return err
}

// CheckData runs `restic check --read-data-subset=<percent>%`. It takes a
// lock in the repository, and uses a cache of its own that restic removes.
func (c *CLI) CheckData(ctx context.Context, percent int) error {
	_, err := c.runRepo(ctx, call{args: []string{"check", fmt.Sprintf("--read-data-subset=%d%%", percent)}})
	return err
}

// Forget runs `restic forget` for the snapshots with exactly these ids and
// nothing else: no --keep-* rule, no --group-by and no --prune, so what is
// forgotten is decided by the caller alone (КL16). A repository locked by a
// live process is waited for up to lockWait, then the error is ErrLocked.
func (c *CLI) Forget(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return fmt.Errorf("%w: no snapshots to forget", ErrInvalidRequest)
	}
	_, err := c.runRepo(ctx, call{args: []string{"forget", "--retry-lock", lockWait}, tail: append([]string{"--"}, ids...)})
	return err
}
