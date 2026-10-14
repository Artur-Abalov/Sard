// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package hostsetup_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
)

// R2a (ВК13): the staging directory reaches the destination with one rename.

func TestMoveMakesTheDirectoryAppearWithItsContentUnderAMissingName(t *testing.T) {
	from, to := t.TempDir(), t.TempDir()
	ok(t, os.MkdirAll(filepath.Join(from, "stage/srv"), 0o700))
	ok(t, os.WriteFile(filepath.Join(from, "stage/srv/f"), []byte("F"), 0o600))
	ok(t, heldDir(t, from).Move("stage", heldDir(t, to), "dest"))
	if data, err := os.ReadFile(filepath.Join(to, "dest/srv/f")); err != nil || string(data) != "F" {
		t.Fatalf("f: %q, %v", data, err)
	}
	if _, err := os.Lstat(filepath.Join(from, "stage")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("stage is still there: %v", err)
	}
}

func TestMoveReplacesAnEmptyDirectory(t *testing.T) {
	from, to := t.TempDir(), t.TempDir()
	ok(t, os.MkdirAll(filepath.Join(from, "stage"), 0o700))
	ok(t, os.WriteFile(filepath.Join(from, "stage/f"), []byte("F"), 0o600))
	ok(t, os.MkdirAll(filepath.Join(to, "dest"), 0o700))
	ok(t, heldDir(t, from).Move("stage", heldDir(t, to), "dest"))
	if data, err := os.ReadFile(filepath.Join(to, "dest/f")); err != nil || string(data) != "F" {
		t.Fatalf("f: %q, %v", data, err)
	}
}

func TestMoveNeverReplacesANonEmptyDirectory(t *testing.T) {
	from, to := t.TempDir(), t.TempDir()
	ok(t, os.MkdirAll(filepath.Join(from, "stage"), 0o700))
	ok(t, os.MkdirAll(filepath.Join(to, "dest"), 0o700))
	ok(t, os.WriteFile(filepath.Join(to, "dest/keep"), []byte("K"), 0o600))
	err := heldDir(t, from).Move("stage", heldDir(t, to), "dest")
	if !errors.Is(err, syscall.ENOTEMPTY) {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(from, "stage")); err != nil {
		t.Errorf("the staging directory moved: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(to, "dest/keep")); err != nil || string(data) != "K" {
		t.Errorf("keep: %q, %v", data, err)
	}
}

func TestSameDeviceTellsAMountPointFromItsParent(t *testing.T) {
	stat := func(p string) fs.FileInfo {
		i, err := os.Stat(p)
		ok(t, err)
		return i
	}
	tmp := t.TempDir()
	if !hostsetup.SameDevice(stat(tmp), stat(os.TempDir())) {
		t.Error("two directories of one file system are on two devices")
	}
	if hostsetup.SameDevice(stat(tmp), stat("/proc")) {
		t.Error("/proc is on the device of the temporary directory")
	}
}

func TestSameDeviceOfAnInfoWithoutADeviceIsTrue(t *testing.T) {
	if !hostsetup.SameDevice(described{}, described{}) {
		t.Error("a fake that reports no device is a different device")
	}
}
