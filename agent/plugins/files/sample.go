// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package files

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"math/rand/v2"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

// walk is one walk of the paths for the sample: a reservoir of k files chosen
// uniformly among the files restic stores (Algorithm R).
type walk struct {
	p        Plugin
	ctx      context.Context
	tree     fs.FS
	exclude  excluder
	oneFS    bool
	maxBytes int64
	k        int
	deadline time.Time

	seen     int
	chosen   []string
	timedOut bool
}

func (p Plugin) now() time.Time {
	if p.Now == nil {
		return time.Now()
	}
	return p.Now()
}

func (p Plugin) intN(n int) int {
	if p.Rand == nil {
		return rand.IntN(n)
	}
	return p.Rand(n)
}

func (p Plugin) walkLimit() time.Duration {
	if p.WalkLimit == 0 {
		return defaultWalkLimit
	}
	return p.WalkLimit
}

func (p Plugin) readLimit() int64 {
	if p.ReadLimit == 0 {
		return defaultReadLimit
	}
	return p.ReadLimit
}

// sample walks the paths, chooses the files and hashes them. What it could not
// do in time or within the read limit is said in the fact "sample"; only a
// cancelled step is an error.
func (p Plugin) sample(ctx context.Context, tree fs.FS, c config) ([]sdk.Fact, error) {
	if c.sampleFiles() == 0 {
		return nil, nil
	}
	w := &walk{p: p, ctx: ctx, tree: tree, exclude: newExcluder(c.Exclude), oneFS: c.OneFileSystem,
		maxBytes: c.sampleMaxFileBytes(), k: c.sampleFiles(), deadline: p.now().Add(p.walkLimit())}
	for _, root := range cleaned(c.Paths) {
		w.walkRoot(root)
		if ctx.Err() != nil {
			return nil, context.Cause(ctx)
		}
	}
	return w.hash()
}

// walkRoot walks one listed path; what cannot be read is left out of the sample.
func (w *walk) walkRoot(root string) {
	dev, _ := device(w.tree, root)
	_ = fs.WalkDir(w.tree, rel(root), func(name string, d fs.DirEntry, err error) error {
		return w.visit("/"+name, d, err, dev)
	})
}

// visit judges one entry of the walk; an entry that cannot be read is skipped.
func (w *walk) visit(abs string, d fs.DirEntry, err error, dev uint64) error {
	switch {
	case w.ctx.Err() != nil:
		return fs.SkipAll
	case !w.p.now().Before(w.deadline):
		w.timedOut = true
		return fs.SkipAll
	case err != nil || w.exclude.excludes(abs):
		return skip(d)
	case d.IsDir():
		return w.descend(abs, dev)
	}
	w.offer(abs, d)
	return nil
}

// skip leaves out an entry: a whole directory, or one file.
func skip(d fs.DirEntry) error {
	if d != nil && d.IsDir() {
		return fs.SkipDir
	}
	return nil
}

// descend: with one_file_system, restic does not enter another mount.
func (w *walk) descend(abs string, dev uint64) error {
	if w.oneFS {
		if other, ok := device(w.tree, abs); ok && other != dev {
			return fs.SkipDir
		}
	}
	return nil
}

// device is the file system a path is on, when the tree tells it.
func device(tree fs.FS, abs string) (uint64, bool) {
	info, err := fs.Stat(tree, rel(abs))
	if err != nil {
		return 0, false
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return st.Dev, true
}

// eligible: a regular file within the size limit whose name is UTF-8.
func (w *walk) eligible(abs string, d fs.DirEntry) bool {
	if !d.Type().IsRegular() || !utf8.ValidString(abs) {
		return false
	}
	info, err := d.Info()
	return err == nil && info.Size() <= w.maxBytes
}

// offer puts a regular file into the reservoir.
func (w *walk) offer(abs string, d fs.DirEntry) {
	if !w.eligible(abs, d) {
		return
	}
	w.seen++
	if len(w.chosen) < w.k {
		w.chosen = append(w.chosen, abs)
		return
	}
	if j := w.p.intN(w.seen); j < w.k {
		w.chosen[j] = abs
	}
}

// hash hashes the chosen files within the read limit, each with the size and
// time of change seen around the hash.
func (w *walk) hash() ([]sdk.Fact, error) {
	var facts []sdk.Fact
	budget := w.p.readLimit()
	hashed, overBudget := 0, false
	for _, abs := range w.chosen {
		if w.ctx.Err() != nil {
			return nil, context.Cause(w.ctx)
		}
		h, ok := hashStable(w.tree, abs, budget)
		if h.size > budget {
			overBudget = true
		}
		if !ok {
			continue
		}
		budget -= h.size
		hashed++
		facts = append(facts, sdk.Fact{Name: hashPrefix + abs, Rule: sdk.RuleExact, Value: h.sum},
			sdk.Fact{Name: statPrefix + abs, Rule: sdk.RuleInfo, Value: h.stat})
	}
	return append(facts, sdk.Fact{Name: factSample, Rule: sdk.RuleInfo, Value: w.summary(hashed, overBudget)}), nil
}

func (w *walk) summary(hashed int, overBudget bool) string {
	s := fmt.Sprintf("%d of %d files hashed", hashed, w.seen)
	if w.timedOut {
		s += "; the walk stopped at its time limit"
	}
	if overBudget {
		s += "; the read limit was reached"
	}
	return s
}

// hashed is a file of the sample: its SHA-256, size and time of change.
type hashed struct {
	sum, stat string
	size      int64
}

// hashStable hashes a file whose size is within budget; false when it cannot
// be read, is over the budget or changed while it was read.
func hashStable(tree fs.FS, abs string, budget int64) (hashed, bool) {
	info, err := fs.Stat(tree, rel(abs))
	if err != nil {
		return hashed{}, false
	}
	h := hashed{size: info.Size()}
	if h.size > budget {
		return h, false
	}
	before := statOf(tree, abs)
	f, err := tree.Open(rel(abs))
	if err != nil {
		return h, false
	}
	defer func() { _ = f.Close() }()
	digest := sha256.New()
	if _, err := io.Copy(digest, f); err != nil {
		return h, false
	}
	h.sum, h.stat = hex.EncodeToString(digest.Sum(nil)), statOf(tree, abs)
	return h, before == h.stat
}
