// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package postgresql_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/signal"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/plugins/postgresql"
)

// helperEnv selects what TestHelperProcess does when the test binary is
// started as a child process by ProcessRunner.
const helperEnv = "SARD_PG_HELPER"

var helperModes = map[string]func(){
	"output": func() {
		fmt.Print("data")
		fmt.Fprint(os.Stderr, "first\nsecond")
		os.Exit(3)
	},
	"env": func() {
		for _, kv := range os.Environ() {
			fmt.Println(kv)
		}
	},
	"selfkill": func() { _ = syscall.Kill(os.Getpid(), syscall.SIGKILL) },
	"sleep":    func() { time.Sleep(time.Minute) },
	"stubborn": func() {
		signal.Ignore(syscall.SIGTERM)
		fmt.Println("ready")
		time.Sleep(time.Minute)
	},
}

// TestHelperProcess is not a test: it is the fake tool.
func TestHelperProcess(t *testing.T) {
	if run, ok := helperModes[os.Getenv(helperEnv)]; ok {
		run()
		os.Exit(0)
	}
}

// helper is the command that starts this test binary as the fake tool. A
// binary built for coverage warns on stderr at exit unless it has a place to
// write its data.
func helper(t *testing.T, mode string, extra ...string) postgresql.Cmd {
	t.Helper()
	env := append([]string{helperEnv + "=" + mode, "GOCOVERDIR=" + t.TempDir()}, extra...)
	return postgresql.Cmd{Path: os.Args[0], Args: []string{"-test.run=^TestHelperProcess$"}, Env: env}
}

// lines collects the lines of stderr.
type lines struct {
	mu  sync.Mutex
	got []string
}

func (l *lines) add(s string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.got = append(l.got, s)
}

func (l *lines) list() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.got)
}

func TestProcessRunnerReturnsTheExitCodeAndTheOutput(t *testing.T) {
	var out bytes.Buffer
	var err lines
	c := helper(t, "output")
	c.Stdout, c.Stderr = &out, err.add
	code, runErr := postgresql.ProcessRunner{}.Run(context.Background(), c)
	if code != 3 || runErr != nil || out.String() != "data" || !slices.Equal(err.list(), []string{"first", "second"}) {
		t.Errorf("code %d, err %v, stdout %q, stderr %q", code, runErr, out.String(), err.list())
	}
}

func TestProcessRunnerGivesTheProcessOnlyTheEnvironmentOfTheCommand(t *testing.T) {
	t.Setenv("SARD_PG_LEAK", "yes")
	var out bytes.Buffer
	c := helper(t, "env", "ONLY=this")
	c.Stdout = &out
	if code, err := (postgresql.ProcessRunner{}).Run(context.Background(), c); code != 0 || err != nil {
		t.Fatalf("code %d, err %v", code, err)
	}
	if strings.Contains(out.String(), "SARD_PG_LEAK") || !strings.Contains(out.String(), "ONLY=this") {
		t.Errorf("environment = %q", out.String())
	}
}

func TestProcessRunnerReportsAProcessThatDoesNotStart(t *testing.T) {
	code, err := postgresql.ProcessRunner{}.Run(context.Background(), postgresql.Cmd{Path: "/nonexistent/tool"})
	if code != -1 || err == nil {
		t.Errorf("code %d, err %v", code, err)
	}
}

func TestProcessRunnerReportsAProcessKilledBySignal(t *testing.T) {
	code, err := postgresql.ProcessRunner{}.Run(context.Background(), helper(t, "selfkill"))
	if code != -1 || err == nil || err.Error() != "signal: killed" {
		t.Errorf("code %d, err %v", code, err)
	}
}

func TestProcessRunnerStopsAProcessWithSigtermWhenTheContextIsDone(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	code, err := postgresql.ProcessRunner{}.Run(ctx, helper(t, "sleep"))
	if code != -1 || err == nil || err.Error() != "signal: terminated" || time.Since(start) > 5*time.Second {
		t.Errorf("code %d, err %v after %v", code, err, time.Since(start))
	}
}

func TestProcessRunnerKillsAProcessThatIgnoresSigtermAfterTheGrace(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var out lines
	c := helper(t, "stubborn")
	c.Stderr = out.add
	var ready sync.WaitGroup
	ready.Add(1)
	c.Stdout = writerFunc(func(p []byte) (int, error) {
		if strings.Contains(string(p), "ready") {
			ready.Done()
		}
		return len(p), nil
	})
	done := make(chan error, 1)
	go func() {
		_, err := postgresql.ProcessRunner{Grace: 100 * time.Millisecond}.Run(ctx, c)
		done <- err
	}()
	ready.Wait()
	cancel()
	select {
	case err := <-done:
		if err == nil || err.Error() != "signal: killed" {
			t.Errorf("err = %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the process was not killed")
	}
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }
