// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package hostsetup_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
)

func TestDropInGrantsWriteAccessToTheRepositoryPath(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sard-agent.service.d")
	rfs := newRecordingFS()
	d := hostsetup.DropIn{FS: rfs, Dir: dir}
	if err := d.Write("extra", "/srv/repo extra"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "sard-repo-extra.conf")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "[Service]\nReadWritePaths=/srv/repo\\x20extra\n") {
		t.Fatalf("content %q", data)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o644 {
		t.Fatalf("mode %v", info.Mode())
	}
	if o, _ := rfs.ownerOf(path); o != (owner{0, 0}) {
		t.Fatalf("owner %+v", o)
	}
	if o, _ := rfs.ownerOf(dir); o != (owner{0, 0}) {
		t.Fatalf("directory owner %+v", o)
	}
	if info, _ := os.Stat(dir); info.Mode().Perm() != 0o755 {
		t.Fatalf("directory mode %v", info.Mode())
	}
}

func TestDropInRemoveReportsWhetherThereWasOne(t *testing.T) {
	dir := t.TempDir()
	d := hostsetup.DropIn{FS: newRecordingFS(), Dir: dir}
	if removed, err := d.Remove("extra"); err != nil || removed {
		t.Fatalf("removed %v, err %v", removed, err)
	}
	if err := d.Write("extra", "/srv/x"); err != nil {
		t.Fatal(err)
	}
	if removed, err := d.Remove("extra"); err != nil || !removed {
		t.Fatalf("removed %v, err %v", removed, err)
	}
	if _, err := os.Stat(d.Path("extra")); !os.IsNotExist(err) {
		t.Fatalf("the file stays: %v", err)
	}
}

func TestDropInWriteFailuresNameThePath(t *testing.T) {
	rfs := newRecordingFS()
	rfs.failOn = "mkdir"
	dir := filepath.Join(t.TempDir(), "d")
	err := hostsetup.DropIn{FS: rfs, Dir: dir}.Write("x", "/srv/x")
	if err == nil || !strings.Contains(err.Error(), dir) || !strings.Contains(err.Error(), "create directory") {
		t.Fatalf("err = %v", err)
	}
}

func TestDropInEscapesWhatSystemdWouldInterpret(t *testing.T) {
	dir := t.TempDir()
	d := hostsetup.DropIn{FS: newRecordingFS(), Dir: dir}
	ok(t, d.Write("x", `/srv/a b%c\d`))
	data, err := os.ReadFile(d.Path("x"))
	ok(t, err)
	if !strings.Contains(string(data), "ReadWritePaths=/srv/a\\x20b%%c\\\\d\n") {
		t.Fatalf("drop-in %q", data)
	}
}
