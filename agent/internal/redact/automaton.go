// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package redact

import "slices"

// edge is one trie transition; a node's edges are sorted by label.
type edge struct {
	label byte
	to    int32
}

// automaton is an Aho–Corasick matcher over bytes. Node 0 is the root.
// Transitions are sparse (a dense 256-wide table per node would cost
// hundreds of megabytes for dozens of PEM keys in several encodings),
// except at the root, where most failure chains end.
type automaton struct {
	root  [256]int32
	edges [][]edge
	fail  []int32
	depth []int32
	// longest is the length of the longest pattern that ends at this
	// node (the node itself or a node on its failure chain), 0 if none.
	longest []int32
}

func newAutomaton(patterns [][]byte) *automaton {
	a := &automaton{edges: make([][]edge, 1), depth: make([]int32, 1), longest: make([]int32, 1)}
	for _, p := range patterns {
		a.insert(p)
	}
	a.link()
	return a
}

func (a *automaton) insert(p []byte) {
	node := int32(0)
	for _, b := range p {
		next := a.child(node, b)
		if next < 0 {
			next = int32(len(a.edges))
			a.edges = append(a.edges, nil)
			a.depth = append(a.depth, a.depth[node]+1)
			a.longest = append(a.longest, 0)
			i, _ := slices.BinarySearchFunc(a.edges[node], b, cmpLabel)
			a.edges[node] = slices.Insert(a.edges[node], i, edge{b, next})
		}
		node = next
	}
	a.longest[node] = a.depth[node]
}

// link computes failure links breadth-first, so a node's failure target
// (always shallower) is complete before the node itself.
func (a *automaton) link() {
	a.fail = make([]int32, len(a.edges))
	for _, e := range a.edges[0] {
		a.root[e.label] = e.to
	}
	queue := make([]int32, 0, len(a.edges))
	for _, e := range a.edges[0] {
		queue = append(queue, e.to)
	}
	for len(queue) > 0 {
		node := queue[0]
		queue = queue[1:]
		for _, e := range a.edges[node] {
			f := a.step(a.fail[node], e.label)
			a.fail[e.to] = f
			a.longest[e.to] = max(a.longest[e.to], a.longest[f])
			queue = append(queue, e.to)
		}
	}
}

// step is the transition from node on byte b, following failure links.
func (a *automaton) step(node int32, b byte) int32 {
	for node != 0 {
		if next := a.child(node, b); next >= 0 {
			return next
		}
		node = a.fail[node]
	}
	return a.root[b]
}

// child is node's trie child on b, or -1.
func (a *automaton) child(node int32, b byte) int32 {
	es := a.edges[node]
	if i, ok := slices.BinarySearchFunc(es, b, cmpLabel); ok {
		return es[i].to
	}
	return -1
}

func cmpLabel(e edge, b byte) int { return int(e.label) - int(b) }
