// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

//go:build integration

package retention_test

import (
	"context"
	"crypto/rand"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/retention"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

// The @restic scenarios of docs/specs/agent/retention-prune.feature: the
// pinned restic on a repository in a temporary directory.

// distinct makes a snapshot of source S whose content differs from every
// other (10 MiB of random bytes) and returns its id.
func (r *realRepo) distinct(n int) string {
	r.t.Helper()
	data := make([]byte, 10<<20)
	_, _ = rand.Read(data)
	if err := os.WriteFile(filepath.Join(r.data, "f"), data, 0o600); err != nil {
		r.t.Fatal(err)
	}
	return r.main("maint", "S", day(n))
}

func (r *realRepo) prune(ctx context.Context) (*agentv1.PruneOutput, error) {
	h := retention.NewPrune(retention.PruneOptions{
		Stores: func(string, string, io.Writer) (retention.PruneStore, bool) { return r.cli, true },
	})
	step := &agentv1.RunStep{CommandId: "p1", Action: agentv1.Action_ACTION_PRUNE, RepositoryName: "it", Prune: &agentv1.PruneTask{}}
	res, err := h.Run(ctx, step, &fakeReporter{})
	return res.GetPrune(), err
}

// dataSize is the size of the files of the data directory of the repository.
func (r *realRepo) dataSize() int64 {
	var total int64
	_ = filepath.WalkDir(filepath.Join(r.dir, "data"), func(_ string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if info, err := d.Info(); err == nil {
				total += info.Size()
			}
		}
		return nil
	})
	return total
}

func TestResticPRUNEОсвобождаетМестоЗабытыхСнимковИНеМеняетСписокСнимков(t *testing.T) {
	r := newRealRepo(t)
	m1, m2, m3 := r.distinct(-3), r.distinct(-2), r.distinct(-1)
	r.restic("forget", m1)
	before := r.dataSize()
	out, err := r.prune(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if out.FreedBytes == nil || *out.FreedBytes == 0 || out.RepositorySizeBytes == nil {
		t.Errorf("output = %v", out)
	}
	if after := r.dataSize(); after >= before {
		t.Errorf("data size %d, was %d", after, before)
	}
	sameSet(t, "snapshots", r.ids(""), []string{m2, m3})
}

func TestResticPRUNEНаРепозиторииБезЛишнихДанныхНичегоНеУдаляет(t *testing.T) {
	r := newRealRepo(t)
	m1, m2 := r.distinct(-2), r.distinct(-1)
	if _, err := r.prune(context.Background()); err != nil {
		t.Fatal(err)
	}
	sameSet(t, "snapshots", r.ids(""), []string{m1, m2})
	r.restic("check", "--read-data")
}

func TestResticPRUNEПустогоРепозиторияЗавершаетсяУспехом(t *testing.T) {
	r := newRealRepo(t)
	if out, err := r.prune(context.Background()); err != nil || out == nil {
		t.Errorf("output %v, err %v", out, err)
	}
}

func TestResticБлокировкаОсвободившаясяВоВремяОжиданияНеМешаетPRUNE(t *testing.T) {
	r := newRealRepo(t)
	r.distinct(-1)
	cmd := r.command(context.Background(), "check", "--read-data", "--limit-download", "4096", "-q")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	for deadline := time.Now().Add(time.Minute); ; time.Sleep(50 * time.Millisecond) {
		locks, err := r.cli.ListLocks(context.Background())
		if err == nil && len(locks) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no lock appeared")
		}
	}
	time.AfterFunc(10*time.Second, func() { _ = cmd.Process.Kill() })
	if _, err := r.prune(context.Background()); err != nil {
		t.Errorf("prune: %v", err)
	}
}
