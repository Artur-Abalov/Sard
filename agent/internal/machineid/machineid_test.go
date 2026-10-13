// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package machineid_test

import (
	"bytes"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/machineid"
)

// docs/specs/agent/agent-role.feature, tag @unit.

const (
	r1      = "0123456789abcdef0123456789abcdef"
	tenant1 = "11111111-1111-1111-1111-111111111111"
	tenant3 = "33333333-3333-3333-3333-333333333333"
	etcID   = "/etc/machine-id"
	dbusID  = "/var/lib/dbus/machine-id"
	state   = "/state"
	stored  = state + "/machine-id"
	// vector1 is hex(HMAC-SHA256(key = "sard-machine-id/v1" + tenant1, data = r1)):
	//   printf %s 0123456789abcdef0123456789abcdef |
	//     openssl dgst -sha256 -hmac "sard-machine-id/v111111111-1111-1111-1111-111111111111"
	vector1 = "b6e930a30c108211f9a4711e40469b5a8ae9e670f971d6982b789de8460790c7"
	// vector3 is the same for tenant3.
	vector3 = "8666c761b2e5192f7ca333419818d9b27de55ce328f52b832c63b4c23c34b49d"
)

var hex64 = regexp.MustCompile(`^[0-9a-f]{64}$`)

// fakeFS is a map of files; a value of nil bytes is an existing empty file.
type fakeFS struct {
	files     map[string][]byte
	modes     map[string]fs.FileMode
	dirs      map[string]fs.FileMode
	failRead  map[string]error
	failWrite error
	failMkdir error
	writes    int
}

func newFS(files map[string]string) *fakeFS {
	f := &fakeFS{files: map[string][]byte{}, modes: map[string]fs.FileMode{}, dirs: map[string]fs.FileMode{}, failRead: map[string]error{}}
	for p, c := range files {
		f.files[p] = []byte(c)
	}
	return f
}

func (f *fakeFS) ReadFile(name string) ([]byte, error) {
	if err := f.failRead[name]; err != nil {
		return nil, &fs.PathError{Op: "open", Path: name, Err: err}
	}
	data, ok := f.files[name]
	if !ok {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	return data, nil
}

func (f *fakeFS) MkdirAll(path string, perm fs.FileMode) error {
	if f.failMkdir != nil {
		return &fs.PathError{Op: "mkdir", Path: path, Err: f.failMkdir}
	}
	f.dirs[path] = perm
	return nil
}

func (f *fakeFS) WriteFileAtomic(name string, data []byte, perm fs.FileMode) error {
	if f.failWrite != nil {
		return &fs.PathError{Op: "write", Path: name, Err: f.failWrite}
	}
	f.writes++
	f.files[name], f.modes[name] = data, perm
	return nil
}

// fakeRand returns bytes 1, 2, 3, ... and counts its calls.
type fakeRand struct{ calls int }

func (r *fakeRand) Read(p []byte) (int, error) {
	r.calls++
	for i := range p {
		p[i] = byte(i + 1)
	}
	return len(p), nil
}

const randomHex = "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20"

type fixture struct {
	fs   *fakeFS
	rand *fakeRand
	log  bytes.Buffer
}

func (x *fixture) compute(tenant string) (string, error) {
	logger := slog.New(slog.NewTextHandler(&x.log, nil))
	return machineid.Compute(machineid.Host{
		FS:      x.fs,
		Rand:    x.rand,
		Sources: []string{etcID, dbusID},
		Logger:  logger,
	}, state, tenant)
}

func with(files map[string]string) *fixture {
	return &fixture{fs: newFS(files), rand: &fakeRand{}}
}

// Scenario: machine_id из etc machine-id совпадает с тестовым вектором
func TestMachineIDFromEtcMachineIDMatchesTheTestVector(t *testing.T) {
	x := with(map[string]string{etcID: r1 + "\n"})
	got, err := x.compute(tenant1)
	if err != nil || got != vector1 || !hex64.MatchString(got) {
		t.Fatalf("got %q, %v", got, err)
	}
}

// Scenario: machine_id не равен и не содержит исходный идентификатор
func TestMachineIDDoesNotContainTheRawID(t *testing.T) {
	got, _ := with(map[string]string{etcID: r1}).compute(tenant1)
	if got == r1 || strings.Contains(got, r1) {
		t.Fatalf("got %q", got)
	}
}

// Scenario: machine_id различается между тенантами
func TestMachineIDDiffersBetweenTenants(t *testing.T) {
	a, _ := with(map[string]string{etcID: r1}).compute(tenant1)
	b, err := with(map[string]string{etcID: r1}).compute(tenant3)
	if err != nil || a == b || b != vector3 {
		t.Fatalf("a = %q, b = %q, %v", a, b, err)
	}
}

// Scenario: Источник исходного идентификатора выбирается по порядку
func TestTheRawIDSourceIsChosenInOrder(t *testing.T) {
	ffff := strings.Repeat("f", 32)
	rows := []struct {
		name  string
		files map[string]string
	}{
		{"etc wins", map[string]string{etcID: r1, dbusID: ffff}},
		{"etc missing", map[string]string{dbusID: r1}},
		{"etc empty", map[string]string{etcID: "", dbusID: r1}},
		{"etc blank", map[string]string{etcID: " \t\n\r\n ", dbusID: r1 + "\n"}},
		{"padded", map[string]string{etcID: "  " + r1 + " \n"}},
	}
	for _, row := range rows {
		x := with(row.files)
		got, err := x.compute(tenant1)
		if err != nil || got != vector1 {
			t.Errorf("%s: got %q, %v", row.name, got, err)
		}
		if x.rand.calls != 0 || x.fs.writes != 0 {
			t.Errorf("%s: random calls = %d, writes = %d", row.name, x.rand.calls, x.fs.writes)
		}
	}
}

// Scenario: Случайный идентификатор генерируется один раз и сохраняется
func TestARandomIDIsGeneratedOnceAndStored(t *testing.T) {
	for _, files := range []map[string]string{{}, {etcID: "", dbusID: "\n"}} {
		x := with(files)
		got, err := x.compute(tenant1)
		if err != nil || !hex64.MatchString(got) {
			t.Fatalf("got %q, %v", got, err)
		}
		requireGenerated(t, x)
		again, err := x.compute(tenant1)
		if err != nil || again != got {
			t.Errorf("second run: %q, %v", again, err)
		}
	}
}

// requireGenerated checks the stored file, the one draw of random bytes and the one log line.
func requireGenerated(t *testing.T, x *fixture) {
	t.Helper()
	if string(x.fs.files[stored]) != randomHex || x.fs.modes[stored] != 0o600 {
		t.Errorf("stored %q with mode %v", x.fs.files[stored], x.fs.modes[stored])
	}
	if x.rand.calls != 1 {
		t.Errorf("random calls = %d", x.rand.calls)
	}
	log := x.log.String()
	if strings.Count(log, "generated a random machine id") != 1 || !strings.Contains(log, stored) {
		t.Errorf("log = %q", log)
	}
}

// Scenario: Сохранённый случайный идентификатор переиспользуется
func TestAStoredRandomIDIsReused(t *testing.T) {
	x := with(map[string]string{stored: r1 + r1})
	x.fs.files[stored] = []byte(strings.Repeat("ab", 32))
	before := string(x.fs.files[stored])
	got, err := x.compute(tenant1)
	if err != nil || !hex64.MatchString(got) {
		t.Fatalf("got %q, %v", got, err)
	}
	if x.rand.calls != 0 || x.fs.writes != 0 || string(x.fs.files[stored]) != before {
		t.Errorf("random calls = %d, writes = %d", x.rand.calls, x.fs.writes)
	}
	other := with(map[string]string{stored: before})
	if again, _ := other.compute(tenant1); again != got {
		t.Errorf("the stored id gives %q then %q", got, again)
	}
}

// Scenario: Отсутствующий каталог состояния создаётся с правами 0700
func TestAMissingStateDirIsCreatedOwnerOnly(t *testing.T) {
	x := with(nil)
	if _, err := x.compute(tenant1); err != nil {
		t.Fatal(err)
	}
	if x.fs.dirs[state] != 0o700 {
		t.Errorf("dirs = %v", x.fs.dirs)
	}
}

// Scenario: Непригодный источник идентификатора останавливает агента до подключения
func TestAnUnusableSourceIsRefusedNamingThePath(t *testing.T) {
	denied := errors.New("permission denied")
	rows := []struct {
		name string
		x    *fixture
		path string
	}{
		{"etc unreadable", func() *fixture {
			x := with(map[string]string{dbusID: r1})
			x.fs.failRead[etcID] = denied
			return x
		}(), etcID},
		{"dbus unreadable", func() *fixture {
			x := with(nil)
			x.fs.failRead[dbusID] = denied
			return x
		}(), dbusID},
		{"stored unreadable", func() *fixture {
			x := with(nil)
			x.fs.failRead[stored] = denied
			return x
		}(), stored},
		{"stored not hex", with(map[string]string{stored: "not-hex"}), stored},
		{"stored empty", with(map[string]string{stored: ""}), stored},
		{"stored with a newline", with(map[string]string{stored: strings.Repeat("a", 64) + "\n"}), stored},
		{"stored upper case", with(map[string]string{stored: strings.Repeat("A", 64)}), stored},
		{"stored too short", with(map[string]string{stored: strings.Repeat("a", 63)}), stored},
		{"stored too long", with(map[string]string{stored: strings.Repeat("a", 65)}), stored},
		{"stored with a character below 0", with(map[string]string{stored: strings.Repeat("/", 64)}), stored},
		{"stored with a character above f", with(map[string]string{stored: strings.Repeat("g", 64)}), stored},
		{"cannot write", func() *fixture {
			x := with(nil)
			x.fs.failWrite = denied
			return x
		}(), stored},
		{"cannot create the directory", func() *fixture {
			x := with(nil)
			x.fs.failMkdir = denied
			return x
		}(), state},
	}
	for _, row := range rows {
		requireRefused(t, row.name, row.x, row.path)
	}
}

func requireRefused(t *testing.T, name string, x *fixture, path string) {
	t.Helper()
	got, err := x.compute(tenant1)
	if err == nil || got != "" || !strings.Contains(err.Error(), path) {
		t.Errorf("%s: got %q, %v", name, got, err)
		return
	}
	if strings.Contains(err.Error(), r1) || strings.Contains(err.Error(), "not-hex") || strings.Contains(err.Error(), "\n") {
		t.Errorf("%s: %q leaks or breaks the line", name, err)
	}
	if string(x.fs.files[stored]) == randomHex {
		t.Errorf("%s: the stored id was overwritten", name)
	}
}

func TestACorruptStoredIDIsNotOverwritten(t *testing.T) {
	x := with(map[string]string{stored: "not-hex"})
	_, _ = x.compute(tenant1)
	if string(x.fs.files[stored]) != "not-hex" || x.rand.calls != 0 {
		t.Errorf("stored = %q, random calls = %d", x.fs.files[stored], x.rand.calls)
	}
}

func TestTheLogNeverHoldsTheIDs(t *testing.T) {
	x := with(map[string]string{etcID: r1})
	got, _ := x.compute(tenant1)
	if strings.Contains(x.log.String(), r1) || strings.Contains(x.log.String(), got) {
		t.Errorf("log = %q", x.log.String())
	}
	y := with(nil)
	got, _ = y.compute(tenant1)
	if strings.Contains(y.log.String(), randomHex) || strings.Contains(y.log.String(), got) {
		t.Errorf("log = %q", y.log.String())
	}
}

func TestARandomSourceThatFailsStopsTheAgent(t *testing.T) {
	x := with(nil)
	logger := slog.New(slog.DiscardHandler)
	_, err := machineid.Compute(machineid.Host{FS: x.fs, Rand: failingRand{}, Sources: []string{etcID}, Logger: logger}, state, tenant1)
	if err == nil || !strings.Contains(err.Error(), stored) {
		t.Fatalf("err = %v", err)
	}
}

type failingRand struct{}

func (failingRand) Read([]byte) (int, error) { return 0, errors.New("no entropy") }

// The real file system: atomic write, owner-only modes, no temporary files left.
func TestTheRealFileSystemStoresTheIDOwnerOnly(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "a", "state")
	host := machineid.OS(slog.New(slog.DiscardHandler))
	host.Sources = []string{filepath.Join(dir, "none")}
	first, err := machineid.Compute(host, dir, tenant1)
	if err != nil {
		t.Fatal(err)
	}
	requireOwnerOnly(t, filepath.Join(dir, "machine-id"), 0o600)
	requireOwnerOnly(t, dir, 0o700)
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("entries = %v", entries)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "machine-id")); !hex64.Match(data) {
		t.Errorf("content = %q", data)
	}
	second, err := machineid.Compute(host, dir, tenant1)
	if err != nil || second != first {
		t.Errorf("second = %q, %v", second, err)
	}
}

func requireOwnerOnly(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != want {
		t.Errorf("%s: %v, %v", path, info, err)
	}
}

// A directory in place of /etc/machine-id is not "missing".
func TestADirectoryInPlaceOfASourceIsRefused(t *testing.T) {
	dir := t.TempDir()
	host := machineid.OS(slog.New(slog.DiscardHandler))
	host.Sources = []string{dir}
	_, err := machineid.Compute(host, filepath.Join(t.TempDir(), "state"), tenant1)
	if err == nil || !strings.Contains(err.Error(), dir) {
		t.Fatalf("err = %v", err)
	}
}

// The atomic write leaves nothing behind when it fails.
func TestAFailedAtomicWriteLeavesNoTemporaryFile(t *testing.T) {
	fsys := machineid.OS(slog.New(slog.DiscardHandler)).FS
	dir := t.TempDir()
	if err := fsys.WriteFileAtomic(filepath.Join(dir, "missing", "machine-id"), []byte("x"), 0o600); err == nil {
		t.Error("a write into a missing directory succeeded")
	}
	target := filepath.Join(dir, "machine-id")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := fsys.WriteFileAtomic(target, []byte("x"), 0o600); err == nil {
		t.Error("a write over a directory succeeded")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("entries = %v", entries)
	}
}
