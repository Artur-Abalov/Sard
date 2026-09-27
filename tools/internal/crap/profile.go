// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package crap

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Block is one statement block of a Go coverage profile.
type Block struct {
	File      string // import path of the package + "/" + file name
	StartLine int
	NumStmt   int
	Covered   bool
}

var blockLine = regexp.MustCompile(`^((.+):(\d+)\.\d+,\d+\.\d+) (\d+) (\d+)$`)

// ParseProfile reads a `go test -coverprofile` file. Duplicate entries for
// the same block (from -coverpkg runs) are merged: covered if any is.
func ParseProfile(r io.Reader) ([]Block, error) {
	sc := bufio.NewScanner(r)
	if !sc.Scan() || !strings.HasPrefix(sc.Text(), "mode: ") {
		return nil, fmt.Errorf("coverage profile: missing mode line")
	}
	var set blockSet
	for line := 2; sc.Scan(); line++ {
		m := blockLine.FindStringSubmatch(sc.Text())
		if m == nil {
			return nil, fmt.Errorf("coverage profile: malformed line %d", line)
		}
		set.add(m[1], toBlock(m))
	}
	return set.blocks, sc.Err()
}

// blockSet keeps blocks in input order and merges repeated locations.
type blockSet struct {
	index  map[string]int
	blocks []Block
}

func (s *blockSet) add(location string, b Block) {
	if s.index == nil {
		s.index = map[string]int{}
	}
	if i, ok := s.index[location]; ok {
		s.blocks[i].Covered = s.blocks[i].Covered || b.Covered
		return
	}
	s.index[location] = len(s.blocks)
	s.blocks = append(s.blocks, b)
}

func toBlock(m []string) Block {
	start, _ := strconv.Atoi(m[3])
	stmts, _ := strconv.Atoi(m[4])
	count, _ := strconv.Atoi(m[5])
	return Block{File: m[2], StartLine: start, NumStmt: stmts, Covered: count > 0}
}

// GoRows joins functions with profile blocks. modulePath is the module's
// import path; a function without any blocks counts as 0% covered.
func GoRows(funcs []GoFunc, blocks []Block, modulePath string) []Row {
	byFile := map[string][]Block{}
	for _, b := range blocks {
		byFile[b.File] = append(byFile[b.File], b)
	}
	rows := make([]Row, 0, len(funcs))
	for _, f := range funcs {
		rows = append(rows, Row{
			Name:     f.File + ":" + f.Name,
			CC:       f.CC,
			Coverage: funcCoverage(f, byFile[modulePath+"/"+f.File]),
		})
	}
	return rows
}

func funcCoverage(f GoFunc, blocks []Block) float64 {
	covered, total := 0, 0
	for _, b := range blocks {
		if b.StartLine < f.StartLine || b.StartLine > f.EndLine {
			continue
		}
		total += b.NumStmt
		if b.Covered {
			covered += b.NumStmt
		}
	}
	return ratio(covered, total)
}

var moduleLine = regexp.MustCompile(`(?m)^module\s+"?([^\s"]+)"?\s*$`)

// ModulePath reads the module path from root/go.mod.
func ModulePath(root string) (string, error) {
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return "", err
	}
	m := moduleLine.FindSubmatch(data)
	if m == nil {
		return "", fmt.Errorf("%s/go.mod: no module line", root)
	}
	return string(m[1]), nil
}
