// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"context"
	"fmt"
	"io"

	"github.com/Artur-Abalov/sard/agent/internal/hostcheck"
	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
	"github.com/Artur-Abalov/sard/agent/internal/refusal"
	"github.com/Artur-Abalov/sard/agent/internal/repoconnect"
	"github.com/Artur-Abalov/sard/agent/internal/repoinit"
	"github.com/Artur-Abalov/sard/agent/internal/restore"
)

// unlockWarning is said before a lock is removed (Ч6).
const unlockWarning = "warning: removing the lock of a process that is still running damages the work of that process; this command removes only the locks of this host that are older than 24h and whose process is gone\n"

// unlockRun is one run of "sard-agent repo unlock".
type unlockRun struct {
	st     snapshotsState
	opts   hostOptions
	deps   hostDeps
	target repoinit.Target
	log    repoconnect.Log
	env    hostcheck.LockEnv
}

// runRepoUnlock is "sard-agent repo unlock <name> --remove-stale": args
// excludes "unlock". It changes the storage, so only root may run it (Р5).
// The checks come in the order of Р69, as for repo snapshots.
func runRepoUnlock(ctx context.Context, args []string, stdout, stderr io.Writer, deps hostDeps) int {
	opts, code := parseRepoFlags("unlock", args, stderr, deps)
	if code != exitOK {
		return code
	}
	who, f := authorize(deps, "repo unlock", opts, true, true)
	if f != nil {
		return report(stderr, "repo unlock", f)
	}
	ctx, cancel := repoContext(ctx, deps.clock, opts.timeout)
	defer cancel(nil)
	st, code := prepareSnapshots(ctx, opts, who, stderr, deps)
	if code != exitOK {
		return code
	}
	u := &unlockRun{st: st, opts: opts, deps: deps, target: targetOf(st, who, deps)}
	n, f := u.removeStale(ctx, stderr)
	if f != nil {
		return report(stderr, "repo unlock", f)
	}
	u.tell(stdout, stderr, who, n)
	return exitOK
}

// removeStale is Ч6: read the locks, and when there is something to remove,
// read them again right before restic unlock and refuse if it would remove
// what the command may not. It returns how many locks were removed.
func (u *unlockRun) removeStale(ctx context.Context, stderr io.Writer) (int, *refusal.Failure) {
	plan, f := u.stalePlan(ctx)
	if f != nil || len(plan.Remove) == 0 {
		return 0, f
	}
	if len(plan.Foreign) > 0 {
		return 0, u.foreignFailure(plan.Foreign)
	}
	_, _ = io.WriteString(stderr, unlockWarning)
	cli := newRestic(u.st.cfg, u.st.binary, u.deps, u.st.repo, runAs(u.st.who)).WithStderr(&u.log)
	f = u.bound().Attempt(ctx, u.target, &u.log, cli.Unlock, u.explain)
	return len(plan.Remove), f
}

// stalePlan is the plan of the second reading of the locks, made only when
// the first one found something to remove.
func (u *unlockRun) stalePlan(ctx context.Context) (hostcheck.UnlockPlan, *refusal.Failure) {
	env, err := lockEnv(u.deps)
	if err != nil {
		return hostcheck.UnlockPlan{}, &refusal.Failure{Class: refusal.ClassAgentError, Detail: fmt.Sprintf("the name of this host is unknown: %v", err)}
	}
	u.env = env
	plan, f := u.plan(ctx)
	if f != nil || len(plan.Remove) == 0 {
		return plan, f
	}
	return u.plan(ctx)
}

func (u *unlockRun) foreignFailure(foreign []hostcheck.Lock) *refusal.Failure {
	return refusal.Fail(refusal.StaleLocksForeign, "restic unlock would also remove locks this command may not: %s; nothing was removed. Remove a lock of another host on that host, and an own one younger than %s only once it is that old",
		hostcheck.DescribeLocks(u.env, foreign), hostcheck.FormatAge(hostcheck.StaleAfter))
}

func (u *unlockRun) bound() repoconnect.Bound {
	return repoconnect.Bound{Clock: u.deps.clock, Timeout: u.opts.connectTimeout}
}

func (u *unlockRun) explain(ctx context.Context, err error) *refusal.Failure {
	return restore.ListFailure(ctx, err, u.target)
}

// plan reads the locks and sorts them.
func (u *unlockRun) plan(ctx context.Context) (hostcheck.UnlockPlan, *refusal.Failure) {
	cli := newRestic(u.st.cfg, u.st.binary, u.deps, u.st.repo, runAs(u.st.who)).WithReadOnly().WithStderr(&u.log)
	var locks []hostcheck.Lock
	f := u.bound().Attempt(ctx, u.target, &u.log, func(limited context.Context) (err error) {
		locks, _, err = readLocks(limited, cli)
		return err
	}, u.explain)
	return hostcheck.PlanUnlock(u.env, locks), f
}

// tell says what was done, and writes the audit line (Р9).
func (u *unlockRun) tell(stdout, stderr io.Writer, who hostsetup.Principal, n int) {
	if n == 0 {
		_, _ = fmt.Fprintf(stdout, "no stale locks of this host in repository %s\n", u.st.repo.Name)
		return
	}
	noun := "locks"
	if n == 1 {
		noun = "lock"
	}
	_, _ = fmt.Fprintf(stdout, "removed %d stale %s of this host from repository %s\n", n, noun, u.st.repo.Name)
	newHostCmd("repo unlock", u.opts, who, stdout, stderr, u.deps).record("repository", u.st.repo.Name, fmt.Sprintf("stale locks removed: %d", n))
}
