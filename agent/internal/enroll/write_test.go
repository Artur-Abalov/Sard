// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package enroll_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/enroll"
)

func threeFiles(t *testing.T) enroll.Files {
	t.Helper()
	dir := t.TempDir()
	return enroll.Files{
		KeyFile:  filepath.Join(dir, "tls.key"),
		CertFile: filepath.Join(dir, "tls.crt"),
		CAFile:   filepath.Join(dir, "ca.crt"),
	}
}

func TestCheckWritableAcceptsExistingWritableDirectories(t *testing.T) {
	if err := enroll.CheckWritable(threeFiles(t)); err != nil {
		t.Fatalf("CheckWritable: %v", err)
	}
}

func TestCheckWritableRejectsAMissingDirectoryWithoutCreatingIt(t *testing.T) {
	base := t.TempDir()
	missing := filepath.Join(base, "does-not-exist")
	files := enroll.Files{KeyFile: filepath.Join(missing, "tls.key"), CertFile: filepath.Join(base, "tls.crt"), CAFile: filepath.Join(base, "ca.crt")}
	err := enroll.CheckWritable(files)
	var eerr *enroll.Error
	if !errors.As(err, &eerr) || eerr.Class != enroll.ClassWrite {
		t.Fatalf("err = %v, want a ClassWrite *enroll.Error", err)
	}
	if _, statErr := os.Stat(missing); !os.IsNotExist(statErr) {
		t.Fatal("CheckWritable must not create the missing directory")
	}
}

func TestCheckWritableRejectsADirectoryThatIsActuallyAFile(t *testing.T) {
	base := t.TempDir()
	notADir := filepath.Join(base, "not-a-dir")
	if err := os.WriteFile(notADir, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	files := enroll.Files{KeyFile: filepath.Join(base, "tls.key"), CertFile: filepath.Join(notADir, "tls.crt"), CAFile: filepath.Join(base, "ca.crt")}
	err := enroll.CheckWritable(files)
	var eerr *enroll.Error
	if !errors.As(err, &eerr) || eerr.Class != enroll.ClassWrite {
		t.Fatalf("err = %v, want a ClassWrite *enroll.Error", err)
	}
}

func TestWriteIdentityWritesAllThreeFilesWithTheirModesRegardlessOfUmask(t *testing.T) {
	old := setUmask(0o000)
	defer setUmask(old)
	files := threeFiles(t)
	if err := enroll.WriteIdentity(files, []byte("key"), []byte("cert"), []byte("ca")); err != nil {
		t.Fatalf("WriteIdentity: %v", err)
	}
	requireContents(t, files.KeyFile, "key")
	requireContents(t, files.CertFile, "cert")
	requireContents(t, files.CAFile, "ca")
	requireMode(t, files.KeyFile, 0o600)
	requireMode(t, files.CertFile, 0o644)
	requireMode(t, files.CAFile, 0o644)
}

func TestWriteIdentityReplacesAllThreeFilesTogether(t *testing.T) {
	files := threeFiles(t)
	if err := enroll.WriteIdentity(files, []byte("key1"), []byte("cert1"), []byte("ca1")); err != nil {
		t.Fatalf("WriteIdentity: %v", err)
	}
	if err := enroll.WriteIdentity(files, []byte("key2"), []byte("cert2"), []byte("ca2")); err != nil {
		t.Fatalf("WriteIdentity: %v", err)
	}
	requireContents(t, files.KeyFile, "key2")
	requireContents(t, files.CertFile, "cert2")
	requireContents(t, files.CAFile, "ca2")
}

// makeUnwritable turns path (an existing directory) into a plain file so
// writes into it fail even for root (no permission bit stops root, but
// ENOTDIR does).
func makeUnwritable(t *testing.T, dir string) {
	t.Helper()
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir, nil, 0o600); err != nil {
		t.Fatal(err)
	}
}

// F4: a target path that already exists as something other than a regular
// file (here: a non-empty directory sitting where ca.crt should go) must
// fail before any rename, and must not leave the other two files or any
// .sard-enroll-* temporary behind.
func TestWriteIdentityRefusesATargetThatIsANonEmptyDirectory(t *testing.T) {
	for _, broken := range []string{"key", "cert", "ca"} {
		t.Run(broken, func(t *testing.T) {
			checkWriteIdentityRefusesANonEmptyDirectoryTarget(t, broken)
		})
	}
}

func checkWriteIdentityRefusesANonEmptyDirectoryTarget(t *testing.T, broken string) {
	t.Helper()
	dir := t.TempDir()
	files := enroll.Files{
		KeyFile:  filepath.Join(dir, "tls.key"),
		CertFile: filepath.Join(dir, "tls.crt"),
		CAFile:   filepath.Join(dir, "ca.crt"),
	}
	targets := map[string]string{"key": files.KeyFile, "cert": files.CertFile, "ca": files.CAFile}
	makeNonEmptyDirectory(t, targets[broken])

	err := enroll.WriteIdentity(files, []byte("key"), []byte("cert"), []byte("ca"))
	var eerr *enroll.Error
	if !errors.As(err, &eerr) || eerr.Class != enroll.ClassWrite {
		t.Fatalf("err = %v, want a ClassWrite *enroll.Error", err)
	}
	requireOnlyTheBrokenTargetExists(t, targets, broken)
	requireNoLeftoverTemps(t, dir)
}

func makeNonEmptyDirectory(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "not-empty"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func requireOnlyTheBrokenTargetExists(t *testing.T, targets map[string]string, broken string) {
	t.Helper()
	for name, path := range targets {
		if name == broken {
			continue
		}
		if _, statErr := os.Stat(path); statErr == nil {
			t.Errorf("%s was written even though %s is broken", name, broken)
		}
	}
}

func requireNoLeftoverTemps(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".sard-enroll-") {
			t.Errorf("leftover temporary file %s", e.Name())
		}
	}
}

// F4 follow-up: handleCommitFailure's rollback branches, exercised via a
// deterministic rename failure (SetRenameForTest) instead of racing the
// filesystem to fail a specific rename after staging already succeeded.

// First enrollment (nothing existed before): a failure renaming the 2nd
// file in commit order (CA, key, cert — key is 2nd) must undo the CA that
// already landed, leave no temporaries, and leave no file at all.
func TestWriteIdentityFirstEnrollmentRemovesTheAlreadyCommittedCAWhenKeyFailsToRename(t *testing.T) {
	files := threeFiles(t)
	restore := enroll.SetRenameForTest(failRenameOnto(t, files.KeyFile))
	defer restore()

	err := enroll.WriteIdentity(files, []byte("key"), []byte("cert"), []byte("ca"))
	requireClassWrite(t, err)
	requireNoneExist(t, files)
	requireNoLeftoverTemps(t, filepath.Dir(files.KeyFile))
}

// Same, but the 3rd file in commit order (cert) fails: both CA and key,
// already committed, must be undone.
func TestWriteIdentityFirstEnrollmentRemovesCAAndKeyWhenCertFailsToRename(t *testing.T) {
	files := threeFiles(t)
	restore := enroll.SetRenameForTest(failRenameOnto(t, files.CertFile))
	defer restore()

	err := enroll.WriteIdentity(files, []byte("key"), []byte("cert"), []byte("ca"))
	requireClassWrite(t, err)
	requireNoneExist(t, files)
	requireNoLeftoverTemps(t, filepath.Dir(files.KeyFile))
}

// --force overwrite (a previous identity exists): a mid-commit failure
// must still remove every not-yet-renamed temporary, but the files that
// were not yet replaced (key, cert — CA commits first and already landed)
// keep their previous content. This is the documented exception rule 7
// cannot close: the CA that did land before the failure is NOT rolled
// back, unlike the first-enrollment case above, because rolling it back
// would not restore the guarantee either (some renames may already have
// replaced the previous identity, so there is nothing safe to revert to).
func TestWriteIdentityForceOverwriteMidCommitFailureLeavesUnreplacedFilesUntouched(t *testing.T) {
	files := threeFiles(t)
	if err := enroll.WriteIdentity(files, []byte("key1"), []byte("cert1"), []byte("ca1")); err != nil {
		t.Fatalf("first WriteIdentity: %v", err)
	}

	restore := enroll.SetRenameForTest(failRenameOnto(t, files.KeyFile))
	defer restore()

	err := enroll.WriteIdentity(files, []byte("key2"), []byte("cert2"), []byte("ca2"))
	requireClassWrite(t, err)

	// The documented exception: CA already committed before the failure
	// and is not rolled back.
	requireContents(t, files.CAFile, "ca2")
	// Not yet renamed when the failure happened: still the old identity.
	requireContents(t, files.KeyFile, "key1")
	requireContents(t, files.CertFile, "cert1")
	requireNoLeftoverTemps(t, filepath.Dir(files.KeyFile))
}

// failRenameOnto fails exactly the rename whose destination is target,
// and performs every other rename for real.
func failRenameOnto(t *testing.T, target string) func(oldpath, newpath string) error {
	t.Helper()
	return func(oldpath, newpath string) error {
		if newpath == target {
			return errors.New("simulated rename failure")
		}
		return os.Rename(oldpath, newpath)
	}
}

func requireClassWrite(t *testing.T, err error) {
	t.Helper()
	var eerr *enroll.Error
	if !errors.As(err, &eerr) || eerr.Class != enroll.ClassWrite {
		t.Fatalf("err = %v, want a ClassWrite *enroll.Error", err)
	}
}

func requireNoneExist(t *testing.T, files enroll.Files) {
	t.Helper()
	for _, p := range []string{files.KeyFile, files.CertFile, files.CAFile} {
		if _, statErr := os.Stat(p); !os.IsNotExist(statErr) {
			t.Errorf("%s exists, want none of the three", p)
		}
	}
}

func TestWriteIdentityFailureLeavesThePreviousFilesIntact(t *testing.T) {
	for _, broken := range []string{"key", "cert", "ca"} {
		t.Run(broken, func(t *testing.T) { checkFailedWriteLeavesFilesIntact(t, broken) })
	}
}

type writeIdentityFixture struct {
	dirs  map[string]string
	files enroll.Files
}

func newWriteIdentityFixture(t *testing.T) writeIdentityFixture {
	t.Helper()
	base := t.TempDir()
	dirs := map[string]string{"key": filepath.Join(base, "key"), "cert": filepath.Join(base, "cert"), "ca": filepath.Join(base, "ca")}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	files := enroll.Files{KeyFile: filepath.Join(dirs["key"], "tls.key"), CertFile: filepath.Join(dirs["cert"], "tls.crt"), CAFile: filepath.Join(dirs["ca"], "ca.crt")}
	return writeIdentityFixture{dirs: dirs, files: files}
}

func (f writeIdentityFixture) contents() map[string]struct{ path, want string } {
	return map[string]struct{ path, want string }{
		"key":  {f.files.KeyFile, "key1"},
		"cert": {f.files.CertFile, "cert1"},
		"ca":   {f.files.CAFile, "ca1"},
	}
}

func checkFailedWriteLeavesFilesIntact(t *testing.T, broken string) {
	t.Helper()
	f := newWriteIdentityFixture(t)
	if err := enroll.WriteIdentity(f.files, []byte("key1"), []byte("cert1"), []byte("ca1")); err != nil {
		t.Fatalf("first WriteIdentity: %v", err)
	}
	makeUnwritable(t, f.dirs[broken])

	err := enroll.WriteIdentity(f.files, []byte("key2"), []byte("cert2"), []byte("ca2"))
	var eerr *enroll.Error
	if !errors.As(err, &eerr) || eerr.Class != enroll.ClassWrite {
		t.Fatalf("err = %v, want a ClassWrite *enroll.Error", err)
	}
	requireUnchangedExcept(t, f, broken)
}

// requireUnchangedExcept checks every file and directory but the broken one
// still holds exactly what the first, successful WriteIdentity put there.
func requireUnchangedExcept(t *testing.T, f writeIdentityFixture, broken string) {
	t.Helper()
	for name, dir := range f.dirs {
		if name == broken {
			continue
		}
		requireOnlyTheOriginalFile(t, name, dir)
	}
	for name, c := range f.contents() {
		if name == broken {
			continue
		}
		requireContents(t, c.path, c.want)
	}
}

// requireOnlyTheOriginalFile: dir (not the broken one) has exactly the file
// WriteIdentity put there the first time — no leftover temporary files.
func requireOnlyTheOriginalFile(t *testing.T, name, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("%s dir has %d entries after a failed write, want exactly the original file: %v", name, len(entries), entries)
	}
}

func requireContents(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", path, err)
	}
	if string(got) != want {
		t.Fatalf("%s = %q, want %q", path, got, want)
	}
}

func requireMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat(%s): %v", path, err)
	}
	if info.Mode().Perm() != want {
		t.Fatalf("%s mode = %v, want %v", path, info.Mode().Perm(), want)
	}
}
