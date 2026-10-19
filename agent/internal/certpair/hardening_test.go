// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package certpair_test

import (
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/certpair"
	"github.com/Artur-Abalov/sard/agent/internal/certtest"
	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/tlsid"
)

// recording notes the renames asked of the host file system.
type recording struct {
	certpair.OSFS
	renamed []string
}

func (r *recording) Rename(oldPath, newPath string) error {
	r.renamed = append(r.renamed, newPath)
	return r.OSFS.Rename(oldPath, newPath)
}

// halfReadable lists a directory and says it failed doing so.
type halfReadable struct{ certpair.OSFS }

func (h halfReadable) ReadDir(dir string) ([]os.DirEntry, error) {
	entries, _ := h.OSFS.ReadDir(dir)
	return entries, errDisk
}

func TestOSFSRenameReportsAMissingSource(t *testing.T) {
	t.Parallel()
	h := newHost(t)
	if err := (certpair.OSFS{}).Rename(h.path("absent"), h.path("target")); err == nil {
		t.Fatal("renaming a file that is not there succeeded")
	}
}

func TestPromoteDoesNotTouchTheCertificateWhenTheKeyRenameFailed(t *testing.T) {
	t.Parallel()
	h := newHost(t)
	p1 := h.pair(1, t0, day(300))
	h.install(p1, "")
	h.install(h.pair(2, day(200), day(500)), ".new")
	sel := certpair.Selection{Pair: certpair.NewPair(h.files()), KeyIsNew: true, CertIsNew: true}
	rec := &failingRecording{recording: recording{}, failRename: "agent.key"}
	if err := certpair.Promote(rec, h.files(), sel); !errors.Is(err, errDisk) {
		t.Fatalf("err = %v", err)
	}
	if got, _ := os.ReadFile(h.path("agent.pem")); string(got) != string(p1.CertPEM) {
		t.Fatal("the current certificate was replaced next to a key that was not")
	}
	if _, err := os.Stat(h.path("agent.pem.new")); err != nil {
		t.Fatalf("the new certificate was moved: %v", err)
	}
}

type failingRecording struct {
	recording
	failRename string
}

func (f *failingRecording) Rename(oldPath, newPath string) error {
	if strings.HasSuffix(newPath, f.failRename) {
		f.renamed = append(f.renamed, newPath)
		return errDisk
	}
	return f.recording.Rename(oldPath, newPath)
}

func TestPromoteOfAKeyAloneRenamesNoCertificate(t *testing.T) {
	t.Parallel()
	h := newHost(t)
	h.install(h.pair(1, t0, day(300)), "")
	h.write("agent.key.new", h.pair(2, day(200), day(500)).KeyPEM)
	sel := certpair.Selection{Pair: certpair.Pair{KeyFile: h.path("agent.key.new"), CertFile: h.path("agent.pem")}, KeyIsNew: true}
	rec := &recording{}
	if err := certpair.Promote(rec, h.files(), sel); err != nil {
		t.Fatal(err)
	}
	if len(rec.renamed) != 1 || rec.renamed[0] != h.path("agent.key") {
		t.Fatalf("renamed = %v", rec.renamed)
	}
}

func TestPromoteOfACertificateAloneRenamesNoKey(t *testing.T) {
	t.Parallel()
	h := newHost(t)
	h.install(h.pair(1, t0, day(300)), "")
	h.write("agent.pem.new", h.pair(2, day(200), day(500)).CertPEM)
	sel := certpair.Selection{Pair: certpair.Pair{KeyFile: h.path("agent.key"), CertFile: h.path("agent.pem.new")}, CertIsNew: true}
	rec := &recording{}
	if err := certpair.Promote(rec, h.files(), sel); err != nil {
		t.Fatal(err)
	}
	if len(rec.renamed) != 1 || rec.renamed[0] != h.path("agent.pem") {
		t.Fatalf("renamed = %v", rec.renamed)
	}
}

// Scenario: Выбирается годная пара с самым поздним NotBefore
func TestTheSerialIsLowercaseHexOfTheCertificate(t *testing.T) {
	t.Parallel()
	h := newHost(t)
	h.install(h.pair(0xabcdef12, t0, day(300)), "")
	got, err := h.selectAt(day(1))
	if err != nil || got.Cert.Serial != "abcdef12" {
		t.Fatalf("selection = %+v, err = %v", got, err)
	}
}

func TestOnlyOneOfTheTLSPathsIsEnoughToSelectNothing(t *testing.T) {
	t.Parallel()
	for name, files := range map[string]config.TLS{
		"no certificate": {KeyFile: "/etc/sard/agent.key"},
		"no key":         {CertFile: "/etc/sard/agent.pem"},
	} {
		got, err := certpair.Select(files, certpair.OSFS{}, t0)
		want := certpair.Pair{KeyFile: files.KeyFile, CertFile: files.CertFile}
		if err != nil || got.Pair != want || got.Pending() {
			t.Errorf("%s: selection = %+v, err = %v", name, got, err)
		}
	}
}

func TestACertificateWithoutAnIdentityIsTheError(t *testing.T) {
	t.Parallel()
	h := newHost(t)
	nameless := h.ca.NewPair(t, certtest.Spec{Tenant: tenant, Agent: "", Role: "source", Serial: 1, NotBefore: t0, NotAfter: day(300)})
	h.install(nameless, "")
	_, err := h.selectAt(day(1))
	var fe *tlsid.FileError
	if !errors.As(err, &fe) || fe.Key != "tls.cert_file" || !strings.Contains(err.Error(), "identity") {
		t.Fatalf("err = %v", err)
	}
}

func TestADirectoryThatCannotBeListedHoldsNoStaleFiles(t *testing.T) {
	t.Parallel()
	h := newHost(t)
	h.install(h.pair(1, t0, day(300)), "")
	h.write(certpair.TempPrefix+"1", nil)
	got, err := certpair.Select(h.files(), halfReadable{}, day(1))
	if err != nil || len(got.Stale) != 0 {
		t.Fatalf("stale = %v, err = %v", got.Stale, err)
	}
}

// A write that fails after the temporary file exists removes it again. The
// file size limit makes the write fail, in a process of its own: it limits
// the whole process.
func TestOSFSWriteFileRemovesTheTempFileOfAFailedWrite(t *testing.T) {
	dir := t.TempDir()
	if os.Getenv("CERTPAIR_WRITE_LIMIT") == "1" {
		signal.Ignore(syscall.SIGXFSZ)
		var before syscall.Rlimit
		if err := syscall.Getrlimit(syscall.RLIMIT_FSIZE, &before); err != nil {
			t.Fatal(err)
		}
		if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &syscall.Rlimit{Cur: 0, Max: before.Max}); err != nil {
			t.Fatal(err)
		}
		err := (certpair.OSFS{}).WriteFile(os.Getenv("CERTPAIR_WRITE_DIR")+"/agent.key", []byte("data"), 0o600)
		// The test binary still writes its coverage when it ends.
		if rerr := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &before); rerr != nil {
			t.Fatal(rerr)
		}
		if err == nil {
			t.Fatal("a write beyond the file size limit succeeded")
		}
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestOSFSWriteFileRemovesTheTempFileOfAFailedWrite$")
	cmd.Env = append(os.Environ(), "CERTPAIR_WRITE_LIMIT=1", "CERTPAIR_WRITE_DIR="+dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("%d files left in the directory", len(entries))
	}
}

func TestASelectionIsPendingWhenEitherFileIsNew(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		key, cert, want bool
	}{{false, false, false}, {true, false, true}, {false, true, true}, {true, true, true}} {
		if got := (certpair.Selection{KeyIsNew: row.key, CertIsNew: row.cert}).Pending(); got != row.want {
			t.Errorf("key new %v, certificate new %v: pending = %v", row.key, row.cert, got)
		}
	}
}

// Scenario: Без единой согласованной пары агент сообщает о незавершённой регистрации
func TestAMissingKeyIsTheFileErrorOfTheKey(t *testing.T) {
	t.Parallel()
	h := newHost(t)
	h.write("agent.pem", h.pair(1, t0, day(300)).CertPEM)
	_, err := h.selectAt(day(1))
	var fe *tlsid.FileError
	if !errors.As(err, &fe) || fe.Key != "tls.key_file" {
		t.Fatalf("err = %v", err)
	}
}
