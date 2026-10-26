// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package secretbind_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/secretbind"
	"github.com/Artur-Abalov/sard/agent/internal/secrets"
)

const agentUID = 990

type file struct {
	data []byte
	info secrets.Info
	err  error
}

func reader(files map[string]file) secretbind.ReadFunc {
	return func(path string) ([]byte, secrets.Info, error) {
		f, ok := files[path]
		if !ok {
			return nil, secrets.Info{}, &fs.PathError{Op: "open", Path: path, Err: fs.ErrNotExist}
		}
		return f.data, f.info, f.err
	}
}

// in is the file of a binding of the tests.
func in(name string) string { return "/b/" + name + ".yaml" }

func TestAStoreReadsTheBindingOfASecret(t *testing.T) {
	doc := []byte("plugin: postgresql\ntargets: [db:5432/app]\n")
	for _, uid := range []uint32{0, agentUID} {
		s := secretbind.NewStore(in, agentUID, reader(map[string]file{"/b/pg.yaml": {data: doc, info: secrets.Info{Mode: 0o644, UID: uid}}}))
		b, ok, err := s.Binding("pg")
		if err != nil || !ok || b.Plugin != "postgresql" {
			t.Errorf("owner %d: Binding = %+v, %v, %v", uid, b, ok, err)
		}
	}
}

func TestASecretWithoutAFileHasNoBinding(t *testing.T) {
	s := secretbind.NewStore(in, agentUID, reader(nil))
	if _, ok, err := s.Binding("pg"); ok || err != nil {
		t.Errorf("Binding = %v, %v; want none and no error", ok, err)
	}
}

func TestABindingFileOthersCanChangeIsRefusedWithoutItsPath(t *testing.T) {
	doc := []byte("plugin: postgresql\ntargets: [db]\n")
	cases := map[string]file{
		"writable by group or others": {data: doc, info: secrets.Info{Mode: 0o664}},
		"others can write":            {data: doc, info: secrets.Info{Mode: 0o646}},
		"owned by uid 1000":           {data: doc, info: secrets.Info{Mode: 0o644, UID: 1000}},
		"not a regular file":          {data: doc, info: secrets.Info{Mode: fs.ModeDir | 0o755}},
		"permission denied":           {err: &fs.PathError{Op: "open", Path: "/b/pg.yaml", Err: fs.ErrPermission}},
		"yaml":                        {data: []byte("plugin: [")},
	}
	for want, f := range cases {
		s := secretbind.NewStore(in, agentUID, reader(map[string]file{"/b/pg.yaml": f}))
		_, ok, err := s.Binding("pg")
		if ok || err == nil {
			t.Errorf("%s: Binding = %v, %v; want an error", want, ok, err)
			continue
		}
		msg := err.Error()
		if !strings.Contains(msg, `binding of secret "pg"`) || strings.Contains(msg, "/b/") {
			t.Errorf("%s: error %q must name the secret, not the file", want, msg)
		}
		want = strings.Replace(want, "others can write", "writable by group or others", 1)
		if !strings.Contains(msg, want) {
			t.Errorf("error %q does not say %q", msg, want)
		}
	}
}

func TestReadFileReadsARegularFileAndNotALink(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pg.yaml")
	if err := os.WriteFile(path, []byte("x"), 0o640); err != nil {
		t.Fatal(err)
	}
	data, info, err := secretbind.ReadFile(path)
	if err != nil || string(data) != "x" || info.Mode.Perm() != 0o640 || info.UID != uint32(os.Getuid()) {
		t.Errorf("ReadFile = %q, %+v, %v", data, info, err)
	}
}

func TestReadFileFollowsNoLinkAndReportsAMissingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pg.yaml")
	if err := os.WriteFile(path, []byte("x"), 0o640); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.yaml")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, _, err := secretbind.ReadFile(link); err == nil {
		t.Error("a link was followed")
	}
	if _, _, err := secretbind.ReadFile(filepath.Join(dir, "none")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a missing file: %v", err)
	}
}
