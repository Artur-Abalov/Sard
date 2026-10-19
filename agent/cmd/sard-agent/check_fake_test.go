// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/restic"
)

// The fake restic of the A8d scenarios: locks, unlock and check.

// fakeLock is a lock in the repository, "L(хост, возраст, pid)" of the
// conventions of host-check.feature.
type fakeLock struct {
	id        string
	host      string
	at        time.Time
	pid       int
	exclusive bool
}

// fakeChecks is how a repository answers list locks, cat lock, unlock and check.
type fakeChecks struct {
	locks []fakeLock
	// gone are the ids that cat lock does not find any more.
	gone map[string]bool
	// onList, if not nil, runs at every list locks, with its number from 1:
	// a lock that appears between two reads.
	onList func(n int)
	lists  int
	// hangList: list locks never finishes.
	hangList bool
	// unlockCode and unlockFatal: how unlock ends.
	unlockCode  int
	unlockFatal string
	// checkCode and checkStderr: how check ends and what it prints.
	checkCode   int
	checkStderr []string
}

func (f *fakeRestic) list(ctx context.Context, cmd restic.Command, r *fakeRepo) (int, error) {
	cs := &r.checkState
	f.mu.Lock()
	cs.lists++
	n := cs.lists
	f.mu.Unlock()
	if cs.onList != nil {
		cs.onList(n)
	}
	if cs.hangList {
		return f.hang(ctx, cmd, r, r.hangLine)
	}
	for _, l := range cs.locks {
		cmd.Stdout([]byte(l.id))
	}
	return 0, nil
}

func (f *fakeRestic) catLock(cmd restic.Command, r *fakeRepo) (int, error) {
	id := cmd.Args[len(cmd.Args)-1]
	if r.checkState.gone[id] {
		cmd.Stderr([]byte("Fatal: unable to load lock " + id + ": not found"))
		return 1, nil
	}
	for _, l := range r.checkState.locks {
		if l.id == id {
			out, _ := json.Marshal(map[string]any{"time": l.at.Format(time.RFC3339Nano), "exclusive": l.exclusive, "hostname": l.host, "username": "root", "pid": l.pid, "uid": 0, "gid": 0})
			cmd.Stdout(out)
			return 0, nil
		}
	}
	cmd.Stderr([]byte("Fatal: unable to load lock " + id + ": not found"))
	return 1, nil
}

func (f *fakeRestic) unlock(_ context.Context, cmd restic.Command, r *fakeRepo) (int, error) {
	if r.checkState.unlockFatal != "" {
		cmd.Stderr([]byte(r.checkState.unlockFatal))
	}
	return r.checkState.unlockCode, nil
}

func (f *fakeRestic) check(_ context.Context, cmd restic.Command, r *fakeRepo) (int, error) {
	for _, line := range r.checkState.checkStderr {
		cmd.Stderr([]byte(line))
	}
	return r.checkState.checkCode, nil
}

// addLock adds L(host, age, pid) to the repository; its id is L1, L2, ... .
func (r *fakeRepo) addLock(now time.Time, host string, age time.Duration, pid int) {
	id := fmt.Sprintf("L%d", len(r.checkState.locks)+1)
	r.checkState.locks = append(r.checkState.locks, fakeLock{id: id, host: host, at: now.Add(-age), pid: pid})
}
