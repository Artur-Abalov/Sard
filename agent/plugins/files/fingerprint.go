// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package files

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

var (
	_ sdk.Fingerprinter        = Plugin{}
	_ sdk.FingerprintCompleter = Plugin{}
	_ sdk.CopyRestorer         = Plugin{}
)

// Names and values of the facts of a fingerprint of files (V2).
const (
	controlPrefix = "control:"
	hashPrefix    = "sha256:"
	statPrefix    = "stat:"
	factSample    = "sample"
	factFiles     = "files"
	factBytes     = "bytes"

	stateNonEmpty = "nonempty"
	stateEmpty    = "empty"
	stateMissing  = "missing"
	changed       = "changed during the backup"
)

// Limits of the walk of the sample when the host sets none.
const (
	defaultWalkLimit = 10 * time.Minute
	defaultReadLimit = 1 << 30
)

func (p Plugin) tree(root string) fs.FS {
	if root == "" {
		root = "/"
	}
	if p.Tree == nil {
		return os.DirFS(root)
	}
	return p.Tree(root)
}

// rel is the name of an absolute path in a tree rooted at "/".
func rel(path string) string {
	name := strings.TrimPrefix(filepath.Clean(path), "/")
	if name == "" {
		return "."
	}
	return name
}

func newFingerprint(facts []sdk.Fact) sdk.Fingerprint {
	return sdk.Fingerprint{Plugin: "files", Format: sdk.FingerprintFormat, Facts: facts}
}

// Fingerprint implements sdk.Fingerprinter. Of the source it is the state of
// the control paths and a sample of files hashed with SHA-256, each with its
// size and time of change; restic's totals are added after the backup
// (CompleteFingerprint). Of a copy under t.Root it measures the facts of the
// reference: the totals, the control paths and the sampled files.
func (p Plugin) Fingerprint(ctx context.Context, _ sdk.Host, cfg sdk.Config, t sdk.FingerprintTarget) (sdk.Fingerprint, error) {
	if t.Reference != nil {
		return p.measureCopy(ctx, p.tree(t.Root), *t.Reference)
	}
	c, err := parse(cfg)
	if err != nil {
		return sdk.Fingerprint{}, err
	}
	tree := p.tree("")
	var facts []sdk.Fact
	for _, path := range cleaned(c.ControlPaths) {
		facts = append(facts, sdk.Fact{Name: controlPrefix + path, Rule: sdk.RuleExact, Value: controlState(tree, path)})
	}
	sampled, err := p.sample(ctx, tree, c)
	if err != nil {
		return sdk.Fingerprint{}, err
	}
	return newFingerprint(append(facts, sampled...)), nil
}

// controlState is whether a control path holds something: a directory with an
// entry, a file with a byte, anything else that exists.
func controlState(tree fs.FS, path string) string {
	info, err := fs.Stat(tree, rel(path))
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return stateMissing
	case err != nil:
		return "unreadable: " + reason(err)
	case !info.IsDir():
		return fileState(info)
	}
	has, err := firstEntry(tree, path)
	if err != nil {
		return "unreadable: " + reason(err)
	}
	return nonEmpty(has)
}

func fileState(info fs.FileInfo) string {
	return nonEmpty(!info.Mode().IsRegular() || info.Size() > 0)
}

func nonEmpty(yes bool) string {
	if yes {
		return stateNonEmpty
	}
	return stateEmpty
}

// firstEntry reports whether a directory has an entry; it reads one.
func firstEntry(tree fs.FS, path string) (bool, error) {
	f, err := tree.Open(rel(path))
	if err != nil {
		return false, err
	}
	defer func() { _ = f.Close() }()
	dir, ok := f.(fs.ReadDirFile)
	if !ok {
		return false, errors.New("not a directory")
	}
	entries, err := dir.ReadDir(1)
	if len(entries) > 0 || errors.Is(err, io.EOF) {
		return len(entries) > 0, nil
	}
	return false, err
}

// checkControls is the check of the control paths before restic runs (V2):
// a missing or empty one fails the backup.
func checkControls(tree fs.FS, paths []string) []failure {
	var out []failure
	for _, path := range paths {
		if state := controlState(tree, filepath.Clean(path)); state != stateNonEmpty {
			if state == stateMissing {
				state = "no such file or directory"
			}
			out = append(out, failure{path, "control path: " + state})
		}
	}
	return out
}

// CompleteFingerprint implements sdk.FingerprintCompleter: restic's totals are
// added, and each sampled file whose size or time of change differs from that
// at the hash, or that is gone, is no longer compared: restic may have read
// another content.
func (p Plugin) CompleteFingerprint(_ context.Context, _ sdk.Host, _ sdk.Config, d sdk.Dump, totals sdk.BackupTotals) (sdk.Fingerprint, error) {
	var facts []sdk.Fact
	if d.Fingerprint != nil {
		facts = append(facts, d.Fingerprint.Facts...)
	}
	tree := p.tree("")
	for i, f := range facts {
		if !strings.HasPrefix(f.Name, hashPrefix) {
			continue
		}
		path := strings.TrimPrefix(f.Name, hashPrefix)
		was, _ := newFingerprint(facts).Lookup(statPrefix + path)
		if statOf(tree, path) != was.Value {
			facts[i] = sdk.Fact{Name: f.Name, Rule: sdk.RuleInfo, Value: changed}
		}
	}
	return newFingerprint(append(facts, totalFacts(totals)...)), nil
}

// statOf is the size and time of change of a file, "" when it cannot be read.
func statOf(tree fs.FS, path string) string {
	info, err := fs.Stat(tree, rel(path))
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%d %s", info.Size(), info.ModTime().UTC().Format(time.RFC3339Nano))
}

// measureCopy measures, in a copy, the exact facts of the reference.
func (p Plugin) measureCopy(ctx context.Context, tree fs.FS, ref sdk.Fingerprint) (sdk.Fingerprint, error) {
	facts := copyFacts(tree, ref)
	if _, ok := ref.Lookup(factFiles); !ok {
		return newFingerprint(facts), nil
	}
	files, size, err := totalsOf(ctx, tree)
	if err != nil {
		return sdk.Fingerprint{}, err
	}
	return newFingerprint(append(facts, totalFacts(sdk.BackupTotals{Files: files, Bytes: size})...)), nil
}

// totalFacts are the facts of the totals of a snapshot or a copy.
func totalFacts(t sdk.BackupTotals) []sdk.Fact {
	return []sdk.Fact{
		{Name: factFiles, Rule: sdk.RuleExact, Value: strconv.FormatUint(t.Files, 10)},
		{Name: factBytes, Rule: sdk.RuleExact, Value: strconv.FormatUint(t.Bytes, 10)},
	}
}

// copyFacts are the exact facts of the reference, but the totals, measured in the copy.
func copyFacts(tree fs.FS, ref sdk.Fingerprint) []sdk.Fact {
	var facts []sdk.Fact
	for _, f := range ref.Facts {
		if f.Rule != sdk.RuleExact {
			continue
		}
		if value, ok := copyValue(tree, f.Name); ok {
			facts = append(facts, sdk.Fact{Name: f.Name, Rule: sdk.RuleExact, Value: value})
		}
	}
	return facts
}

// copyValue measures one fact of a copy; false for the totals, measured by a walk.
func copyValue(tree fs.FS, name string) (string, bool) {
	switch {
	case strings.HasPrefix(name, controlPrefix):
		return controlState(tree, strings.TrimPrefix(name, controlPrefix)), true
	case strings.HasPrefix(name, hashPrefix):
		return hashOf(tree, strings.TrimPrefix(name, hashPrefix)), true
	}
	return "", false
}

// hashOf is the hex SHA-256 of a file of a copy, or why there is none.
func hashOf(tree fs.FS, path string) string {
	f, err := tree.Open(rel(path))
	if errors.Is(err, fs.ErrNotExist) {
		return stateMissing
	} else if err != nil {
		return "unreadable: " + reason(err)
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "unreadable: " + reason(err)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// totalsOf counts the regular files of a copy and their bytes, as restic
// counts them in the summary of a backup.
func totalsOf(ctx context.Context, tree fs.FS) (files, size uint64, err error) {
	err = fs.WalkDir(tree, ".", func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return context.Cause(ctx)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		files++
		size += uint64(info.Size())
		return nil
	})
	return files, size, err
}

// RestoreInto implements sdk.CopyRestorer: the host restores the whole
// snapshot as a tree into a directory it chooses, and the copy is measured
// there; the target config names nothing the host does not already decide.
func (Plugin) RestoreInto(ctx context.Context, h sdk.RestoreHost, _ sdk.Config, req sdk.RestoreRequest) (sdk.RestoredCopy, error) {
	root, err := h.RestoreTree(ctx, req.SnapshotID)
	if err != nil {
		return sdk.RestoredCopy{}, err
	}
	return sdk.RestoredCopy{Root: root, Result: sdk.RestoreResult{Target: root}}, nil
}
