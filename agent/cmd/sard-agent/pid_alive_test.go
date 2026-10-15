// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"os"
	"os/exec"
	"testing"
)

// "процесс жив" of host-check.feature, with the real kernel.

func TestAProcessIsAliveWhileItRuns(t *testing.T) {
	if !processAlive(os.Getpid()) {
		t.Fatal("this process is not alive")
	}
	cmd := exec.Command("sleep", "30")
	ok(t, cmd.Start())
	alive := processAlive(cmd.Process.Pid)
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	if !alive || processAlive(cmd.Process.Pid) {
		t.Fatalf("alive while running: %v; after it was reaped: %v", alive, processAlive(cmd.Process.Pid))
	}
}

func TestNoPidIsNotAProcess(t *testing.T) {
	for _, pid := range []int{0, -1, 1 << 30} {
		if processAlive(pid) {
			t.Errorf("pid %d is alive", pid)
		}
	}
}

func TestTheFirstProcessOfTheHostIsAliveEvenForAnotherUser(t *testing.T) {
	if !processAlive(1) {
		t.Fatal("process 1 is not alive")
	}
}
