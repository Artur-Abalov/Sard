// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

//go:build integration

package retention_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/crypto"
	"github.com/Artur-Abalov/sard/agent/internal/executor"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
	"github.com/Artur-Abalov/sard/agent/internal/retention"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

// The @restic scenarios of docs/specs/agent/retention-forget.feature: the
// pinned restic (scripts/fetch-restic.sh) on a repository in a temporary
// directory; snapshots are made by restic backup --time --host --tag.

type realRepo struct {
	t       *testing.T
	bin     string
	dir     string
	data    string
	pass    string
	cli     *restic.CLI
	nextRun int
}

func newRealRepo(t *testing.T) *realRepo {
	t.Helper()
	bin, err := filepath.Abs(filepath.Join("..", "..", "..", ".bin", "restic", restic.Pinned.String(), "linux_"+runtime.GOARCH, "restic"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(bin); err != nil {
		t.Fatalf("pinned restic missing, run scripts/fetch-restic.sh: %v", err)
	}
	tmp := t.TempDir()
	r := &realRepo{t: t, bin: bin, dir: filepath.Join(tmp, "repo"), data: filepath.Join(tmp, "data"), pass: filepath.Join(tmp, "password")}
	for path, content := range map[string]string{r.pass: "integration test password\n", filepath.Join(r.data, "f"): "x"} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	r.cli = restic.New(restic.Options{
		Binary: bin, CacheDir: filepath.Join(tmp, "cache"), Path: os.Getenv("PATH"), Exec: restic.ProcessExecutor{},
		Keys: crypto.NewResticAES(map[string]string{"it": r.pass}), ReadFile: os.ReadFile, Stderr: t.Output(),
	}, config.Repository{Name: "it", URL: r.dir, PasswordFile: r.pass})
	if _, err := r.cli.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	return r
}

func (r *realRepo) command(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, r.bin, args...)
	cmd.Env = []string{"RESTIC_REPOSITORY=" + r.dir, "RESTIC_PASSWORD_FILE=" + r.pass, "RESTIC_CACHE_DIR=" + filepath.Join(filepath.Dir(r.dir), "cache"), "TZ=UTC", "PATH=" + os.Getenv("PATH")}
	return cmd
}

func (r *realRepo) restic(args ...string) []byte {
	r.t.Helper()
	out, err := r.command(context.Background(), args...).Output()
	if err != nil {
		r.t.Fatalf("restic %v: %v", args, err)
	}
	return out
}

// seed makes a snapshot of the time with the tags and returns its full id.
func (r *realRepo) seed(host string, tm time.Time, tags ...string) string {
	r.t.Helper()
	args := []string{"backup", "--json", "--time", tm.UTC().Format("2006-01-02 15:04:05"), "--host", host}
	if len(tags) > 0 {
		args = append(args, "--tag", strings.Join(tags, ","))
	}
	var summary struct {
		Type string `json:"message_type"`
		ID   string `json:"snapshot_id"`
	}
	for line := range strings.Lines(string(r.restic(append(args, r.data)...))) {
		if json.Unmarshal([]byte(line), &summary) == nil && summary.Type == "summary" {
			return summary.ID
		}
	}
	r.t.Fatal("no summary")
	return ""
}

// main is a main snapshot of source s, as the step tags it.
func (r *realRepo) main(host, source string, tm time.Time) string {
	r.nextRun++
	return r.seed(host, tm, "sard.source="+source, fmt.Sprintf("sard.run=r-%d", r.nextRun), "sard.step=st")
}

func (r *realRepo) fingerprint(source, of string, tm time.Time) string {
	return r.seed("maint", tm.Add(time.Second), "sard.source="+source, "files.part=fingerprint", "files.main_snapshot="+of)
}

func (r *realRepo) globals(source, of string, tm time.Time) string {
	return r.seed("maint", tm.Add(time.Second), "sard.source="+source, "postgresql.part=globals", "postgresql.main_snapshot="+of)
}

// ids lists the full ids of the snapshots, optionally of one source.
func (r *realRepo) ids(source string) []string {
	r.t.Helper()
	var tags []string
	if source != "" {
		tags = []string{"sard.source=" + source}
	}
	snaps, err := r.cli.Snapshots(context.Background(), tags)
	if err != nil {
		r.t.Fatal(err)
	}
	var out []string
	for _, s := range snaps {
		out = append(out, s.ID)
	}
	return out
}

func (r *realRepo) handler(floor config.Floor) *retention.Handler {
	return retention.New(retention.Options{
		Stores: func(string, string, io.Writer) (retention.Store, bool) { return r.cli, true },
		Floors: func(string) config.Floor { return floor },
		Now:    func() time.Time { return now },
	})
}

func (r *realRepo) forget(floor config.Floor, source string, policy *agentv1.RetentionPolicy) (*agentv1.ForgetOutput, error) {
	r.t.Helper()
	step := &agentv1.RunStep{CommandId: "c1", Action: agentv1.Action_ACTION_FORGET, RepositoryName: "it",
		Forget: &agentv1.ForgetTask{SourceId: source, Policy: policy}}
	res, err := r.handler(floor).Run(context.Background(), step, &fakeReporter{})
	return res.GetForget(), err
}

func (r *realRepo) mustForget(floor config.Floor, source string, policy *agentv1.RetentionPolicy) *agentv1.ForgetOutput {
	r.t.Helper()
	out, err := r.forget(floor, source, policy)
	if err != nil {
		r.t.Fatalf("forget: %v", err)
	}
	return out
}

func day(n int) time.Time { return time.Date(2026, 11, 30, 10, 0, 0, 0, time.UTC).AddDate(0, 0, n) }

func TestResticKeepLastОставляетПоследниеСнимкиИсточникаИНеТрогаетЧужие(t *testing.T) {
	r := newRealRepo(t)
	var m []string
	for i := 1; i <= 5; i++ {
		m = append(m, r.main("maint", "S", day(i-5)))
		r.main("maint", "T", day(i-5))
	}
	for i := range 3 {
		r.seed("maint", day(-20-i)) // made by hand, no tags
	}
	out := r.mustForget(noFloor, "S", &agentv1.RetentionPolicy{KeepLast: 2})
	sameSet(t, "forgotten", out.ForgottenSnapshotIds, m[:3])
	sameSet(t, "remaining", out.RemainingSnapshotIds, m[3:])
	sameSet(t, "remaining in the repository", r.ids("S"), m[3:])
	if got := len(r.ids("")); got != 5+2+3 {
		t.Errorf("%d snapshots in the repository, want the 5 of T, the 2 of S and the 3 without tags", got)
	}
}

// The decision of the policy equals that of restic forget on the same
// snapshots (КL7), here on 150 snapshots over more than a year.
func TestResticРешениеПолитикиСовпадаетСResticForgetНаТехЖеСнимках(t *testing.T) {
	r := newRealRepo(t)
	// restic measures keep_within from the real clock if a snapshot is later
	// than it, so the snapshots all lie in the past.
	real := time.Now().UTC().Truncate(time.Second)
	start := real.Add(-150 * 66 * time.Hour)
	for i := range 150 {
		r.main("maint", "S", start.Add(time.Duration(i)*66*time.Hour))
	}
	snaps, err := r.cli.Snapshots(context.Background(), []string{"sard.source=S"})
	if err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct {
		flags  []string
		policy retention.Policy
	}{
		"keep_daily 7":                {[]string{"--keep-daily", "7"}, retention.Policy{Daily: 7}},
		"daily weekly monthly":        {[]string{"--keep-daily", "7", "--keep-weekly", "4", "--keep-monthly", "6"}, retention.Policy{Daily: 7, Weekly: 4, Monthly: 6}},
		"keep_daily 7 keep_yearly 2":  {[]string{"--keep-daily", "7", "--keep-yearly", "2"}, retention.Policy{Daily: 7, Yearly: 2}},
		"keep_within 72h":             {[]string{"--keep-within", "72h"}, retention.Policy{Within: 72 * time.Hour}},
		"keep_last 3 keep_within 48h": {[]string{"--keep-last", "3", "--keep-within", "48h"}, retention.Policy{Last: 3, Within: 48 * time.Hour}},
	} {
		t.Run(name, func(t *testing.T) {
			want := r.resticForgets(c.flags)
			got := retention.Plan(snaps, retention.Task{Source: "S", Policy: c.policy}, noFloor, real)
			sameSet(t, "forgotten", got.Forget(), want)
			if len(want) == 0 || len(want) == len(snaps) {
				t.Errorf("restic forgets %d of %d: the policy does not discriminate", len(want), len(snaps))
			}
		})
	}
}

// resticForgets are the ids restic forget --dry-run would forget.
func (r *realRepo) resticForgets(flags []string) []string {
	r.t.Helper()
	var dry []struct {
		Remove []struct {
			ID string `json:"id"`
		} `json:"remove"`
	}
	out := r.restic(append([]string{"forget", "--dry-run", "--json", "--group-by", "", "--tag", "sard.source=S"}, flags...)...)
	if err := json.Unmarshal(out, &dry); err != nil || len(dry) != 1 {
		r.t.Fatalf("restic forget --dry-run: %v, %s", err, out)
	}
	var ids []string
	for _, s := range dry[0].Remove {
		ids = append(ids, s.ID)
	}
	return ids
}

func TestResticОтпечатокИГлобальныеОбъектыЗабываютсяВместеСОсновнымСнимком(t *testing.T) {
	r := newRealRepo(t)
	m1, m2, m3 := r.main("maint", "S", day(-2)), r.main("maint", "S", day(-1)), r.main("maint", "S", day(0))
	f1, g2, f3 := r.fingerprint("S", m1, day(-2)), r.globals("S", m2, day(-1)), r.fingerprint("S", m3, day(0))
	out := r.mustForget(noFloor, "S", &agentv1.RetentionPolicy{KeepLast: 1})
	sameSet(t, "forgotten", out.ForgottenSnapshotIds, []string{m1, f1, m2, g2})
	sameSet(t, "remaining", out.RemainingSnapshotIds, []string{m3, f3})
	if out.ListedAt == nil || out.ListedAt.AsTime().Before(now) {
		t.Errorf("listed_at %v", out.ListedAt)
	}
}

func TestResticПределСохраняетПоследние7СнимковПриПолитикеKeepLast1(t *testing.T) {
	r := newRealRepo(t)
	var m []string
	for i := 1; i <= 20; i++ {
		m = append(m, r.main("db1", "S", day(7*(i-20)))) // made by another host
	}
	out := r.mustForget(builtInFloor, "S", &agentv1.RetentionPolicy{KeepLast: 1})
	sameSet(t, "forgotten", out.ForgottenSnapshotIds, m[:13])
	sameSet(t, "remaining", out.RemainingSnapshotIds, m[13:])
	sameSet(t, "kept by floor", out.KeptByFloorSnapshotIds, m[13:19])
}

func TestResticСамаяУзкаяПолитикаОставляетВсёЧтоЗащищаетПредел(t *testing.T) {
	r := newRealRepo(t)
	var keep []string
	for i := range 30 {
		tm := day(i - 29)
		m := r.main("maint", "S", tm)
		f := r.fingerprint("S", m, tm)
		if tm.After(day(0).AddDate(0, 0, -14)) {
			keep = append(keep, m, f)
		}
	}
	r.mustForget(builtInFloor, "S", &agentv1.RetentionPolicy{KeepLast: 1})
	sameSet(t, "remaining", r.ids("S"), keep)
}

func TestResticПолитикаХранитьНольНеУдаляетНиОдногоСнимка(t *testing.T) {
	r := newRealRepo(t)
	for i := range 3 {
		r.main("maint", "S", day(i-3))
	}
	if _, err := r.forget(builtInFloor, "S", &agentv1.RetentionPolicy{}); !errors.Is(err, executor.ErrRejected) {
		t.Errorf("err = %v", err)
	}
	if got := len(r.ids("S")); got != 3 {
		t.Errorf("%d snapshots", got)
	}
}

// holdLock starts a restic check that reads all the data, waits for its lock
// to appear in the repository and returns the process.
func (r *realRepo) holdLock() *exec.Cmd {
	r.t.Helper()
	// Throttled, so that it holds its lock long enough to be stopped.
	cmd := r.command(context.Background(), "check", "--read-data", "--limit-download", "4096", "-q")
	if err := cmd.Start(); err != nil {
		r.t.Fatal(err)
	}
	r.t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	deadline := time.Now().Add(time.Minute)
	for len(strings.Fields(string(r.restic("list", "locks", "--no-lock")))) == 0 {
		if time.Now().After(deadline) {
			r.t.Fatal("no lock appeared")
		}
	}
	return cmd
}

// bulk makes the check last long enough to be stopped while it holds its lock.
func (r *realRepo) bulk() {
	r.t.Helper()
	data := filepath.Join(r.data, "bulk")
	if err := os.MkdirAll(data, 0o755); err != nil {
		r.t.Fatal(err)
	}
	rng := rand.NewChaCha8([32]byte{7}) // random bytes neither compress nor deduplicate
	for i := range 3 {
		block := make([]byte, 8<<20)
		_, _ = rng.Read(block)
		if err := os.WriteFile(filepath.Join(data, fmt.Sprintf("b%d", i)), block, 0o644); err != nil {
			r.t.Fatal(err)
		}
	}
	r.seed("maint", day(-100))
}

func TestResticУстаревшаяБлокировкаСнимаетсяИForgetВыполняется(t *testing.T) {
	r := newRealRepo(t)
	r.bulk()
	for i := range 4 {
		r.main("maint", "S", day(i-4))
	}
	check := r.holdLock()
	_ = check.Process.Signal(syscall.SIGKILL) // the lock of a process that is gone
	_ = check.Wait()
	out, err := r.forget(noFloor, "S", &agentv1.RetentionPolicy{KeepLast: 1})
	if err != nil || len(out.ForgottenSnapshotIds) != 3 {
		t.Fatalf("err = %v, output %v", err, out)
	}
}

func TestResticБлокировкаОсвободившаясяВоВремяОжиданияНеМешаетForget(t *testing.T) {
	r := newRealRepo(t)
	r.bulk()
	for i := range 4 {
		r.main("maint", "S", day(i-4))
	}
	check := r.holdLock()
	if err := check.Process.Signal(syscall.SIGSTOP); err != nil {
		t.Fatal(err)
	}
	released := time.AfterFunc(5*time.Second, func() { _ = check.Process.Signal(syscall.SIGCONT) })
	defer released.Stop()
	start := time.Now()
	out, err := r.forget(noFloor, "S", &agentv1.RetentionPolicy{KeepLast: 1})
	if err != nil || len(out.ForgottenSnapshotIds) != 3 || time.Since(start) < 4*time.Second {
		t.Fatalf("err = %v, output %v, waited %v", err, out, time.Since(start))
	}
}

// forgetTogether runs a step for each source at once, keeping the last snapshot.
func (r *realRepo) forgetTogether(sources ...string) ([]*agentv1.ForgetOutput, []error) {
	var wg sync.WaitGroup
	outs := make([]*agentv1.ForgetOutput, len(sources))
	errs := make([]error, len(sources))
	for i, source := range sources {
		wg.Add(1)
		go func() {
			defer wg.Done()
			outs[i], errs[i] = r.forget(noFloor, source, &agentv1.RetentionPolicy{KeepLast: 1})
		}()
	}
	wg.Wait()
	return outs, errs
}

func TestResticДваШагаFORGETРазныхИсточниковОдногоРепозиторияВыполняютсяОба(t *testing.T) {
	r := newRealRepo(t)
	for i := range 10 {
		r.main("maint", "S", day(i-10))
		r.main("maint", "T", day(i-10))
	}
	outs, errs := r.forgetTogether("S", "T")
	if errs[0] != nil || errs[1] != nil {
		t.Fatalf("errors: %v", errs)
	}
	if len(r.ids("S")) != 1 || len(r.ids("T")) != 1 {
		t.Errorf("%d snapshots of S and %d of T remain", len(r.ids("S")), len(r.ids("T")))
	}
	for _, id := range outs[0].ForgottenSnapshotIds {
		if slices.Contains(outs[1].ForgottenSnapshotIds, id) {
			t.Errorf("%s is forgotten by both steps", id)
		}
	}
}
