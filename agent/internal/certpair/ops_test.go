// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package certpair_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/certpair"
)

// failing is the host file system whose writes of chosen files fail.
type failing struct {
	certpair.OSFS
	failWrite  string
	failRename string
}

var errDisk = errors.New("disk failure")

func (f failing) WriteFile(path string, data []byte, mode os.FileMode) error {
	if filepath.Base(path) == f.failWrite {
		return errDisk
	}
	return f.OSFS.WriteFile(path, data, mode)
}

func (f failing) Rename(oldPath, newPath string) error {
	if filepath.Base(newPath) == f.failRename {
		return errDisk
	}
	return f.OSFS.Rename(oldPath, newPath)
}

func (h *host) names() string {
	entries, err := os.ReadDir(h.dir)
	if err != nil {
		h.t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return strings.Join(names, " ")
}

// Scenario: Новая пара пишется рядом с текущей с правами ключа 0600 и сертификата 0644
func TestWriteNewPutsTheKeyAndTheCertificateNextToTheCurrentPair(t *testing.T) {
	t.Parallel()
	h := newHost(t)
	p1, p2 := h.pair(1, t0, day(300)), h.pair(2, day(200), day(500))
	h.install(p1, "")
	if err := certpair.WriteNew(h.files(), certpair.OSFS{}, p2.KeyPEM, p2.CertPEM); err != nil {
		t.Fatal(err)
	}
	for name, mode := range map[string]os.FileMode{"agent.key.new": 0o600, "agent.pem.new": 0o644} {
		info, err := os.Stat(h.path(name))
		if err != nil || info.Mode().Perm() != mode {
			t.Errorf("%s: %v, %v", name, info, err)
		}
	}
	if h.names() != "agent.key agent.key.new agent.pem agent.pem.new" {
		t.Fatalf("files = %s", h.names())
	}
	if got, _ := os.ReadFile(h.path("agent.key.new")); string(got) != string(p2.KeyPEM) {
		t.Fatal("the key was changed")
	}
	if got, _ := os.ReadFile(h.path("agent.key")); string(got) != string(p1.KeyPEM) {
		t.Fatal("the current key was touched")
	}
}

func TestWriteNewIgnoresTheUmask(t *testing.T) {
	h := newHost(t)
	old := umask(0o277)
	defer umask(old)
	if err := certpair.WriteNew(h.files(), certpair.OSFS{}, []byte("k"), []byte("c")); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(h.path("agent.pem.new"))
	if err != nil || info.Mode().Perm() != 0o644 {
		t.Fatalf("%v, %v", info, err)
	}
}

// Scenario: Сбой записи сертификата после записи ключа не оставляет половины пары
func TestWriteNewLeavesNoHalfAPairWhenTheCertificateFails(t *testing.T) {
	t.Parallel()
	h := newHost(t)
	h.install(h.pair(1, t0, day(300)), "")
	err := certpair.WriteNew(h.files(), failing{failWrite: "agent.pem.new"}, []byte("k"), []byte("c"))
	if !errors.Is(err, errDisk) || h.names() != "agent.key agent.pem" {
		t.Fatalf("err = %v, files = %s", err, h.names())
	}
}

// Scenario: Каталог tls недоступен на запись — пара не меняется, ошибка в файле состояния
func TestWriteNewNamesTheDirectoryItCannotWriteTo(t *testing.T) {
	t.Parallel()
	h := newHost(t)
	files := h.files()
	files.KeyFile = filepath.Join(h.dir, "missing", "agent.key")
	err := certpair.WriteNew(files, certpair.OSFS{}, []byte("k"), []byte("c"))
	if err == nil || !strings.Contains(err.Error(), filepath.Join(h.dir, "missing")) {
		t.Fatalf("err = %v", err)
	}
}

func TestPromoteRenamesTheKeyThenTheCertificateOverTheCurrentOnes(t *testing.T) {
	t.Parallel()
	h := newHost(t)
	p2 := h.pair(2, day(200), day(500))
	h.install(h.pair(1, t0, day(300)), "")
	h.install(p2, ".new")
	sel := certpair.Selection{Pair: certpair.Pair{KeyFile: h.path("agent.key.new"), CertFile: h.path("agent.pem.new")}, KeyIsNew: true, CertIsNew: true}
	if err := certpair.Promote(certpair.OSFS{}, h.files(), sel); err != nil {
		t.Fatal(err)
	}
	key, _ := os.ReadFile(h.path("agent.key"))
	cert, _ := os.ReadFile(h.path("agent.pem"))
	if h.names() != "agent.key agent.pem" || string(key) != string(p2.KeyPEM) || string(cert) != string(p2.CertPEM) {
		t.Fatalf("files = %s", h.names())
	}
}

func TestPromoteMovesOnlyTheNewFilesOfTheSelection(t *testing.T) {
	t.Parallel()
	h := newHost(t)
	p2 := h.pair(2, day(200), day(500))
	h.write("agent.key", p2.KeyPEM)
	h.write("agent.pem", h.pair(1, t0, day(300)).CertPEM)
	h.write("agent.pem.new", p2.CertPEM)
	sel := certpair.Selection{Pair: certpair.Pair{KeyFile: h.path("agent.key"), CertFile: h.path("agent.pem.new")}, CertIsNew: true}
	if err := certpair.Promote(certpair.OSFS{}, h.files(), sel); err != nil {
		t.Fatal(err)
	}
	cert, _ := os.ReadFile(h.path("agent.pem"))
	if h.names() != "agent.key agent.pem" || string(cert) != string(p2.CertPEM) {
		t.Fatalf("files = %s", h.names())
	}
}

func TestPromoteReportsAFailedRename(t *testing.T) {
	t.Parallel()
	for _, failed := range []string{"agent.key", "agent.pem"} {
		h := newHost(t)
		h.install(h.pair(1, t0, day(300)), "")
		h.install(h.pair(2, day(200), day(500)), ".new")
		sel := certpair.Selection{Pair: certpair.Pair{KeyFile: h.path("agent.key.new"), CertFile: h.path("agent.pem.new")}, KeyIsNew: true, CertIsNew: true}
		if err := certpair.Promote(failing{failRename: failed}, h.files(), sel); !errors.Is(err, errDisk) {
			t.Fatalf("%s: err = %v", failed, err)
		}
	}
}

// Scenario: Окончательный отказ в подтверждении удаляет новую пару и оставляет текущую
func TestDiscardRemovesTheNewFilesAndTheTempFiles(t *testing.T) {
	t.Parallel()
	h := newHost(t)
	h.install(h.pair(1, t0, day(300)), "")
	h.install(h.pair(2, day(200), day(500)), ".new")
	h.write(certpair.TempPrefix+"1", nil)
	removed := certpair.Discard(certpair.OSFS{}, h.files())
	if h.names() != "agent.key agent.pem" || len(removed) != 3 {
		t.Fatalf("files = %s, removed = %v", h.names(), removed)
	}
	if removed = certpair.Discard(certpair.OSFS{}, h.files()); len(removed) != 0 {
		t.Fatalf("removed = %v", removed)
	}
}

func TestRemoveDeletesTheListedFilesAndReportsThose(t *testing.T) {
	t.Parallel()
	h := newHost(t)
	h.write("a", nil)
	got := certpair.Remove(certpair.OSFS{}, []string{h.path("a"), h.path("absent")})
	if len(got) != 1 || got[0] != h.path("a") || h.names() != "" {
		t.Fatalf("removed = %v, files = %s", got, h.names())
	}
}

func TestOSFSWriteFileLeavesNoTempFileWhenTheRenameFails(t *testing.T) {
	t.Parallel()
	h := newHost(t)
	if err := os.Mkdir(h.path("target"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := (certpair.OSFS{}).WriteFile(h.path("target"), []byte("x"), 0o600); err == nil {
		t.Fatal("writing over a directory succeeded")
	}
	if h.names() != "target" {
		t.Fatalf("files = %s", h.names())
	}
}
