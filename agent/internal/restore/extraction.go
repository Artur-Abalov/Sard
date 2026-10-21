// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package restore

import (
	"slices"
	"strings"

	"github.com/Artur-Abalov/sard/agent/internal/refusal"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

// Extraction is how repo restore lays out a snapshot: the files a plugin
// planned for it and the lines it tells the operator after them. Without files
// the snapshot is a tree (Р61).
type Extraction struct {
	// Plugin is the plugin that planned the files.
	Plugin string
	// Label says what the snapshot is, for the messages of the agent.
	Label string
	Files []File
	Hints []string
}

// Tree: the snapshot is restored as a tree of files, not laid out by a plugin.
func (e Extraction) Tree() bool { return len(e.Files) == 0 }

// PlanExtraction asks the plugin that owns the snapshot how to lay it out
// (Р60, О3): the owner is the plugin, among extractors, whose name stands before
// the dot of a tag of the snapshot (the host writes the tags of a plugin as
// "<plugin>.<key>=<value>"). A snapshot of no such plugin is a tree. The agent
// knows no plugin by name, nor what a snapshot of one holds: the plugin says.
func PlanExtraction(extractors map[string]sdk.Extractor, list []Snapshot, snap Snapshot, target string) (Extraction, *refusal.Failure) {
	owners := claimants(extractors, snap)
	switch len(owners) {
	case 0:
		return Extraction{}, nil
	case 1:
	default:
		return Extraction{}, refusal.Fail(refusal.PluginFailed, "snapshot %s carries tags of several plugins that lay out files (%s); it cannot be restored, and nothing was written", snap.ShortID, strings.Join(owners, ", "))
	}
	name := owners[0]
	plan, err := extractors[name].PlanExtract(sdk.ExtractRequest{Snapshot: info(snap), Snapshots: infos(list), Target: target})
	if err != nil {
		return Extraction{}, refusal.Fail(refusal.ResticOutputUnexpected, "%s", err)
	}
	if err := plan.Validate(); err != nil {
		return Extraction{}, refusal.Fail(refusal.PluginFailed, "the plan of the plugin %s for snapshot %s cannot be laid out: %v; nothing was written", name, snap.ShortID, err)
	}
	files := make([]File, len(plan.Files))
	for i, f := range plan.Files {
		files[i] = File(f)
	}
	label := plan.Label
	if label == "" {
		label = name
	}
	return Extraction{Plugin: name, Label: label, Files: files, Hints: plan.Hints}, nil
}

// claimants are the names, sorted, of the extractors that have a tag on the snapshot.
func claimants(extractors map[string]sdk.Extractor, snap Snapshot) []string {
	var names []string
	for name := range extractors {
		if slices.ContainsFunc(snap.Tags, func(tag string) bool { return strings.HasPrefix(tag, name+".") }) {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}

func info(s Snapshot) sdk.SnapshotInfo {
	return sdk.SnapshotInfo{ID: s.ID, Time: s.Time, Tags: s.Tags, Paths: s.Paths}
}

func infos(list []Snapshot) []sdk.SnapshotInfo {
	out := make([]sdk.SnapshotInfo, len(list))
	for i, s := range list {
		out[i] = info(s)
	}
	return out
}
