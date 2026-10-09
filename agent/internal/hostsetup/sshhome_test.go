// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package hostsetup_test

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
	"github.com/Artur-Abalov/sard/agent/internal/refusal"
)

// service is the service user of these tests: the user running them, so
// that the owner of what they make can really be set.
func service(home string) hostsetup.User {
	return hostsetup.User{Name: "sard-agent", UID: uint32(os.Getuid()), GID: uint32(os.Getgid()), Home: home}
}

func openHome(t *testing.T, fsys hostsetup.FS, home string) *hostsetup.SSHHome {
	t.Helper()
	h, f := hostsetup.OpenSSHHome(fsys, service(home))
	if f != nil {
		t.Fatal(f)
	}
	t.Cleanup(h.Close)
	return h
}

func assertFailure(t *testing.T, f *refusal.Failure, reason refusal.Reason, mentions ...string) {
	t.Helper()
	if f == nil || f.Reason != reason {
		t.Fatalf("failure %+v, want %s", f, reason)
	}
	for _, m := range mentions {
		if !strings.Contains(f.Detail, m) {
			t.Errorf("%s does not mention %q: %s", reason, m, f.Detail)
		}
	}
}

// withUmask runs the test with the umask given.
func withUmask(t *testing.T, mask int) {
	t.Helper()
	old := syscall.Umask(mask)
	t.Cleanup(func() { syscall.Umask(old) })
}

// Р37: the home of passwd must be an absolute path that is not "/".
func TestAnUnusableHomeIsSSHHomeInvalidAndTouchesNothing(t *testing.T) {
	for _, home := range []string{"", "relative", "/", "relative/../x"} {
		_, f := hostsetup.OpenSSHHome(hostsetup.OS{}, service(home))
		assertFailure(t, f, refusal.SSHHomeInvalid)
	}
}

func TestALinkOnTheWayToTheHomeIsSSHHomeInvalidAndNamesTheComponent(t *testing.T) {
	base, outside := t.TempDir(), t.TempDir()
	ok(t, os.Symlink(outside, filepath.Join(base, "link")))
	ok(t, os.Mkdir(filepath.Join(outside, "home"), 0o755))
	for _, home := range []string{filepath.Join(base, "link", "home"), filepath.Join(base, "link")} {
		_, f := hostsetup.OpenSSHHome(hostsetup.OS{}, service(home))
		assertFailure(t, f, refusal.SSHHomeInvalid, filepath.Join(base, "link"))
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 1 {
		t.Fatalf("the target of the link changed: %v", entries)
	}
}

func TestAHomeThatIsAFileIsSSHHomeInvalid(t *testing.T) {
	file := filepath.Join(t.TempDir(), "f")
	ok(t, os.WriteFile(file, nil, 0o600))
	_, f := hostsetup.OpenSSHHome(hostsetup.OS{}, service(file))
	assertFailure(t, f, refusal.SSHHomeInvalid, file)
}

// Н23: a missing home is made as the last component of its held parent.
func TestAMissingHomeAndSSHDirectoryAreMadeForTheServiceUserWhateverTheUmask(t *testing.T) {
	base := t.TempDir()
	withUmask(t, 0)
	home := filepath.Join(base, "state")
	h := openHome(t, hostsetup.OS{}, home)
	if data, found, f := h.Read("known_hosts"); f != nil || found || data != nil {
		t.Fatalf("a missing home: %q, %v, %v", data, found, f)
	}
	if _, err := os.Lstat(home); err == nil {
		t.Fatal("reading made the home")
	}
	if f := h.Write("known_hosts", []byte("x\n")); f != nil {
		t.Fatal(f)
	}
	for path, mode := range map[string]os.FileMode{home: 0o700, filepath.Join(home, ".ssh"): 0o700, filepath.Join(home, ".ssh", "known_hosts"): 0o600} {
		assertOwnedWithMode(t, path, mode)
	}
}

// assertOwnedWithMode: the path is the user's own, with the mode.
func assertOwnedWithMode(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	ok(t, err)
	if info.Mode().Perm() != mode || int(info.Sys().(*syscall.Stat_t).Uid) != os.Getuid() {
		t.Errorf("%s: mode %v", path, info.Mode().Perm())
	}
}

func TestWithoutTheParentOfTheHomeNothingIsMadeAndTheFirstMissingDirectoryIsNamed(t *testing.T) {
	base := t.TempDir()
	h := openHome(t, hostsetup.OS{}, filepath.Join(base, "var", "lib", "sard-agent"))
	f := h.Write("known_hosts", []byte("x\n"))
	assertFailure(t, f, refusal.ConfigWrite, filepath.Join(base, "var"))
	if entries, _ := os.ReadDir(base); len(entries) != 0 {
		t.Fatalf("directories were made: %v", entries)
	}
}

func TestAnExistingHomeAndSSHDirectoryKeepTheirModes(t *testing.T) {
	home := t.TempDir()
	ok(t, os.Chmod(home, 0o755))
	ok(t, os.Mkdir(filepath.Join(home, ".ssh"), 0o750))
	ok(t, os.Chmod(filepath.Join(home, ".ssh"), 0o750))
	h := openHome(t, hostsetup.OS{}, home)
	ok(t, errOf(h.Write("config", []byte("c\n"))))
	for path, mode := range map[string]os.FileMode{home: 0o755, filepath.Join(home, ".ssh"): 0o750} {
		if info, err := os.Stat(path); err != nil || info.Mode().Perm() != mode {
			t.Errorf("%s: %v, %v", path, info, err)
		}
	}
}

// errOf makes a failure a plain error for ok.
func errOf(f *refusal.Failure) error {
	if f == nil {
		return nil
	}
	return f
}

// Р37: ~/.ssh is opened without following a link and must be the service
// user's or root's directory.
func TestASSHDirectoryThatIsALinkOrAFileOrAnotherUsersIsRejected(t *testing.T) {
	for name, plant := range map[string]func(t *testing.T, home, ssh string, h *hooks){
		"a link": func(t *testing.T, home, ssh string, _ *hooks) {
			outside := t.TempDir()
			ok(t, os.Symlink(outside, ssh))
		},
		"a file": func(t *testing.T, _, ssh string, _ *hooks) { ok(t, os.WriteFile(ssh, nil, 0o600)) },
		"owned by another user": func(t *testing.T, _, ssh string, h *hooks) {
			ok(t, os.Mkdir(ssh, 0o700))
			h.uid[".ssh"] = uint32(os.Getuid()) + 1000
		},
	} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			h := newHooks()
			ssh := filepath.Join(home, ".ssh")
			plant(t, home, ssh, h)
			sh, f := hostsetup.OpenSSHHome(h.fs(), service(home))
			if f == nil {
				defer sh.Close()
			}
			assertFailure(t, f, refusal.SSHFileRejected, ssh)
		})
	}
}

func TestASSHDirectoryOfRootIsAccepted(t *testing.T) {
	home := t.TempDir()
	ok(t, os.Mkdir(filepath.Join(home, ".ssh"), 0o700))
	h := newHooks()
	h.uid[".ssh"] = 0
	openHome(t, h.fs(), home)
}

// Р38: what is read is a regular file with one name, of the service user
// or root, not larger than 1 MiB; it is judged from the descriptor.
func TestAnExistingFileIsReadWhenItIsRegularWithOneNameAndTheServiceUsers(t *testing.T) {
	home := t.TempDir()
	ok(t, os.Mkdir(filepath.Join(home, ".ssh"), 0o700))
	ok(t, os.WriteFile(filepath.Join(home, ".ssh", "known_hosts"), []byte("a b c\n"), 0o600))
	h := openHome(t, hostsetup.OS{}, home)
	data, found, f := h.Read("known_hosts")
	if f != nil || !found || string(data) != "a b c\n" {
		t.Fatalf("%q, %v, %v", data, found, f)
	}
	if data, found, f := h.Read("config"); f != nil || found || data != nil {
		t.Fatalf("a missing file: %q, %v, %v", data, found, f)
	}
}

func TestAPlantedFileIsRejectedAndNeverReadBeyondItsStat(t *testing.T) {
	victim := filepath.Join(t.TempDir(), "victim")
	ok(t, os.WriteFile(victim, []byte("root:x\n"), 0o600))
	for name, plant := range map[string]func(t *testing.T, path string){
		"a link":          func(t *testing.T, path string) { ok(t, os.Symlink(victim, path)) },
		"a dangling link": func(t *testing.T, path string) { ok(t, os.Symlink(victim+".none", path)) },
		"a hard link":     func(t *testing.T, path string) { ok(t, os.Link(victim, path)) },
		"a directory":     func(t *testing.T, path string) { ok(t, os.Mkdir(path, 0o700)) },
		"a FIFO":          func(t *testing.T, path string) { ok(t, syscall.Mkfifo(path, 0o600)) },
	} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			ok(t, os.Mkdir(filepath.Join(home, ".ssh"), 0o700))
			path := filepath.Join(home, ".ssh", "known_hosts")
			plant(t, path)
			h := openHome(t, hostsetup.OS{}, home)
			data, found, f := h.Read("known_hosts")
			assertFailure(t, f, refusal.SSHFileRejected, path)
			if data != nil || found {
				t.Fatalf("data %q found %v", data, found)
			}
			if got, _ := os.ReadFile(victim); string(got) != "root:x\n" {
				t.Fatalf("the victim changed: %q", got)
			}
		})
	}
}

func TestAFileOfAnotherUserIsRejectedAndOneOfRootIsAccepted(t *testing.T) {
	home := t.TempDir()
	ok(t, os.Mkdir(filepath.Join(home, ".ssh"), 0o700))
	ok(t, os.WriteFile(filepath.Join(home, ".ssh", "config"), []byte("Host *\n"), 0o644))
	hk := newHooks()
	h := openHome(t, hk.fs(), home)
	hk.uid["config"] = uint32(os.Getuid()) + 1000
	_, _, f := h.Read("config")
	assertFailure(t, f, refusal.SSHFileRejected, filepath.Join(home, ".ssh", "config"))
	hk.uid["config"] = 0
	data, found, f := h.Read("config")
	if f != nil || !found || string(data) != "Host *\n" {
		t.Fatalf("a file of root: %q, %v, %v", data, found, f)
	}
}

func TestAFileLargerThanOneMiBIsRejectedAndOneOfExactlyOneMiBIsRead(t *testing.T) {
	home := t.TempDir()
	ok(t, os.Mkdir(filepath.Join(home, ".ssh"), 0o700))
	path := filepath.Join(home, ".ssh", "known_hosts")
	h := openHome(t, hostsetup.OS{}, home)
	ok(t, os.WriteFile(path, make([]byte, 1<<20), 0o600))
	if data, found, f := h.Read("known_hosts"); f != nil || !found || len(data) != 1<<20 {
		t.Fatalf("exactly 1 MiB: %d, %v, %v", len(data), found, f)
	}
	ok(t, os.WriteFile(path, make([]byte, 1<<20+1), 0o600))
	_, _, f := h.Read("known_hosts")
	assertFailure(t, f, refusal.SSHFileRejected, path, "1048576")
}

func TestAFileThatCannotBeLookedAtOrReadIsRejectedNamingThePathAndTheCause(t *testing.T) {
	for _, step := range []string{"stat", "read"} {
		home := t.TempDir()
		ok(t, os.Mkdir(filepath.Join(home, ".ssh"), 0o700))
		path := filepath.Join(home, ".ssh", "config")
		ok(t, os.WriteFile(path, []byte("Host *\n"), 0o600))
		hk := newHooks()
		hk.fail[step] = "config"
		h := openHome(t, hk.fs(), home)
		data, found, f := h.Read("config")
		assertFailure(t, f, refusal.SSHFileRejected, path, "injected")
		if data != nil || found {
			t.Errorf("%s: data %q, found %v", step, data, found)
		}
	}
}

// The private key is only looked at.
func TestPresentLooksAtAFileWithoutOpeningIt(t *testing.T) {
	home := t.TempDir()
	ok(t, os.Mkdir(filepath.Join(home, ".ssh"), 0o700))
	ok(t, os.WriteFile(filepath.Join(home, ".ssh", "id_ed25519"), []byte("PRIVATE"), 0o600))
	hk := newHooks()
	h := openHome(t, hk.fs(), home)
	if present, f := h.Present("id_ed25519"); f != nil || !present {
		t.Fatalf("%v, %v", present, f)
	}
	if present, f := h.Present("id_rsa"); f != nil || present {
		t.Fatalf("a missing name: %v, %v", present, f)
	}
	if hk.has("openfile") {
		t.Fatalf("the file was opened: %v", hk.ops)
	}
}

func TestPresentRejectsALinkEvenWhenItPointsNowhere(t *testing.T) {
	home := t.TempDir()
	ok(t, os.Mkdir(filepath.Join(home, ".ssh"), 0o700))
	path := filepath.Join(home, ".ssh", "id_ed25519")
	ok(t, os.Symlink(filepath.Join(t.TempDir(), "absent"), path))
	h := openHome(t, hostsetup.OS{}, home)
	_, f := h.Present("id_ed25519")
	assertFailure(t, f, refusal.SSHFileRejected, path)
}

func TestPresentOfAMissingSSHDirectoryIsFalse(t *testing.T) {
	h := openHome(t, hostsetup.OS{}, filepath.Join(t.TempDir(), "none"))
	if present, f := h.Present("id_ed25519"); f != nil || present {
		t.Fatalf("%v, %v", present, f)
	}
}

// Р38: a file is written through the descriptor of the directory: a
// temporary file, its owner and mode before the content, fsync, rename in
// the same directory, fsync of the directory.
func TestAFileIsWrittenWithItsOwnerBeforeItsContentAndRenamedInTheSameDirectory(t *testing.T) {
	home := t.TempDir()
	withUmask(t, 0)
	hk := newHooks()
	h := openHome(t, hk.fs(), home)
	ok(t, errOf(h.Write("known_hosts", []byte("host key\n"))))
	dir := filepath.Join(home, ".ssh")
	if got := strings.Join(hk.stepsOf(dir, "known_hosts"), " "); got != "create chown chmod write sync rename dirsync" {
		t.Fatalf("order of operations: %s", got)
	}
	assertOwnedWithMode(t, filepath.Join(dir, "known_hosts"), 0o600)
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("entries %v", entries)
	}
}

func TestAWriteReplacesTheNameAndLeavesTheTargetOfAHardLinkAlone(t *testing.T) {
	home := t.TempDir()
	ok(t, os.Mkdir(filepath.Join(home, ".ssh"), 0o700))
	other := filepath.Join(t.TempDir(), "other")
	ok(t, os.WriteFile(other, []byte("other\n"), 0o600))
	ok(t, os.Link(other, filepath.Join(home, ".ssh", "config")))
	h := openHome(t, hostsetup.OS{}, home)
	ok(t, errOf(h.Write("config", []byte("new\n"))))
	if got, _ := os.ReadFile(other); string(got) != "other\n" {
		t.Fatalf("the other name changed: %q", got)
	}
	if got, _ := os.ReadFile(filepath.Join(home, ".ssh", "config")); string(got) != "new\n" {
		t.Fatalf("config %q", got)
	}
}

func TestAFailedWriteIsConfigWriteNamingTheFileAndLeavesNoTemporaryFile(t *testing.T) {
	for _, step := range []string{"chown", "chmod", "write", "sync", "rename"} {
		t.Run(step, func(t *testing.T) {
			home := t.TempDir()
			ok(t, os.Mkdir(filepath.Join(home, ".ssh"), 0o700))
			path := filepath.Join(home, ".ssh", "known_hosts")
			ok(t, os.WriteFile(path, []byte("old\n"), 0o600))
			hk := newHooks()
			hk.fail[step] = "known_hosts"
			h := openHome(t, hk.fs(), home)
			assertFailure(t, h.Write("known_hosts", []byte("new\n")), refusal.ConfigWrite, path)
			if got, _ := os.ReadFile(path); string(got) != "old\n" {
				t.Errorf("content %q", got)
			}
			if entries, _ := os.ReadDir(filepath.Join(home, ".ssh")); len(entries) != 1 {
				t.Errorf("entries %v", entries)
			}
		})
	}
}

func TestAFailedSyncOfTheDirectoryAfterTheRenameIsConfigWrite(t *testing.T) {
	home := t.TempDir()
	hk := newHooks()
	hk.fail["dirsync"] = ".ssh"
	h := openHome(t, hk.fs(), home)
	assertFailure(t, h.Write("known_hosts", []byte("x\n")), refusal.ConfigWrite, "known_hosts")
}

// R-family: the directory swapped for a link after it was opened does not
// take the write away.
func TestADirectorySwappedForALinkAfterItWasOpenedDoesNotTakeTheWriteAway(t *testing.T) {
	home, outside := t.TempDir(), t.TempDir()
	dir := filepath.Join(home, ".ssh")
	ok(t, os.Mkdir(dir, 0o700))
	h := openHome(t, hostsetup.OS{}, home)
	ok(t, os.Rename(dir, filepath.Join(home, "moved")))
	ok(t, os.Symlink(outside, dir))
	ok(t, errOf(h.Write("known_hosts", []byte("x\n"))))
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Fatalf("a file was made outside: %v", entries)
	}
	if _, err := os.Stat(filepath.Join(home, "moved", "known_hosts")); err != nil {
		t.Fatal(err)
	}
}

func TestSSHHomeClosesEveryDescriptorItOpened(t *testing.T) {
	home := t.TempDir()
	ok(t, os.Mkdir(filepath.Join(home, ".ssh"), 0o700))
	assertNoLeak(t, "an SSHHome used and closed", func() {
		h, f := hostsetup.OpenSSHHome(hostsetup.OS{}, service(home))
		if f != nil {
			t.Fatal(f)
		}
		_, _, _ = h.Read("known_hosts")
		_ = h.Write("known_hosts", []byte("x\n"))
		_, _ = h.Present("id_ed25519")
		h.Close()
		h.Close()
	})
}

// A home, ~/.ssh or file that others can write to lets any local user plant
// a host key that would then be trusted silently (Р40).
func TestAHomeSSHDirectoryOrFileOthersCanWriteIsRejected(t *testing.T) {
	for _, mode := range []os.FileMode{0o777, 0o775, 0o757} {
		home := t.TempDir()
		ok(t, os.Chmod(home, mode))
		_, f := hostsetup.OpenSSHHome(hostsetup.OS{}, service(home))
		assertFailure(t, f, refusal.SSHHomeInvalid, home)

		home = t.TempDir()
		ok(t, os.Mkdir(filepath.Join(home, ".ssh"), 0o700))
		ok(t, os.Chmod(filepath.Join(home, ".ssh"), mode))
		_, f = hostsetup.OpenSSHHome(hostsetup.OS{}, service(home))
		assertFailure(t, f, refusal.SSHFileRejected, filepath.Join(home, ".ssh"))
	}
	for _, mode := range []os.FileMode{0o666, 0o664, 0o646} {
		home := t.TempDir()
		ok(t, os.Mkdir(filepath.Join(home, ".ssh"), 0o700))
		path := filepath.Join(home, ".ssh", "known_hosts")
		ok(t, os.WriteFile(path, []byte("h ssh-ed25519 AAAA\n"), 0o600))
		ok(t, os.Chmod(path, mode))
		h := openHome(t, hostsetup.OS{}, home)
		data, _, f := h.Read("known_hosts")
		assertFailure(t, f, refusal.SSHFileRejected, path, "writable")
		if data != nil {
			t.Errorf("%v: read %q", mode, data)
		}
	}
}

func TestAPublicKeyOf0644IsStillRead(t *testing.T) {
	home := t.TempDir()
	ok(t, os.Mkdir(filepath.Join(home, ".ssh"), 0o700))
	path := filepath.Join(home, ".ssh", "id_ed25519.pub")
	ok(t, os.WriteFile(path, []byte("ssh-ed25519 AAAA x\n"), 0o644))
	ok(t, os.Chmod(path, 0o644))
	if _, found, f := openHome(t, hostsetup.OS{}, home).Read("id_ed25519.pub"); f != nil || !found {
		t.Fatalf("%v %v", found, f)
	}
}

func TestAHomeOfAnotherUserIsRejected(t *testing.T) {
	base := t.TempDir()
	home := filepath.Join(base, "userhome")
	ok(t, os.Mkdir(home, 0o755))
	hk := newHooks()
	hk.uid["userhome"] = uint32(os.Getuid()) + 1000
	_, f := hostsetup.OpenSSHHome(hk.fs(), service(home))
	assertFailure(t, f, refusal.SSHHomeInvalid, home)
	hk.uid["userhome"] = 0
	h, f := hostsetup.OpenSSHHome(hk.fs(), service(home))
	if f != nil {
		t.Fatal(f)
	}
	h.Close()
}

func TestAnAncestorOfTheHomeOfAnotherUserOrOpenToOthersWithoutTheStickyBitIsRejected(t *testing.T) {
	for name, c := range map[string]struct {
		prepare func(t *testing.T, ancestor string, hk *hooks)
		ok      bool
	}{
		"owned by another user":  {func(_ *testing.T, _ string, hk *hooks) { hk.uid["a"] = uint32(os.Getuid()) + 1000 }, false},
		"owned by root":          {func(_ *testing.T, _ string, hk *hooks) { hk.uid["a"] = 0 }, true},
		"open to others":         {func(t *testing.T, a string, _ *hooks) { ok(t, os.Chmod(a, 0o777)) }, false},
		"group writable":         {func(t *testing.T, a string, _ *hooks) { ok(t, os.Chmod(a, 0o775)) }, false},
		"open to others, sticky": {func(t *testing.T, a string, _ *hooks) { ok(t, os.Chmod(a, 0o777|os.ModeSticky)) }, true},
	} {
		t.Run(name, func(t *testing.T) {
			ancestor := filepath.Join(t.TempDir(), "a")
			home := filepath.Join(ancestor, "home")
			ok(t, os.MkdirAll(home, 0o755))
			hk := newHooks()
			c.prepare(t, ancestor, hk)
			h, f := hostsetup.OpenSSHHome(hk.fs(), service(home))
			if c.ok {
				if f != nil {
					t.Fatal(f)
				}
				h.Close()
				return
			}
			assertFailure(t, f, refusal.SSHHomeInvalid, ancestor)
		})
	}
}

func TestAnAncestorOfAHomeThatIsNotThereYetIsCheckedToo(t *testing.T) {
	ancestor := filepath.Join(t.TempDir(), "a")
	ok(t, os.Mkdir(ancestor, 0o755))
	ok(t, os.Chmod(ancestor, 0o777))
	_, f := hostsetup.OpenSSHHome(hostsetup.OS{}, service(filepath.Join(ancestor, "home")))
	assertFailure(t, f, refusal.SSHHomeInvalid, ancestor)
	if _, err := os.Stat(filepath.Join(ancestor, "home")); err == nil {
		t.Fatal("the home was made")
	}
}
