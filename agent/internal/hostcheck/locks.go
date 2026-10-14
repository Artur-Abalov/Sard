// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package hostcheck

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// StaleAfter is the age from which a lock of restic counts as stale for
// the agent (ЧВ1); a lock exactly this old is not yet stale.
const StaleAfter = 24 * time.Hour

// ResticStaleAfter is the age from which restic unlock removes a lock,
// whatever host holds it.
const ResticStaleAfter = 30 * time.Minute

// Lock is a lock of restic as `restic cat lock` shows it.
type Lock struct {
	Hostname  string    `json:"hostname"`
	PID       int       `json:"pid"`
	Time      time.Time `json:"time"`
	Exclusive bool      `json:"exclusive"`
}

// ParseLock reads the JSON `restic cat lock` prints.
func ParseLock(data []byte) (Lock, error) {
	var l Lock
	if err := json.Unmarshal(data, &l); err != nil {
		return Lock{}, fmt.Errorf("restic cat lock: %w", err)
	}
	return l, nil
}

// LockEnv is what a lock is judged against: this host, the time of the
// agent's clock and the question whether a process of this host is alive.
type LockEnv struct {
	Host  string
	Now   time.Time
	Alive func(pid int) bool
}

func (e LockEnv) age(l Lock) time.Duration { return e.Now.Sub(l.Time) }

func (e LockEnv) own(l Lock) bool { return l.Hostname == e.Host }

func (e LockEnv) stale(l Lock) bool { return e.age(l) > StaleAfter }

// removable: a lock the agent may remove itself (Ч6) is an own, stale one
// whose process is gone.
func (e LockEnv) removable(l Lock) bool {
	return e.own(l) && e.stale(l) && !e.Alive(l.PID)
}

// resticRemoves: restic unlock removes a lock older than 30 minutes on any
// host, and a lock of this host whose process is gone.
func (e LockEnv) resticRemoves(l Lock) bool {
	return e.age(l) > ResticStaleAfter || (e.own(l) && !e.Alive(l.PID))
}

// FormatAge is the age in whole hours from an hour on, in minutes below.
func FormatAge(d time.Duration) string {
	if d >= time.Hour {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dm", int(d.Minutes()))
}

func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// LockLine is the line of repository.locks for the locks read (Ч5); gone
// counts the locks that were listed and disappeared before they were read.
func LockLine(repo string, env LockEnv, locks []Lock, gone int) Line {
	stale, fresh := splitStale(env, locks)
	line := Line{Check: "repository.locks", Subject: repo, Status: OK, Message: lockMessage(env, locks, stale, fresh, gone)}
	if len(stale) > 0 {
		line.Status, line.Hint = Warn, staleHint(repo, env, stale)
	}
	return line
}

func splitStale(env LockEnv, locks []Lock) (stale, fresh []Lock) {
	for _, l := range locks {
		if env.stale(l) {
			stale = append(stale, l)
		} else {
			fresh = append(fresh, l)
		}
	}
	return stale, fresh
}

func lockMessage(env LockEnv, locks, stale, fresh []Lock, gone int) string {
	var parts []string
	if len(locks) == 0 {
		parts = append(parts, "no locks")
	}
	for _, l := range stale {
		parts = append(parts, describeStale(env, l))
	}
	if len(fresh) > 0 {
		parts = append(parts, describeFresh(env, fresh, len(stale) > 0))
	}
	if gone > 0 {
		parts = append(parts, plural(gone, "lock")+" disappeared while reading")
	}
	return strings.Join(parts, "; ")
}

func describeStale(env LockEnv, l Lock) string {
	text := fmt.Sprintf("stale lock of %s, %s old", l.Hostname, FormatAge(env.age(l)))
	if l.Exclusive {
		text += ", exclusive"
	}
	if env.own(l) && env.Alive(l.PID) {
		text += fmt.Sprintf(", held by running process %d", l.PID)
	}
	return text
}

// describeFresh names the locks that are not stale: by count, host and age
// alone, or as "younger" next to stale ones.
func describeFresh(env LockEnv, fresh []Lock, withStale bool) string {
	if withStale {
		return plural(len(fresh), "younger lock")
	}
	held := make([]string, len(fresh))
	for i, l := range fresh {
		held[i] = l.Hostname + " " + FormatAge(env.age(l))
	}
	return fmt.Sprintf("%s (%s): a backup or a check is probably running", plural(len(fresh), "lock"), strings.Join(held, ", "))
}

// staleHint is what to do about the stale locks: the unlock command for the
// own ones whose process is gone, a look at a process that is alive, and for
// the locks of other hosts the same command there.
func staleHint(repo string, env LockEnv, stale []Lock) string {
	command := fmt.Sprintf("sudo sard-agent repo unlock %s --remove-stale", repo)
	var hints []string
	var hosts []string
	for _, l := range stale {
		switch {
		case !env.own(l):
			hosts = appendOnce(hosts, l.Hostname)
		case env.Alive(l.PID):
			hints = append(hints, fmt.Sprintf("check the process %d (ps -p %d): a lock of a running process is not stale", l.PID, l.PID))
		default:
			hints = appendOnce(hints, command)
		}
	}
	if len(hosts) > 0 {
		hints = append(hints, fmt.Sprintf("remove the lock on %s with the same command (%s), not here: the agent never removes locks of other hosts", strings.Join(hosts, ", "), command))
	}
	return strings.Join(hints, "; ")
}

func appendOnce(list []string, s string) []string {
	for _, x := range list {
		if x == s {
			return list
		}
	}
	return append(list, s)
}

// UnlockPlan is what repo unlock does with the locks it has read.
type UnlockPlan struct {
	// Remove are the locks the command exists to remove.
	Remove []Lock
	// Foreign are the locks restic unlock would remove on top of them, which
	// the command may not: it refuses while there is one.
	Foreign []Lock
}

// PlanUnlock sorts the locks (Ч6).
func PlanUnlock(env LockEnv, locks []Lock) UnlockPlan {
	var plan UnlockPlan
	for _, l := range locks {
		switch {
		case env.removable(l):
			plan.Remove = append(plan.Remove, l)
		case env.resticRemoves(l):
			plan.Foreign = append(plan.Foreign, l)
		}
	}
	return plan
}

// DescribeLocks names locks by host and age, for a message.
func DescribeLocks(env LockEnv, locks []Lock) string {
	parts := make([]string, len(locks))
	for i, l := range locks {
		parts[i] = fmt.Sprintf("%s %s", l.Hostname, FormatAge(env.age(l)))
	}
	return strings.Join(parts, ", ")
}
