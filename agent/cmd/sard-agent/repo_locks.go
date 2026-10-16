// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"context"

	"github.com/Artur-Abalov/sard/agent/internal/hostcheck"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
)

// readLocks reads the locks of the repository: restic list locks, then
// restic cat lock of each (Ч5). A lock that is listed and cannot be read when
// its turn comes (restic removes a lock when its process ends, and a new
// one may still be half written) is counted in gone, not an error. The cli
// must be read-only.
func readLocks(ctx context.Context, cli *restic.CLI) (locks []hostcheck.Lock, gone int, err error) {
	ids, err := cli.ListLocks(ctx)
	if err != nil {
		return nil, 0, err
	}
	for _, id := range ids {
		l, readable := readLock(ctx, cli, id)
		if ctx.Err() != nil {
			return nil, 0, ctx.Err()
		}
		if !readable {
			gone++
			continue
		}
		locks = append(locks, l)
	}
	return locks, gone, nil
}

// readLock is one lock; false when restic cannot show it or what it shows is
// no lock.
func readLock(ctx context.Context, cli *restic.CLI, id string) (hostcheck.Lock, bool) {
	data, err := cli.CatLock(ctx, id)
	if err != nil {
		return hostcheck.Lock{}, false
	}
	l, err := hostcheck.ParseLock(data)
	return l, err == nil
}

// lockEnv is what the locks of a repository are judged against.
func lockEnv(deps hostDeps) (hostcheck.LockEnv, error) {
	host, err := deps.hostname()
	return hostcheck.LockEnv{Host: host, Now: deps.clock.Now(), Alive: deps.pidAlive}, err
}
