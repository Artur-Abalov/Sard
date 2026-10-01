// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package repoinit_test

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"testing/iotest"

	"github.com/Artur-Abalov/sard/agent/internal/repoinit"
)

func TestNewPasswordIs32RandomBytesAsBase64urlWithoutPadding(t *testing.T) {
	raw := make([]byte, 40)
	for i := range raw {
		raw[i] = byte(i)
	}
	random := bytes.NewReader(raw)
	got, err := repoinit.NewPassword(random)
	if want := "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8"; err != nil || got != want {
		t.Fatalf("password = %q, err = %v, want %q", got, err, want)
	}
	if random.Len() != 8 {
		t.Errorf("%d bytes of the generator were left, want exactly 32 read", random.Len())
	}
}

func TestNewPasswordFailsWhenTheGeneratorFails(t *testing.T) {
	for name, r := range map[string]io.Reader{
		"error": iotest.ErrReader(errors.New("no entropy")),
		"short": bytes.NewReader(make([]byte, 31)),
	} {
		if got, err := repoinit.NewPassword(r); err == nil || got != "" {
			t.Errorf("%s: password = %q, err = %v", name, got, err)
		}
	}
}

func TestWriteNewCreatesAnOwnerOnlyFileAndNeverOverwrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pass")
	if err := repoinit.WriteNew(path, []byte("secret\n")); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("%v, %v", info, err)
	}
	if err := repoinit.WriteNew(path, []byte("other\n")); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("second write: %v", err)
	}
	if data, _ := os.ReadFile(path); string(data) != "secret\n" {
		t.Fatalf("content = %q", data)
	}
}

func TestWriteNewDoesNotCreateDirectories(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "absent")
	if err := repoinit.WriteNew(filepath.Join(dir, "pass"), []byte("x")); !errors.Is(err, syscall.ENOENT) {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(dir); err == nil {
		t.Fatal("the directory was created")
	}
}
