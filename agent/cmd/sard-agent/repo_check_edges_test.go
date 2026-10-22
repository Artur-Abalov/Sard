// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/hostcheck"
	"github.com/Artur-Abalov/sard/agent/internal/repoconnect"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
)

// readOnlyCLI is the restic of the repository base as the checks use it.
func (h *setupHost) readOnlyCLI() *restic.CLI {
	repo := h.cfg.Repositories[0]
	return newRestic(h.cfg, h.cfg.Restic.Path, h.deps, repo, nil).WithReadOnly()
}

func TestAllTheLocksOfARepositoryAreReadAndTheOnesThatWentAreCounted(t *testing.T) {
	h := fullHost(t)
	r := h.base()
	r.addLock(h.clock.now, "db1", time.Hour, 1)
	r.addLock(h.clock.now, "db2", 2*time.Hour, 2)
	r.addLock(h.clock.now, "db3", 3*time.Hour, 3)
	r.checkState.gone = map[string]bool{"L2": true}
	locks, gone, err := readLocks(context.Background(), h.readOnlyCLI())
	if err != nil || gone != 1 || len(locks) != 2 || locks[0].Hostname != "db1" || locks[1].Hostname != "db3" {
		t.Errorf("locks %+v, gone %d, err %v", locks, gone, err)
	}
}

func TestALockThatIsNoLockIsCountedAsGone(t *testing.T) {
	h := fullHost(t)
	r := h.base()
	r.addLock(h.clock.now, "db1", time.Hour, 1)
	r.script = func(sub string, _ []string) (string, int, bool) { return "", 0, false }
	h.deps.exec = &garbledLocks{inner: h.restic}
	locks, gone, err := readLocks(context.Background(), h.readOnlyCLI())
	if err != nil || gone != 1 || len(locks) != 0 {
		t.Errorf("locks %+v, gone %d, err %v", locks, gone, err)
	}
}

// garbledLocks answers restic cat lock with text that is no JSON.
type garbledLocks struct{ inner restic.Executor }

func (g *garbledLocks) Run(ctx context.Context, cmd restic.Command) (int, error) {
	if len(cmd.Args) > 1 && cmd.Args[0] == "cat" && cmd.Args[1] == "lock" {
		cmd.Stdout([]byte("not json"))
		return 0, nil
	}
	return g.inner.Run(ctx, cmd)
}

func TestWhenTheListOfLocksCannotBeReadThereIsNoLockAndNoneIsGone(t *testing.T) {
	h := fullHost(t)
	h.base().script = func(sub string, _ []string) (string, int, bool) {
		return "Fatal: unable to open repository", 1, sub == "list"
	}
	locks, gone, err := readLocks(context.Background(), h.readOnlyCLI())
	if err == nil || gone != 0 || locks != nil {
		t.Errorf("locks %+v, gone %d, err %v", locks, gone, err)
	}
}

func TestAnInterruptWhileReadingTheLocksEndsTheReading(t *testing.T) {
	h := fullHost(t)
	r := h.base()
	r.addLock(h.clock.now, "db1", time.Hour, 1)
	r.addLock(h.clock.now, "db2", time.Hour, 2)
	ctx, cancel := context.WithCancel(context.Background())
	r.script = func(sub string, _ []string) (string, int, bool) {
		if sub == "cat" {
			cancel()
		}
		return "", 0, false
	}
	locks, gone, err := readLocks(ctx, h.readOnlyCLI())
	if !errors.Is(err, context.Canceled) || gone != 0 || locks != nil {
		t.Errorf("locks %+v, gone %d, err %v", locks, gone, err)
	}
}

func TestAnInterruptBetweenTwoRepositoriesLeavesTheSecondOneUntouched(t *testing.T) {
	h := fullHost(t)
	h.fragmentRepo("obj")
	ctx, cancel := context.WithCancel(context.Background())
	h.base().script = func(sub string, _ []string) (string, int, bool) {
		if sub == "list" {
			cancel()
		}
		return "", 0, false
	}
	var out, errOut strings.Builder
	h.deps.euid = 0
	code := runCheck(ctx, h.subst([]string{"--config", "C"}), &out, &errOut, h.deps)
	if code != exitTemporary {
		t.Errorf("code %d", code)
	}
	for _, c := range h.restic.calls {
		if c.repository == h.path("obj") && c.sub != "version" {
			t.Errorf("restic %v ran on the repository obj", c.args)
		}
	}
}

// --- the flags of the commands of the host check

func parse(spec cmdSpec, args ...string) (hostOptions, int, string) {
	var stderr strings.Builder
	opts, code := parseFlags(spec, args, &stderr, "/etc/sard/agent.yaml")
	return opts, code, stderr.String()
}

// arity is the names each command requires and the ones it takes besides.
var arity = map[string][2]int{
	"check": {0, 0}, "repo init": {1, 0}, "repo list": {0, 0}, "repo add": {2, 0}, "repo show": {1, 0}, "repo remove": {1, 0},
	"repo password": {1, 0}, "repo snapshots": {1, 0}, "repo restore": {2, 0}, "repo check": {0, 1}, "repo unlock": {1, 0},
	"secret set": {1, 0}, "secret list": {0, 0}, "secret remove": {1, 0},
	"repo key add": {1, 0}, "repo key list": {1, 0}, "repo key remove": {2, 0},
}

// argsOf is n names and the flags the command needs for its names to be the
// only thing in question.
func argsOf(name string, n int) []string {
	extra := []string{"--config", "C"}
	switch name {
	case "repo restore":
		extra = append(extra, "--target", "/restore")
	case "repo unlock":
		extra = append(extra, "--remove-stale")
	case "check", "secret list":
		extra = nil
	}
	return append([]string{"a", "abcdef12", "c", "d"}[:n], extra...)
}

func allSpecs() map[string]cmdSpec {
	specs := map[string]cmdSpec{"check": checkSpec}
	for k, s := range repoSpecs {
		specs["repo "+k] = s
	}
	for k, s := range secretSpecs {
		specs["secret "+k] = s
	}
	return specs
}

func TestEveryCommandIsKnownToTheTable(t *testing.T) {
	specs := allSpecs()
	if len(arity) != len(specs) {
		t.Fatalf("%d commands, %d known", len(specs), len(arity))
	}
	for name := range specs {
		if _, ok := arity[name]; !ok {
			t.Errorf("%s is not in the table", name)
		}
	}
}

func TestOneNameTooManyIsRefusedByEveryCommand(t *testing.T) {
	for name, spec := range allSpecs() {
		n := arity[name][0] + arity[name][1] + 1
		if _, code, msg := parse(spec, argsOf(name, n)...); code != exitUsage || !strings.Contains(msg, "unexpected extra argument") {
			t.Errorf("%s with %d names: %d %q", name, n, code, msg)
		}
	}
}

func TestOneNameTooFewIsRefusedByEveryCommandThatNeedsOne(t *testing.T) {
	for name, spec := range allSpecs() {
		if arity[name][0] == 0 {
			continue
		}
		n := arity[name][0] - 1
		if _, code, msg := parse(spec, argsOf(name, n)...); code != exitUsage || !strings.Contains(msg, "required") {
			t.Errorf("%s with %d names: %d %q", name, n, code, msg)
		}
	}
}

func TestEveryCommandTakesTheNamesItNeedsAndTheOptionalOnes(t *testing.T) {
	for name, spec := range allSpecs() {
		for n := arity[name][0]; n <= arity[name][0]+arity[name][1]; n++ {
			if _, code, msg := parse(spec, argsOf(name, n)...); code != exitOK {
				t.Errorf("%s with %d names: %d %q", name, n, code, msg)
			}
		}
	}
}

func TestRepoCheckTakesOneOptionalNameAndCheckTakesNone(t *testing.T) {
	opts, code, msg := parse(repoSpecs["check"], "base")
	if code != exitOK || opts.name != "base" {
		t.Errorf("repo check base: %d %q %+v", code, msg, opts)
	}
	if _, code, _ := parse(repoSpecs["check"]); code != exitOK {
		t.Errorf("repo check: %d", code)
	}
	if _, code, msg := parse(checkSpec, "base"); code != exitUsage || !strings.Contains(msg, "unexpected extra argument") {
		t.Errorf("check base: %d %q", code, msg)
	}
}

func TestRemoveStaleIsRequiredByUnlock(t *testing.T) {
	spec := repoSpecs["unlock"]
	if _, code, msg := parse(spec, "base"); code != exitUsage || !strings.Contains(msg, "--remove-stale is required") {
		t.Errorf("without: %d %q", code, msg)
	}
	opts, code, msg := parse(spec, "base", "--remove-stale", "--timeout", "1m", "--connect-timeout", "5s")
	if code != exitOK || !opts.removeStale || opts.timeout != time.Minute || opts.connectTimeout != 5*time.Second {
		t.Errorf("with: %d %q %+v", code, msg, opts)
	}
}

func TestRemoveStaleIsKnownToNoOtherCommand(t *testing.T) {
	for _, other := range []cmdSpec{repoSpecs["list"], repoSpecs["check"], checkSpec} {
		if _, code, msg := parse(other, "--remove-stale"); code != exitUsage || !strings.Contains(msg, "flag provided but not defined") {
			t.Errorf("%s --remove-stale: %d %q", other.words, code, msg)
		}
	}
}

func TestOfflineIsAFlagOfCheckOnly(t *testing.T) {
	opts, code, msg := parse(checkSpec, "--offline")
	if code != exitOK || !opts.offline {
		t.Errorf("check --offline: %d %q", code, msg)
	}
	for _, other := range []cmdSpec{repoSpecs["list"], repoSpecs["check"], repoSpecs["unlock"]} {
		if _, code, msg := parse(other, "--offline"); code != exitUsage || !strings.Contains(msg, "flag provided but not defined") {
			t.Errorf("%s --offline: %d %q", other.words, code, msg)
		}
	}
}

func TestReadDataIsAFlagOfRepoCheckAndRefusedByCheck(t *testing.T) {
	if _, code, msg := parse(checkSpec, "--read-data", "5"); code != exitUsage || !strings.Contains(msg, "--read-data is a flag of `sard-agent repo check`") {
		t.Errorf("check: %d %q", code, msg)
	}
	for _, other := range []cmdSpec{repoSpecs["list"], repoSpecs["unlock"]} {
		if _, code, msg := parse(other, "--read-data", "5"); code != exitUsage || !strings.Contains(msg, "flag provided but not defined") {
			t.Errorf("%s: %d %q", other.words, code, msg)
		}
	}
}

func TestReadDataIsAWholePercentFromOneToOneHundred(t *testing.T) {
	for value, want := range map[string]bool{"1": true, "2": true, "99": true, "100": true, "0": false, "101": false, "-1": false, "5.5": false, "x": false, "": false} {
		opts, code, msg := parse(repoSpecs["check"], "--read-data", value)
		if (code == exitOK) != want {
			t.Errorf("--read-data %q: %d %q", value, code, msg)
		}
		if want && fmt.Sprint(opts.readData) != value {
			t.Errorf("--read-data %q: %d", value, opts.readData)
		}
		if !want && !strings.Contains(msg, "--read-data must be a whole number from 1 to 100") {
			t.Errorf("--read-data %q: message %q", value, msg)
		}
	}
}

func TestAPercentIsShownAsTheNumberAndNothingIsShownWhenThereIsNone(t *testing.T) {
	zero, one, five := 0, 1, 5
	for want, flag := range map[string]percentFlag{"": {}, " ": {d: &zero}, "1": {d: &one}, "5": {d: &five}} {
		if got := flag.String(); got != strings.TrimSpace(want) {
			t.Errorf("%v: %q, want %q", flag.d, got, want)
		}
	}
}

func TestReadDataLiftsTheTimeLimitUnlessTheOperatorGivesOne(t *testing.T) {
	spec := repoSpecs["check"]
	for name, c := range map[string]struct {
		args []string
		want time.Duration
	}{
		"without read-data":    {[]string{"base"}, defaultRepoTimeout},
		"with read-data":       {[]string{"base", "--read-data", "5"}, 0},
		"read-data of one":     {[]string{"base", "--read-data", "1"}, 0},
		"with a given timeout": {[]string{"base", "--read-data", "5", "--timeout", "90m"}, 90 * time.Minute},
	} {
		if opts, code, msg := parse(spec, c.args...); code != exitOK || opts.timeout != c.want {
			t.Errorf("%s: %d %q timeout %v, want %v", name, code, msg, opts.timeout, c.want)
		}
	}
}

func TestTheConnectTimeoutOfTheChecksIsShortAndTheOthersIsLong(t *testing.T) {
	for name, c := range map[string]struct {
		spec cmdSpec
		want time.Duration
	}{
		"check": {checkSpec, shortConnectTimeout}, "repo check": {repoSpecs["check"], shortConnectTimeout},
		"repo unlock": {repoSpecs["unlock"], defaultConnectTimeout}, "repo snapshots": {repoSpecs["snapshots"], defaultConnectTimeout},
	} {
		args := []string{"base"}
		switch name {
		case "check":
			args = nil
		case "repo unlock":
			args = append(args, "--remove-stale")
		}
		if opts, code, msg := parse(c.spec, args...); code != exitOK || opts.connectTimeout != c.want {
			t.Errorf("%s: %d %q %v, want %v", name, code, msg, opts.connectTimeout, c.want)
		}
	}
}

// --- repo check

// interrupted runs repo check base --read-data 5 and cancels the command at the
// n-th restic command called sub.
func (h *setupHost) interruptedAt(sub string) (code int, stdout string) {
	ctx, cancel := context.WithCancel(context.Background())
	h.base().script = func(got string, _ []string) (string, int, bool) {
		if got == sub {
			cancel()
		}
		return "", 0, false
	}
	var out, errOut strings.Builder
	h.deps.euid = 0
	code = runRepoWithDeps(ctx, h.subst([]string{"check", "base", "--config", "C", "--read-data", "5"}), &out, &errOut, h.deps)
	return code, out.String()
}

func TestAnInterruptAtTheFirstAccessReportsNoCheckAsOKAndSkipsTheRest(t *testing.T) {
	h := checkHost(t)
	code, stdout := h.interruptedAt("cat")
	assertCode(t, code, exitTemporary)
	lines, _ := parseOut(t, stdout)
	var ids []string
	for _, l := range lines {
		ids = append(ids, l.check)
		if l.status != "SKIP" {
			t.Errorf("%+v", l)
		}
	}
	want := []string{"repository.credentials", "repository.initialized", "repository.password", "repository.id", "repository.keys", "repository.locks", "repository.integrity"}
	if !slices.Equal(ids, want) {
		t.Errorf("lines %v, want %v", ids, want)
	}
}

func TestAnInterruptWhileTheLocksAreReadLeavesTheLocksLineOut(t *testing.T) {
	h := checkHost(t)
	code, stdout := h.interruptedAt("list")
	assertCode(t, code, exitTemporary)
	lines, _ := parseOut(t, stdout)
	if last := lines[len(lines)-1]; last.check != "repository.keys" || last.status != "OK" {
		t.Errorf("lines %v", lines)
	}
}

func TestAnInterruptOfTheIntegrityCheckIsAnInterruptNotAFindingOfErrors(t *testing.T) {
	h := checkHost(t)
	h.base().checkState.checkCode = 1
	h.base().checkState.checkStderr = []string{"Fatal: killed"}
	code, stdout := h.interruptedAt("check")
	assertCode(t, code, exitTemporary)
	if strings.Contains(stdout, "found errors") || strings.Contains(stdout, "repository.integrity") {
		t.Errorf("stdout %q", stdout)
	}
}

func TestACheckThatEndedBecauseOfAnInterruptDoesNotTellOfALockedRepository(t *testing.T) {
	h := checkHost(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := &repoCheck{ctx: ctx, opts: hostOptions{readData: 5, connectTimeout: time.Second}, deps: h.deps, cfg: h.cfg, binary: h.cfg.Restic.Path}
	r := newRepoSteps(c, ctx, 0)
	r.target = repoconnect.TargetFor(r.repo, nil)
	h.base().checkState.checkCode = 11
	if l := r.integrityStep(); l.Status != hostcheck.Error || strings.Contains(l.Message, "locked by another process") || l.Hint != "see "+hostcheck.HelpURL(hostcheck.CheckRepositoryIntegrity) {
		t.Errorf("%+v", l)
	}
	h.base().checkState.checkCode = 1
	h.base().checkState.checkStderr = []string{"Fatal: killed"}
	if l := r.integrityStep(); strings.Contains(l.Message, "found errors") {
		t.Errorf("%+v", l)
	}
}

func TestABrokenConfigStopsRepoCheckBeforeAnyRepository(t *testing.T) {
	h := checkHost(t)
	h.write(h.cfgPath, "server: [", 0o640)
	code, stdout, stderr := h.repoCheck()
	assertCode(t, code, exitUsage)
	if stdout != "" || !strings.Contains(stderr, "reading config") {
		t.Errorf("stdout %q stderr %q", stdout, stderr)
	}
}

func TestAFlagErrorStopsRepoCheckBeforeAnythingIsRead(t *testing.T) {
	h := checkHost(t)
	code, stdout, stderr := h.repoCheck("--read-data", "0")
	assertCode(t, code, exitUsage)
	if stdout != "" || stderr != "sard-agent repo check: invalid value for flag -read-data: --read-data must be a whole number from 1 to 100\n" || len(h.restic.subs()) != 0 {
		t.Errorf("stdout %q stderr %q restic %v", stdout, stderr, h.restic.subs())
	}
}

func TestAnInterruptBetweenTwoRepositoriesOfRepoCheckLeavesTheSecondUntouched(t *testing.T) {
	h := checkHost(t)
	h.fragmentRepo("obj")
	ctx, cancel := context.WithCancel(context.Background())
	h.base().script = func(sub string, _ []string) (string, int, bool) {
		if sub == "list" {
			cancel()
		}
		return "", 0, false
	}
	var out, errOut strings.Builder
	h.deps.euid = 0
	code := runRepoWithDeps(ctx, h.subst([]string{"check", "--config", "C"}), &out, &errOut, h.deps)
	assertCode(t, code, exitTemporary)
	for _, c := range h.restic.calls {
		if c.repository == h.path("obj") && c.sub != "version" {
			t.Errorf("restic %v ran on the repository obj", c.args)
		}
	}
	if strings.Contains(out.String(), " obj ") {
		t.Errorf("stdout %q", out.String())
	}
}

func TestAnEncryptionTheAgentCannotDoIsAFailureOfThePasswordStep(t *testing.T) {
	h := checkHost(t)
	h.cfg.Repositories[0].CryptoProvider = "vault"
	h.saveHostConfig()
	code, stdout, _ := h.repoCheck("base")
	assertCode(t, code, exitAgentError)
	lines, _ := parseOut(t, stdout)
	for _, id := range []string{"repository.storage", "repository.credentials", "repository.initialized"} {
		if l := line(t, lines, id, "base"); l.status != "OK" {
			t.Errorf("%+v", l)
		}
	}
	if l := line(t, lines, "repository.password", "base"); l.status != "ERROR" || !strings.HasPrefix(l.message, "CRYPTO_PROVIDER_UNSUPPORTED: ") {
		t.Errorf("%+v", l)
	}
	if l := line(t, lines, "repository.id", "base"); l.status != "SKIP" || l.message != "not checked: repository.password failed" {
		t.Errorf("%+v", l)
	}
}

func TestReadingOnePercentOfTheDataIsAnIntegrityCheck(t *testing.T) {
	h := checkHost(t)
	code, stdout, _ := h.repoCheck("base", "--read-data", "1")
	assertCode(t, code, exitOK)
	lines, _ := parseOut(t, stdout)
	if l := line(t, lines, "repository.integrity", "base"); l.status != "OK" || l.message != "restic read 1% of the data and found no errors" {
		t.Errorf("%+v", l)
	}
}

func TestTheErrorsResticFoundAreNamedAfterTheWordsOfTheCommand(t *testing.T) {
	h := checkHost(t)
	h.base().checkState.checkCode = 1
	h.base().checkState.checkStderr = []string{"Fatal: pack abc contains 1 errors"}
	_, stdout, _ := h.repoCheck("base", "--read-data", "5")
	lines, _ := parseOut(t, stdout)
	if l := line(t, lines, "repository.integrity", "base"); l.message != "restic check found errors in the repository: Fatal: pack abc contains 1 errors" {
		t.Errorf("%+v", l)
	}
}

func TestWhatResticSaidIsCutToTwoHundredCharacters(t *testing.T) {
	for n, want := range map[int]int{150: 150, 199: 199, 200: 200, 201: 200, 400: 200} {
		h := checkHost(t)
		h.base().checkState.checkCode = 1
		said := "Fatal: " + strings.Repeat("e", n-len("Fatal: "))
		h.base().checkState.checkStderr = []string{said}
		_, stdout, _ := h.repoCheck("base", "--read-data", "5")
		lines, _ := parseOut(t, stdout)
		l := line(t, lines, "repository.integrity", "base")
		if got := strings.TrimPrefix(l.message, "restic check found errors in the repository: "); got != said[:want] {
			t.Errorf("%d characters: %d kept, want %d", n, len(got), want)
		}
	}
}

// --- repo unlock

func TestTheNumberOfLocksRemovedIsSaidInTheRightNumber(t *testing.T) {
	for n, want := range map[int]string{1: "removed 1 stale lock of this host from repository base\n", 2: "removed 2 stale locks of this host from repository base\n", 3: "removed 3 stale locks of this host from repository base\n"} {
		h := checkHost(t)
		for i := range n {
			h.base().addLock(h.clock.now, "db1", time.Duration(30+i)*time.Hour, 4242+i)
		}
		code, stdout, _ := h.unlock("--remove-stale")
		assertCode(t, code, exitOK)
		if stdout != want {
			t.Errorf("%d locks: stdout %q, want %q", n, stdout, want)
		}
	}
}

func TestWithoutStaleLocksTheLocksAreReadOnceAndNothingIsRemoved(t *testing.T) {
	h := checkHost(t)
	h.base().addLock(h.clock.now, "db1", 5*time.Minute, 4242)
	code, stdout, stderr := h.unlock("--remove-stale")
	assertCode(t, code, exitOK)
	if stdout != "no stale locks of this host in repository base\n" || stderr != "" || len(h.unlocks()) != 0 {
		t.Errorf("stdout %q stderr %q unlocks %d", stdout, stderr, len(h.unlocks()))
	}
	if n := h.base().checkState.lists; n != 1 {
		t.Errorf("the locks were listed %d times", n)
	}
}

func TestStaleLocksAreReadAgainRightBeforeTheyAreRemoved(t *testing.T) {
	h := checkHost(t)
	h.base().addLock(h.clock.now, "db1", 30*time.Hour, 4242)
	h.unlock("--remove-stale")
	if n := h.base().checkState.lists; n != 2 {
		t.Errorf("the locks were listed %d times", n)
	}
}

func TestAFlagErrorStopsRepoUnlockBeforeAnythingIsRead(t *testing.T) {
	h := checkHost(t)
	code, stdout, stderr := h.unlock("--remove-stale", "--timeout", "0s")
	assertCode(t, code, exitUsage)
	if stdout != "" || stderr != "sard-agent repo unlock: --timeout must be a positive duration, got \"0s\"\n" || len(h.restic.subs()) != 0 {
		t.Errorf("stdout %q stderr %q restic %v", stdout, stderr, h.restic.subs())
	}
}
