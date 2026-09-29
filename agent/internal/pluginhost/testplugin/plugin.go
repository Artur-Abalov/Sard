// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

// Package testplugin is a source plugin for tests only; the agent never
// registers it. It backs up a directory by paths or a file as a stream,
// reads an optional secret, and verifies a restored copy byte for byte.
package testplugin

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

//go:embed schema.json
var schema []byte

// Config is the plugin's configuration (schema.json).
type Config struct {
	Mode    string   `json:"mode"`   // "paths" or "stream"
	Source  string   `json:"source"` // a directory (paths) or a file (stream)
	Exclude []string `json:"exclude"`
	Token   string   `json:"token"` // a secret name
}

// Plugin is the test source plugin.
type Plugin struct{}

var (
	_ sdk.Plugin   = Plugin{}
	_ sdk.Verifier = Plugin{}
)

// Name implements sdk.Plugin.
func (Plugin) Name() string { return "test" }

// Version implements sdk.Plugin.
func (Plugin) Version() string { return "0.0.0-test" }

// ConfigSchema implements sdk.Plugin.
func (Plugin) ConfigSchema() []byte { return schema }

func parse(cfg sdk.Config) (Config, error) {
	var c Config
	if err := json.Unmarshal(cfg, &c); err != nil {
		return Config{}, fmt.Errorf("config: %w", err)
	}
	return c, nil
}

// Prepare reads the token secret, which must not be empty, and checks
// that the source exists: a directory or file for paths, a file for a
// stream.
func (Plugin) Prepare(_ context.Context, h sdk.Host, cfg sdk.Config) error {
	c, err := parse(cfg)
	if err != nil {
		return err
	}
	if err := checkToken(h, c.Token); err != nil {
		return err
	}
	info, err := os.Stat(c.Source)
	if err != nil {
		return err
	}
	if c.Mode == "stream" && !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", c.Source)
	}
	return nil
}

func checkToken(h sdk.Host, name string) error {
	if name == "" {
		return nil
	}
	token, err := h.Secret(name)
	if err != nil {
		return err
	}
	if len(token) == 0 {
		return fmt.Errorf("secret %q is empty", name)
	}
	h.Log(sdk.LevelInfo, fmt.Sprintf("secret %q read, %d bytes", name, len(token)))
	return nil
}

// Dump returns the source by paths, or as a stream named after the file.
func (Plugin) Dump(_ context.Context, _ sdk.Host, cfg sdk.Config) (sdk.Dump, error) {
	c, err := parse(cfg)
	if err != nil {
		return sdk.Dump{}, err
	}
	if c.Mode == "stream" {
		return sdk.Dump{Filename: filepath.Base(c.Source)}, nil
	}
	return sdk.Dump{Paths: []string{c.Source}, Excludes: c.Exclude}, nil
}

// chunk is how much Stream copies between progress reports.
const chunk = 64 << 10

// Stream copies the source file to w, checking ctx before every chunk.
func (Plugin) Stream(ctx context.Context, h sdk.Host, cfg sdk.Config, _ sdk.Dump, w io.Writer) error {
	c, err := parse(cfg)
	if err != nil {
		return err
	}
	f, err := os.Open(c.Source)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	return copyChunks(ctx, h, w, f, uint64(info.Size()))
}

func copyChunks(ctx context.Context, h sdk.Host, w io.Writer, r io.Reader, total uint64) error {
	var done uint64
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := io.CopyN(w, r, chunk)
		done += uint64(n)
		if n > 0 {
			h.Progress(done, total)
		}
		if errors.Is(err, io.EOF) {
			return nil
		} else if err != nil {
			return err
		}
	}
}

// Verify compares a restored copy with the source. restic restores paths
// under their absolute path, and a stream as one file in restoredPath.
func (Plugin) Verify(_ context.Context, _ sdk.Host, cfg sdk.Config, restoredPath string) error {
	c, err := parse(cfg)
	if err != nil {
		return err
	}
	if c.Mode == "stream" {
		return sameFile(c.Source, filepath.Join(restoredPath, filepath.Base(c.Source)))
	}
	return sameTree(c.Source, filepath.Join(restoredPath, c.Source), c.Exclude)
}

// sameTree checks every file of want: an excluded one must be absent from
// got, any other one equal. A tree without files is an error.
func sameTree(want, got string, exclude []string) error {
	var files []string
	err := filepath.WalkDir(want, func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			files = append(files, path)
		}
		return err
	})
	if err != nil {
		return err
	}
	return compareFiles(want, got, files, exclude)
}

func compareFiles(want, got string, files, exclude []string) error {
	compared := 0
	for _, path := range files {
		rel, _ := filepath.Rel(want, path)
		check := sameFile
		if excluded(filepath.Base(path), exclude) {
			check = func(_, restored string) error { return absent(restored) }
		} else {
			compared++
		}
		if err := check(path, filepath.Join(got, rel)); err != nil {
			return err
		}
	}
	if compared == 0 {
		return fmt.Errorf("%s has no files to compare", want)
	}
	return nil
}

func excluded(name string, patterns []string) bool {
	return slices.ContainsFunc(patterns, func(p string) bool {
		ok, _ := filepath.Match(p, name)
		return ok
	})
}

func absent(path string) error {
	if _, err := os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("%s was excluded but is restored", path)
	}
	return nil
}

func sameFile(want, got string) error {
	a, err := os.ReadFile(want)
	if err != nil {
		return err
	}
	b, err := os.ReadFile(got)
	if err != nil {
		return err
	}
	if !bytes.Equal(a, b) {
		return fmt.Errorf("%s differs from %s", got, want)
	}
	return nil
}
