// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package testplugin_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/pluginhost/testplugin"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

// host is a fake sdk.Host with one secret.
type host struct {
	secrets  map[string]string
	progress [][2]uint64
	logs     []string
}

func (h *host) Secret(name string) ([]byte, error) {
	v, ok := h.secrets[name]
	if !ok {
		return nil, &sdk.SecretError{Name: name}
	}
	return []byte(v), nil
}

func (h *host) Progress(done, total uint64)  { h.progress = append(h.progress, [2]uint64{done, total}) }
func (h *host) Log(_ sdk.Level, text string) { h.logs = append(h.logs, text) }
func newHost() *host                         { return &host{secrets: map[string]string{"tok": "s3cret", "empty": ""}} }
func write(t *testing.T, path, data string)  { t.Helper(); mustWrite(t, path, []byte(data)) }
func cfg(mode, source string, extra ...string) sdk.Config {
	return sdk.Config(`{"mode":"` + mode + `","source":"` + source + `"` + strings.Join(extra, "") + `}`)
}

func mustWrite(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

var p = testplugin.Plugin{}

func TestMetadata(t *testing.T) {
	if p.Name() != "test" || p.Version() != "0.0.0-test" || !bytes.Contains(p.ConfigSchema(), []byte(`"sard-secret"`)) {
		t.Fatalf("%s %s %s", p.Name(), p.Version(), p.ConfigSchema())
	}
	if _, ok := any(p).(sdk.Verifier); !ok {
		t.Error("the test plugin verifies restored copies")
	}
}

func TestPrepareReadsTheTokenAndChecksTheSource(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "db.sql")
	write(t, file, "x")
	h := newHost()
	ctx := context.Background()
	if err := p.Prepare(ctx, h, cfg("paths", dir, `,"token":"tok"`)); err != nil {
		t.Fatal(err)
	}
	if err := p.Prepare(ctx, h, cfg("stream", file)); err != nil {
		t.Fatal(err)
	}
	if slices.ContainsFunc(h.logs, func(l string) bool { return strings.Contains(l, "s3cret") }) || len(h.logs) != 1 {
		t.Errorf("logs = %q", h.logs)
	}
	for name, c := range map[string]sdk.Config{
		"unknown token":      cfg("paths", dir, `,"token":"nope"`),
		"empty token":        cfg("paths", dir, `,"token":"empty"`),
		"missing source":     cfg("paths", filepath.Join(dir, "none")),
		"stream a directory": cfg("stream", dir),
		"not JSON":           sdk.Config(`{`),
	} {
		if err := p.Prepare(ctx, h, c); err == nil {
			t.Errorf("%s: Prepare succeeded", name)
		}
	}
	if err := p.Prepare(ctx, h, cfg("paths", dir, `,"token":"nope"`)); !errors.Is(err, sdk.ErrUnknownSecret) {
		t.Errorf("unknown token: %v", err)
	}
}

func TestDumpByPaths(t *testing.T) {
	d, err := p.Dump(context.Background(), newHost(), cfg("paths", "/srv/data", `,"exclude":["*.tmp"]`))
	if err != nil || d.Streamed() || !slices.Equal(d.Paths, []string{"/srv/data"}) || !slices.Equal(d.Excludes, []string{"*.tmp"}) {
		t.Fatalf("Dump = %+v, %v", d, err)
	}
}

func TestDumpAsAStreamNamedAfterTheFile(t *testing.T) {
	d, err := p.Dump(context.Background(), newHost(), cfg("stream", "/srv/db.sql"))
	if err != nil || d.Filename != "db.sql" || len(d.Paths) != 0 {
		t.Fatalf("Dump = %+v, %v", d, err)
	}
	if _, err := p.Dump(context.Background(), newHost(), sdk.Config(`[]`)); err == nil {
		t.Fatal("Dump of a config that is not an object")
	}
}

func TestStreamCopiesTheFileAndReportsProgress(t *testing.T) {
	file := filepath.Join(t.TempDir(), "db.sql")
	data := bytes.Repeat([]byte("0123456789abcdef"), 10<<10) // 160 KiB
	mustWrite(t, file, data)
	h := newHost()
	var out bytes.Buffer
	if err := p.Stream(context.Background(), h, cfg("stream", file), sdk.Dump{Filename: "db.sql"}, &out); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out.Bytes(), data) {
		t.Errorf("streamed %d bytes, want %d", out.Len(), len(data))
	}
	total := uint64(len(data))
	if want := [][2]uint64{{64 << 10, total}, {128 << 10, total}, {total, total}}; !slices.Equal(h.progress, want) {
		t.Errorf("progress = %v", h.progress)
	}
}

func TestStreamStopsWhenCancelled(t *testing.T) {
	file := filepath.Join(t.TempDir(), "db.sql")
	mustWrite(t, file, make([]byte, 1<<20))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := p.Stream(ctx, newHost(), cfg("stream", file), sdk.Dump{Filename: "db.sql"}, io.Discard); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("pipe closed") }

func TestStreamErrors(t *testing.T) {
	file := filepath.Join(t.TempDir(), "db.sql")
	mustWrite(t, file, []byte("data"))
	if err := p.Stream(context.Background(), newHost(), cfg("stream", file), sdk.Dump{Filename: "db.sql"}, failingWriter{}); err == nil {
		t.Error("a failed write must fail the stream")
	}
	if err := p.Stream(context.Background(), newHost(), cfg("stream", file+".none"), sdk.Dump{Filename: "db.sql"}, io.Discard); err == nil {
		t.Error("a missing file must fail the stream")
	}
}

func TestVerifyComparesTheRestoredTree(t *testing.T) {
	src, restored := t.TempDir(), t.TempDir()
	write(t, filepath.Join(src, "a.txt"), "a")
	write(t, filepath.Join(src, "sub", "b.txt"), "b")
	write(t, filepath.Join(src, "skip.tmp"), "tmp")
	copyTo := filepath.Join(restored, src)
	write(t, filepath.Join(copyTo, "a.txt"), "a")
	write(t, filepath.Join(copyTo, "sub", "b.txt"), "b")
	ctx := context.Background()
	c := cfg("paths", src, `,"exclude":["*.tmp"]`)
	if err := p.Verify(ctx, newHost(), c, restored); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(copyTo, "sub", "b.txt"), "changed")
	if err := p.Verify(ctx, newHost(), c, restored); err == nil {
		t.Error("a changed file passed verification")
	}
	write(t, filepath.Join(copyTo, "sub", "b.txt"), "b")
	write(t, filepath.Join(copyTo, "skip.tmp"), "tmp")
	if err := p.Verify(ctx, newHost(), c, restored); err == nil {
		t.Error("an excluded file in the restored copy passed verification")
	}
	if err := p.Verify(ctx, newHost(), cfg("paths", src), t.TempDir()); err == nil {
		t.Error("an empty restore passed verification")
	}
	if err := p.Verify(ctx, newHost(), cfg("paths", filepath.Join(src, "none")), restored); err == nil {
		t.Error("a missing source passed verification")
	}
	empty := t.TempDir()
	if err := p.Verify(ctx, newHost(), cfg("paths", empty), restored); err == nil {
		t.Error("a source without files passed verification")
	}
}

func TestVerifyComparesTheRestoredStream(t *testing.T) {
	file, restored := filepath.Join(t.TempDir(), "db.sql"), t.TempDir()
	write(t, file, "dump")
	write(t, filepath.Join(restored, "db.sql"), "dump")
	ctx := context.Background()
	if err := p.Verify(ctx, newHost(), cfg("stream", file), restored); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(restored, "db.sql"), "dum")
	if err := p.Verify(ctx, newHost(), cfg("stream", file), restored); err == nil {
		t.Error("a truncated stream passed verification")
	}
	if err := p.Verify(ctx, newHost(), sdk.Config(`{`), restored); err == nil {
		t.Error("Verify of an invalid config")
	}
}
