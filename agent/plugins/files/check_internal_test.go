// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package files

import (
	"context"
	"io/fs"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

// slowFS answers Lstat only when the gate opens, and counts the calls.
type slowFS struct {
	gate   chan struct{}
	lstats atomic.Int32
}

type socketInfo struct{ fs.FileInfo }

func (socketInfo) Mode() fs.FileMode { return fs.ModeSocket }
func (socketInfo) IsDir() bool       { return false }

func (f *slowFS) Lstat(string) (fs.FileInfo, error) {
	f.lstats.Add(1)
	<-f.gate
	return socketInfo{}, nil
}
func (*slowFS) Readlink(string) (string, error) { return "", nil }
func (*slowFS) Open(string) (File, error)       { return nil, nil }

// A step that is cancelled stops the walk over the paths: the paths after
// the one being looked at are not touched.
func TestCheckAllStopsLookingAtPathsOnceTheStepIsCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	fsys := &slowFS{gate: make(chan struct{})}
	close(fsys.gate)
	if got := checkAll(ctx, fsys, []string{"/a", "/b"}); got != nil || fsys.lstats.Load() != 0 {
		t.Errorf("failures %v, %d paths looked at", got, fsys.lstats.Load())
	}
}

// The check of a cancelled step does not outlive the file system's answer:
// its goroutine ends when the answer comes, nobody has to receive it.
func TestTheCheckEndsAfterACancelledStepWhenTheFileSystemAnswers(t *testing.T) {
	before := runtime.NumGoroutine()
	ctx, cancel := context.WithCancelCause(context.Background())
	fsys := &slowFS{gate: make(chan struct{})}
	done := make(chan error, 1)
	go func() { done <- Plugin{FS: fsys}.checkPaths(ctx, []string{"/slow"}) }()
	for fsys.lstats.Load() == 0 {
		time.Sleep(time.Millisecond)
	}
	cancel(context.Canceled)
	if err := <-done; err != context.Canceled {
		t.Fatalf("err = %v", err)
	}
	close(fsys.gate)
	deadline := time.Now().Add(5 * time.Second)
	for runtime.NumGoroutine() > before && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if n := runtime.NumGoroutine(); n > before {
		t.Errorf("%d goroutines, %d before: the check is stuck", n, before)
	}
}

// Dump repeats the parse of the config: a broken one is a ConfigError, not
// a dump of nothing.
func TestDumpRejectsAConfigThatDoesNotParse(t *testing.T) {
	_, err := Plugin{}.Dump(context.Background(), nil, sdk.Config(`{"paths": ["/a", "/a/b"]}`))
	if _, ok := err.(*sdk.ConfigError); !ok {
		t.Errorf("err = %v, want *sdk.ConfigError", err)
	}
}
