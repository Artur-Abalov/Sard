// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package hostcheck_test

import (
	"strings"
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/hostcheck"
)

// Rule "repo check показывает блокировки restic с возрастом и хостом-владельцем" (Ч5).

var now = time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)

func env(alive ...int) hostcheck.LockEnv {
	return hostcheck.LockEnv{Host: "db1", Now: now, Alive: func(pid int) bool {
		for _, p := range alive {
			if p == pid {
				return true
			}
		}
		return false
	}}
}

func lock(host string, age time.Duration, pid int) hostcheck.Lock {
	return hostcheck.Lock{Hostname: host, PID: pid, Time: now.Add(-age)}
}

const h = time.Hour

func TestALockIsReadFromTheJSONOfRestic(t *testing.T) {
	l, err := hostcheck.ParseLock([]byte(`{"time":"2026-10-13T06:00:00.5+03:00","exclusive":true,"hostname":"db2","username":"root","pid":77,"uid":0,"gid":0}`))
	if err != nil {
		t.Fatal(err)
	}
	if l.Hostname != "db2" || l.PID != 77 || !l.Exclusive || !l.Time.Equal(time.Date(2026, 10, 13, 3, 0, 0, 5e8, time.UTC)) {
		t.Fatalf("lock %+v", l)
	}
	if _, err := hostcheck.ParseLock([]byte("not json")); err == nil {
		t.Fatal("a text that is no JSON is a lock")
	}
}

func TestTheAgeIsInHoursFromAnHourAndInMinutesBelow(t *testing.T) {
	for age, want := range map[time.Duration]string{30 * h: "30h", 24*h + 59*time.Minute: "24h", h: "1h", 59 * time.Minute: "59m", 10 * time.Minute: "10m", 0: "0m"} {
		if got := hostcheck.FormatAge(age); got != want {
			t.Errorf("%v: %q, want %q", age, got, want)
		}
	}
}

func TestNoLocksIsOK(t *testing.T) {
	l := hostcheck.LockLine("base", env(), nil, 0)
	if l.Status != hostcheck.OK || l.Check != "repository.locks" || l.Subject != "base" || l.Message != "no locks" || l.Hint != "" {
		t.Fatalf("%+v", l)
	}
}

func TestFreshLocksAreOKAndNamedByCountHostAndAge(t *testing.T) {
	l := hostcheck.LockLine("base", env(), []hostcheck.Lock{lock("db1", 10*time.Minute, 4242), lock("db2", 2*h, 77)}, 0)
	if l.Status != hostcheck.OK {
		t.Fatalf("%+v", l)
	}
	for _, want := range []string{"2 locks", "db1 10m", "db2 2h"} {
		if !strings.Contains(l.Message, want) {
			t.Errorf("message %q lacks %q", l.Message, want)
		}
	}
	one := hostcheck.LockLine("base", env(), []hostcheck.Lock{lock("db1", 10*time.Minute, 4242)}, 0)
	if !strings.Contains(one.Message, "1 lock ") {
		t.Errorf("message %q", one.Message)
	}
}

func TestALockExactlyAtTheThresholdIsNotStale(t *testing.T) {
	l := hostcheck.LockLine("base", env(), []hostcheck.Lock{lock("db1", 24*h, 4242)}, 0)
	if l.Status != hostcheck.OK {
		t.Fatalf("%+v", l)
	}
	l = hostcheck.LockLine("base", env(), []hostcheck.Lock{lock("db1", 24*h+time.Second, 4242)}, 0)
	if l.Status != hostcheck.Warn {
		t.Fatalf("a second past the threshold: %+v", l)
	}
}

func TestAStaleOwnLockOfADeadProcessOffersTheUnlockCommand(t *testing.T) {
	l := hostcheck.LockLine("base", env(), []hostcheck.Lock{lock("db1", 30*h, 4242)}, 0)
	if l.Status != hostcheck.Warn || !strings.Contains(l.Message, "db1") || !strings.Contains(l.Message, "30h") {
		t.Fatalf("%+v", l)
	}
	if !strings.Contains(l.Hint, "sudo sard-agent repo unlock base --remove-stale") {
		t.Errorf("hint %q", l.Hint)
	}
}

func TestAStaleOwnLockOfALiveProcessNamesTheProcessAndOffersNoUnlock(t *testing.T) {
	l := hostcheck.LockLine("base", env(4242), []hostcheck.Lock{lock("db1", 30*h, 4242)}, 0)
	if l.Status != hostcheck.Warn || !strings.Contains(l.Message, "held by running process 4242") {
		t.Fatalf("%+v", l)
	}
	if strings.Contains(l.Hint, "repo unlock") || !strings.Contains(l.Hint, "4242") {
		t.Errorf("hint %q", l.Hint)
	}
}

func TestAStaleForeignLockNamesTheHostAndIsToBeRemovedThere(t *testing.T) {
	l := hostcheck.LockLine("base", env(), []hostcheck.Lock{lock("db2", 48*h, 77)}, 0)
	if l.Status != hostcheck.Warn || !strings.Contains(l.Message, "db2") || !strings.Contains(l.Message, "48h") {
		t.Fatalf("%+v", l)
	}
	if !strings.Contains(l.Hint, "on db2") || strings.HasPrefix(l.Hint, "sudo") {
		t.Errorf("hint %q", l.Hint)
	}
}

func TestAnExclusiveStaleLockSaysSoAndYoungerLocksAreCounted(t *testing.T) {
	old := lock("db2", 48*h, 77)
	old.Exclusive = true
	l := hostcheck.LockLine("base", env(), []hostcheck.Lock{old, lock("db1", h, 5), lock("db1", h, 6)}, 0)
	if !strings.Contains(l.Message, "exclusive") || !strings.Contains(l.Message, "2 younger locks") {
		t.Fatalf("%+v", l)
	}
}

func TestALockThatDisappearedWhileReadingIsCountedAndDoesNotSpoilTheStep(t *testing.T) {
	l := hostcheck.LockLine("base", env(), nil, 1)
	if l.Status != hostcheck.OK || !strings.Contains(l.Message, "1 lock disappeared while reading") {
		t.Fatalf("%+v", l)
	}
	l = hostcheck.LockLine("base", env(), []hostcheck.Lock{lock("db1", h, 5)}, 2)
	if !strings.Contains(l.Message, "1 lock ") || !strings.Contains(l.Message, "2 locks disappeared while reading") {
		t.Fatalf("%+v", l)
	}
}

// Rule "repo unlock снимает только свои зависшие блокировки" (Ч6).

func TestOnlyOwnStaleLocksOfDeadProcessesAreRemoved(t *testing.T) {
	locks := []hostcheck.Lock{
		lock("db1", 30*h, 1),  // removed
		lock("db1", 30*h, 2),  // process alive
		lock("db1", 24*h, 3),  // at the threshold
		lock("db2", 48*h, 77), // foreign
	}
	plan := hostcheck.PlanUnlock(env(2), locks)
	if len(plan.Remove) != 1 || plan.Remove[0].PID != 1 {
		t.Fatalf("remove %+v", plan.Remove)
	}
}

func TestWhatResticWouldRemoveBeyondTheRuleIsForeign(t *testing.T) {
	locks := []hostcheck.Lock{
		lock("db1", 30*h, 1),            // removed
		lock("db2", 48*h, 77),           // foreign, older than 30 minutes
		lock("db1", 2*h, 3),             // own, younger than the threshold, process dead
		lock("db2", 5*time.Minute, 78),  // foreign but younger than 30 minutes: restic keeps it
		lock("db1", 2*h, 4),             // own, process alive: restic keeps it only if young
		lock("db1", 10*time.Minute, 79), // own, young, alive
	}
	plan := hostcheck.PlanUnlock(env(4, 79), locks)
	var pids []int
	for _, l := range plan.Foreign {
		pids = append(pids, l.PID)
	}
	if len(plan.Remove) != 1 || len(pids) != 3 || pids[0] != 77 || pids[1] != 3 || pids[2] != 4 {
		t.Fatalf("remove %+v foreign pids %v", plan.Remove, pids)
	}
}
