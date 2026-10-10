// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package hostsetup_test

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
	"github.com/Artur-Abalov/sard/agent/internal/refusal"
)

// serviceUID is the uid of a service user who is not the user of the test
// and not root, so that "the service user's" and "root's" differ from the
// owner of anything the test makes.
const serviceUID = 4242

func otherService(home string) hostsetup.User {
	u := service(home)
	u.UID = serviceUID
	return u
}

// ownedBy makes the files of a home look owned as the map says; every
// ancestor of the home is root's, and what the map leaves out is the
// service user's.
func (h *hooks) ownedBy(home string, owners map[string]uint32) {
	for _, c := range strings.Split(strings.Trim(filepath.Dir(home), "/"), "/") {
		h.uid[c] = 0
	}
	h.uid[filepath.Base(home)] = serviceUID
	h.uid[".ssh"] = serviceUID
	h.uid["known_hosts"] = serviceUID
	for name, uid := range owners {
		h.uid[name] = uid
	}
}

// readOwnedBy opens the home of a service user and reads known_hosts when
// the owner of the element (home, ssh or file) is as given.
func readOwnedBy(t *testing.T, element string, owner uint32) (found bool, f *refusal.Failure) {
	t.Helper()
	home := t.TempDir()
	ok(t, os.Mkdir(filepath.Join(home, ".ssh"), 0o700))
	ok(t, os.WriteFile(filepath.Join(home, ".ssh", "known_hosts"), []byte("x\n"), 0o600))
	name := map[string]string{"home": filepath.Base(home), "ssh": ".ssh", "file": "known_hosts"}[element]
	hk := newHooks()
	hk.ownedBy(home, map[string]uint32{name: owner})
	h, f := hostsetup.OpenSSHHome(hk.fs(), otherService(home))
	if f != nil {
		return false, f
	}
	defer h.Close()
	_, found, f = h.Read("known_hosts")
	return found, f
}

// Р37, Р38: the service user and root may own the home, ~/.ssh and its
// files; anyone else, root's neighbour included, may not.
func TestOnlyTheServiceUserAndRootMayOwnTheHomeTheSSHDirectoryAndItsFiles(t *testing.T) {
	for _, element := range []string{"home", "ssh", "file"} {
		for owner, accepted := range map[uint32]bool{serviceUID: true, 0: true, serviceUID + 1: false, 1: false} {
			found, f := readOwnedBy(t, element, owner)
			if accepted != (f == nil && found) {
				t.Errorf("%s owned by uid %d: found %v, %v (accepted: %v)", element, owner, found, f, accepted)
			}
		}
	}
}

// What cannot be judged is not accepted.
func TestAnOwnerThatTheFileSystemDoesNotTellIsNotAccepted(t *testing.T) {
	home := t.TempDir()
	ok(t, os.Mkdir(filepath.Join(home, ".ssh"), 0o700))
	ok(t, os.WriteFile(filepath.Join(home, ".ssh", "known_hosts"), []byte("x\n"), 0o600))
	for name, why := range map[string]string{".ssh": "no owner", filepath.Base(home): "no owner"} {
		hk := newHooks()
		hk.anonymous[name] = true
		_, f := hostsetup.OpenSSHHome(hk.fs(), service(home))
		assertFailure(t, f, map[bool]refusal.Reason{true: refusal.SSHFileRejected, false: refusal.SSHHomeInvalid}[name == ".ssh"], why)
	}
	hk := newHooks()
	h := openHome(t, hk.fs(), home)
	hk.anonymous["known_hosts"] = true
	_, found, f := h.Read("known_hosts")
	assertFailure(t, f, refusal.SSHFileRejected, "known_hosts", "not a regular file")
	if found {
		t.Fatal("found")
	}
}

// The home is never exempt by the sticky bit; the directories above it are
// (/tmp is one), whether the home is there or is yet to be made.
func TestAHomeOpenToOthersIsRejectedEvenWithTheStickyBit(t *testing.T) {
	home := t.TempDir()
	ok(t, os.Chmod(home, 0o777|os.ModeSticky))
	_, f := hostsetup.OpenSSHHome(hostsetup.OS{}, service(home))
	assertFailure(t, f, refusal.SSHHomeInvalid, home, "writable")
}

func TestAMissingHomeIsMadeUnderAParentOpenToOthersWithTheStickyBit(t *testing.T) {
	parent := t.TempDir()
	ok(t, os.Chmod(parent, 0o777|os.ModeSticky))
	h := openHome(t, hostsetup.OS{}, filepath.Join(parent, "state"))
	ok(t, errOf(h.Write("known_hosts", []byte("x\n"))))
}

// A parent that was fine when the home was looked at and is not any more
// when the home is made is the home's fault, not a failure to write.
func TestAParentThatTurnedOpenToOthersBeforeTheHomeWasMadeIsSSHHomeInvalid(t *testing.T) {
	parent := t.TempDir()
	h := openHome(t, hostsetup.OS{}, filepath.Join(parent, "state"))
	ok(t, os.Chmod(parent, 0o777))
	assertFailure(t, h.Write("known_hosts", []byte("x\n")), refusal.SSHHomeInvalid, parent)
	if _, err := os.Lstat(filepath.Join(parent, "state")); err == nil {
		t.Fatal("the home was made")
	}
}

func TestAFailureToMakeTheHomeOrTheSSHDirectoryIsConfigWriteNamingTheCause(t *testing.T) {
	for _, step := range []string{"state", ".ssh"} {
		parent := t.TempDir()
		hk := newHooks()
		hk.fail["mkdir"] = step
		h := openHome(t, hk.fs(), filepath.Join(parent, "state"))
		if step == ".ssh" {
			ok(t, os.Mkdir(filepath.Join(parent, "state"), 0o700))
			h.Close()
			h = openHome(t, hk.fs(), filepath.Join(parent, "state"))
		}
		assertFailure(t, h.Write("known_hosts", []byte("x\n")), refusal.ConfigWrite, step, "injected")
	}
}

// failingRootFS cannot open "/".
type failingRootFS struct{ hostsetup.OS }

func (failingRootFS) OpenRootDir() (hostsetup.Dir, error) { return nil, errInjected }

func TestAFileSystemThatCannotBeOpenedIsSSHHomeInvalidNamingTheCause(t *testing.T) {
	_, f := hostsetup.OpenSSHHome(failingRootFS{}, service(t.TempDir()))
	assertFailure(t, f, refusal.SSHHomeInvalid, "sard-agent", "injected")
}

func TestASSHDirectoryThatCannotBeLookedAtIsRejected(t *testing.T) {
	home := t.TempDir()
	ok(t, os.Mkdir(filepath.Join(home, ".ssh"), 0o700))
	hk := newHooks()
	hk.fail["dirstat"] = ".ssh"
	_, f := hostsetup.OpenSSHHome(hk.fs(), service(home))
	assertFailure(t, f, refusal.SSHFileRejected, filepath.Join(home, ".ssh"), "cannot be looked at", "injected")
}

func TestAFileThatCannotBeOpenedOrLookedAtIsRejectedNamingTheCause(t *testing.T) {
	for step, call := range map[string]func(h *hostsetup.SSHHome) *refusal.Failure{
		"open":  func(h *hostsetup.SSHHome) *refusal.Failure { _, _, f := h.Read("config"); return f },
		"lstat": func(h *hostsetup.SSHHome) *refusal.Failure { _, f := h.Present("config"); return f },
	} {
		home := t.TempDir()
		ok(t, os.Mkdir(filepath.Join(home, ".ssh"), 0o700))
		ok(t, os.WriteFile(filepath.Join(home, ".ssh", "config"), []byte("Host *\n"), 0o600))
		hk := newHooks()
		hk.fail[step] = "config"
		h := openHome(t, hk.fs(), home)
		assertFailure(t, call(h), refusal.SSHFileRejected, filepath.Join(home, ".ssh", "config"), "injected")
	}
}

func TestALinkIsNamedALinkAndAFileOverTheLimitIsNamedTooLargeWhereverItIsLookedAt(t *testing.T) {
	home := t.TempDir()
	ok(t, os.Mkdir(filepath.Join(home, ".ssh"), 0o700))
	ok(t, os.Symlink("/etc/passwd", filepath.Join(home, ".ssh", "link")))
	ok(t, os.WriteFile(filepath.Join(home, ".ssh", "large"), make([]byte, hostsetup.MaxSSHFileSize+1), 0o600))
	h := openHome(t, hostsetup.OS{}, home)
	_, f := h.Present("link")
	assertFailure(t, f, refusal.SSHFileRejected, "symbolic link")
	_, f = h.Present("large")
	assertFailure(t, f, refusal.SSHFileRejected, "larger than 1048576 bytes")
}

// A file that grows after it was looked at is not read beyond the limit.
func TestAFileThatGrewPastTheLimitAfterItWasLookedAtIsRejected(t *testing.T) {
	home := t.TempDir()
	ok(t, os.Mkdir(filepath.Join(home, ".ssh"), 0o700))
	ok(t, os.WriteFile(filepath.Join(home, ".ssh", "known_hosts"), make([]byte, hostsetup.MaxSSHFileSize+1), 0o600))
	hk := newHooks()
	hk.size["known_hosts"] = 10
	h := openHome(t, hk.fs(), home)
	data, found, f := h.Read("known_hosts")
	assertFailure(t, f, refusal.SSHFileRejected, "larger than 1048576 bytes")
	if data != nil || found {
		t.Fatalf("data %d bytes, found %v", len(data), found)
	}
}

// Every descriptor is closed on every way out.
func TestNoDescriptorIsLeftOpenByAReadAWriteThatFailsOrARejectedDirectory(t *testing.T) {
	home := t.TempDir()
	ok(t, os.Mkdir(filepath.Join(home, ".ssh"), 0o700))
	ok(t, os.WriteFile(filepath.Join(home, ".ssh", "known_hosts"), []byte("x\n"), 0o600))
	assertNoLeak(t, "a read of a file that is there", func() {
		h := openHome(t, hostsetup.OS{}, home)
		_, found, f := h.Read("known_hosts")
		ok(t, errOf(f))
		if !found {
			t.Error("not found")
		}
		h.Close()
	})
	assertNoLeak(t, "a write that fails", func() {
		hk := newHooks()
		hk.fail["write"] = "config"
		h := openHome(t, hk.fs(), home)
		assertFailure(t, h.Write("config", []byte("c\n")), refusal.ConfigWrite)
		h.Close()
	})
	assertNoLeak(t, "the making of a home", func() {
		h := openHome(t, hostsetup.OS{}, filepath.Join(t.TempDir(), "state"))
		ok(t, errOf(h.Write("config", []byte("c\n"))))
		h.Close()
	})
	assertNoLeak(t, "a ~/.ssh that is rejected", func() {
		bad := t.TempDir()
		ok(t, os.Symlink(t.TempDir(), filepath.Join(bad, ".ssh")))
		_, f := hostsetup.OpenSSHHome(hostsetup.OS{}, service(bad))
		assertFailure(t, f, refusal.SSHFileRejected)
	})
}

// Close may be called again.
func TestCloseTwiceLeavesNothingToCloseTheSecondTime(t *testing.T) {
	home := t.TempDir()
	ok(t, os.Mkdir(filepath.Join(home, ".ssh"), 0o700))
	assertNoLeak(t, "a home closed twice", func() {
		h, f := hostsetup.OpenSSHHome(hostsetup.OS{}, service(home))
		ok(t, errOf(f))
		h.Close()
		h.Close()
	})
}

var tempName = regexp.MustCompile(`^\.known_hosts\.tmp-[0-9a-f]{12}$`)

// The temporary file has a name nobody can guess, and a name that is taken
// is not reused: another one is tried, eight times at most; an error other
// than "taken" is not retried.
func TestTheTemporaryFileGetsARandomNameIsRetriedWhenTakenAndNotWhenItFailsOtherwise(t *testing.T) {
	home := t.TempDir()
	hk := newHooks()
	h := openHome(t, hk.fs(), home)
	ok(t, errOf(h.Write("known_hosts", []byte("x\n"))))
	if len(hk.createTries) != 1 || !tempName.MatchString(hk.createTries[0]) {
		t.Fatalf("names tried: %q", hk.createTries)
	}

	hk.createTries, hk.createErr = nil, syscall.EEXIST
	assertFailure(t, h.Write("known_hosts", []byte("y\n")), refusal.ConfigWrite, "known_hosts", "file exists")
	if len(hk.createTries) != 8 {
		t.Errorf("a taken name was tried %d times", len(hk.createTries))
	}
	seen := map[string]bool{}
	for _, name := range hk.createTries {
		if seen[name] || !tempName.MatchString(name) {
			t.Errorf("name %q repeated or malformed", name)
		}
		seen[name] = true
	}

	hk.createTries, hk.createErr = nil, errors.New("disk on fire")
	assertFailure(t, h.Write("known_hosts", []byte("z\n")), refusal.ConfigWrite, "disk on fire")
	if len(hk.createTries) != 1 {
		t.Errorf("an error other than a taken name was tried %d times", len(hk.createTries))
	}
}
