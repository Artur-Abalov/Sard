// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package restic_test

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/restic"
)

// helperEnv selects what TestHelperProcess does when the test binary is
// started as a child process by ProcessExecutor.
const helperEnv = "SARD_RESTIC_HELPER"

// helperModes are the behaviours of the fake restic process.
var helperModes = map[string]func(){
	"env": func() {
		for _, kv := range os.Environ() {
			fmt.Println(kv)
		}
	},
	"output": func() {
		fmt.Print("first\nsecond\r\nz") // the last line has no line ending
		fmt.Fprint(os.Stderr, "\rwarn")
		os.Exit(3)
	},
	"cat": func() {
		if _, err := io.Copy(os.Stdout, os.Stdin); err != nil {
			os.Exit(2)
		}
	},
	"tree":  startStubbornChild,
	"leak":  leaveChildBehind,
	"sleep": func() { time.Sleep(time.Minute) },
	"stubborn": func() {
		signal.Ignore(syscall.SIGTERM)
		fmt.Println("ready")
		time.Sleep(time.Minute)
	},
}

// TestHelperProcess is not a test: it is the fake restic process.
func TestHelperProcess(t *testing.T) {
	if run, ok := helperModes[os.Getenv(helperEnv)]; ok {
		run()
		os.Exit(0)
	}
}

// startStubbornChild starts a child that ignores SIGTERM, waits until it
// does, reports its pid and sleeps.
func startStubbornChild() {
	child := exec.Command(os.Args[0], "-test.run=^TestHelperProcess$")
	child.Env = []string{helperEnv + "=stubborn"} // only SIGKILL stops it
	ready, err := child.StdoutPipe()
	if err != nil || child.Start() != nil {
		os.Exit(2)
	}
	if _, err := bufio.NewReader(ready).ReadString('\n'); err != nil {
		os.Exit(2)
	}
	fmt.Printf("child %d\n", child.Process.Pid)
	time.Sleep(time.Minute)
}

// leaveChildBehind starts a sleeping child that shares our stdout, reports
// its pid and exits.
func leaveChildBehind() {
	child := exec.Command(os.Args[0], "-test.run=^TestHelperProcess$")
	child.Env = []string{helperEnv + "=sleep"}
	child.Stdout = os.Stdout
	if child.Start() != nil {
		os.Exit(2)
	}
	fmt.Printf("child %d\n", child.Process.Pid)
}

// helper runs the fake restic process. GOCOVERDIR keeps a coverage build
// of the test binary from warning on stderr in the child.
func helper(t *testing.T, mode string, env ...string) restic.Command {
	return restic.Command{
		Path: os.Args[0],
		Args: []string{"-test.run=^TestHelperProcess$"},
		Env:  append([]string{helperEnv + "=" + mode, "GOCOVERDIR=" + t.TempDir()}, env...),
	}
}

// lines collects callback lines; the executor calls it from its own goroutine.
type lines struct {
	mu  sync.Mutex
	got []string
}

func (l *lines) add(line []byte) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.got = append(l.got, string(line))
}

func (l *lines) all() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.got)
}

func TestProcessExecutorPassesOnlyTheGivenEnvironment(t *testing.T) {
	t.Setenv("RESTIC_PASSWORD", "leaked")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "leaked")
	var out lines
	cmd := helper(t, "env", "A=1")
	cmd.Stdout = out.add
	code, err := restic.ProcessExecutor{}.Run(context.Background(), cmd)
	want := cmd.Env
	if code != 0 || err != nil || !slices.Equal(out.all(), want) {
		t.Fatalf("code = %d, err = %v, env = %q, want %q", code, err, out.all(), want)
	}
}

func TestProcessExecutorSplitsOutputIntoLinesAndReportsTheExitCode(t *testing.T) {
	var out, errOut lines
	cmd := helper(t, "output")
	cmd.Stdout, cmd.Stderr = out.add, errOut.add
	code, err := restic.ProcessExecutor{}.Run(context.Background(), cmd)
	if code != 3 || err != nil {
		t.Fatalf("code = %d, err = %v", code, err)
	}
	if want := []string{"first", "second", "z"}; !slices.Equal(out.all(), want) {
		t.Errorf("stdout = %q, want %q", out.all(), want)
	}
	if want := []string{"warn"}; !slices.Equal(errOut.all(), want) {
		t.Errorf("stderr = %q, want %q", errOut.all(), want)
	}
}

func TestProcessExecutorWithoutCallbacksDiscardsOutput(t *testing.T) {
	code, err := restic.ProcessExecutor{}.Run(context.Background(), helper(t, "output"))
	if code != 3 || err != nil {
		t.Fatalf("code = %d, err = %v", code, err)
	}
}

func TestProcessExecutorReportsAStartFailure(t *testing.T) {
	code, err := restic.ProcessExecutor{}.Run(context.Background(), restic.Command{Path: "/nonexistent/restic"})
	if code != -1 || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("code = %d, err = %v", code, err)
	}
}

// Cancelling the context stops the process and every process it started.
func TestProcessExecutorCancellationKillsTheProcessGroup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var child int
	cmd := helper(t, "tree")
	cmd.Stdout = func(line []byte) {
		if pid, ok := strings.CutPrefix(string(line), "child "); ok {
			child, _ = strconv.Atoi(pid)
			cancel()
		}
	}
	start := time.Now()
	code, err := restic.ProcessExecutor{Grace: 5 * time.Second}.Run(ctx, cmd)
	if code != -1 || err != nil || time.Since(start) > 30*time.Second {
		t.Fatalf("code = %d, err = %v after %v", code, err, time.Since(start))
	}
	if child == 0 {
		t.Fatal("helper did not report its child")
	}
	waitDead(t, child)
}

// A process that ignores SIGTERM is killed once the grace period is over.
func TestProcessExecutorKillsAProcessThatIgnoresSIGTERM(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := helper(t, "stubborn")
	cmd.Stdout = func([]byte) { cancel() }
	grace := 300 * time.Millisecond
	start := time.Now()
	code, err := restic.ProcessExecutor{Grace: grace}.Run(ctx, cmd)
	if elapsed := time.Since(start); code != -1 || err != nil || elapsed < grace {
		t.Fatalf("code = %d, err = %v after %v", code, err, elapsed)
	}
}

// A child left behind by a process that exited is killed with its group;
// its hold on stdout does not keep Run waiting past the grace period.
func TestProcessExecutorKillsWhatTheProcessLeftBehind(t *testing.T) {
	var child int
	cmd := helper(t, "leak")
	cmd.Stdout = func(line []byte) {
		if pid, ok := strings.CutPrefix(string(line), "child "); ok {
			child, _ = strconv.Atoi(pid)
		}
	}
	start := time.Now()
	code, err := restic.ProcessExecutor{Grace: 300 * time.Millisecond}.Run(context.Background(), cmd)
	if code != 0 || err != nil || time.Since(start) > 30*time.Second || child == 0 {
		t.Fatalf("code = %d, err = %v, child = %d after %v", code, err, child, time.Since(start))
	}
	waitDead(t, child)
}

func TestZeroGraceMeansTheDefault(t *testing.T) {
	if got := (restic.ProcessExecutor{}).GraceForTest(); got != 10*time.Second {
		t.Errorf("zero grace = %v", got)
	}
	if got := (restic.ProcessExecutor{Grace: time.Second}).GraceForTest(); got != time.Second {
		t.Errorf("grace = %v", got)
	}
}

func TestSignalGroupErrors(t *testing.T) {
	// No process group has this id: pids are below 2^22 on Linux.
	if err := restic.SignalGroup(1<<30, syscall.SIGKILL); !errors.Is(err, os.ErrProcessDone) {
		t.Errorf("missing group: %v", err)
	}
	if err := restic.SignalGroup(syscall.Getpgrp(), syscall.Signal(999)); !errors.Is(err, syscall.EINVAL) {
		t.Errorf("invalid signal: %v", err)
	}
}

// waitDead fails unless pid is gone or a zombie within a few seconds.
func waitDead(t *testing.T, pid int) {
	t.Helper()
	for range 100 {
		if !alive(pid) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("process %d still alive", pid)
}

func alive(pid int) bool {
	stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return false
	}
	// "pid (comm) S ...": the state follows the closing parenthesis.
	rest := string(stat[strings.LastIndexByte(string(stat), ')')+1:])
	return !strings.HasPrefix(strings.TrimSpace(rest), "Z")
}

func TestProcessExecutorFeedsStdin(t *testing.T) {
	var out lines
	cmd := helper(t, "cat")
	cmd.Stdin = strings.NewReader("dump line 1\ndump line 2\n")
	cmd.Stdout = out.add
	code, err := restic.ProcessExecutor{}.Run(context.Background(), cmd)
	if want := []string{"dump line 1", "dump line 2"}; code != 0 || err != nil || !slices.Equal(out.all(), want) {
		t.Fatalf("code = %d, err = %v, stdout = %q", code, err, out.all())
	}
}

func TestProcessExecutorWithoutStdinGivesTheProcessAnEmptyOne(t *testing.T) {
	var out lines
	cmd := helper(t, "cat")
	cmd.Stdout = out.add
	if code, err := (restic.ProcessExecutor{}).Run(context.Background(), cmd); code != 0 || err != nil || len(out.all()) != 0 {
		t.Fatalf("code = %d, err = %v, stdout = %q", code, err, out.all())
	}
}
