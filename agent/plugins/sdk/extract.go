// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Artur Abalov

package sdk

import (
	"fmt"
	"strings"
)

// Extractor is implemented by a plugin whose snapshots the operator restores
// by hand: "sard-agent repo restore" on the host, without the server. The
// plugin says which files of which snapshots to lay out and what the operator
// runs next; the agent lays them out, with the guarantees of a write to the
// host (a file of its own, the rights set before the content, all or nothing).
//
// The agent asks the plugin whose name stands before the dot of a tag of the
// snapshot ("<plugin>.<key>=<value>", the tags the agent wrote from Dump.Tags).
// A snapshot with no such tag is restored as a tree of files.
type Extractor interface {
	// PlanExtract plans the files of the snapshot req.Snapshot. A plan without
	// files says that the snapshot is a tree: the agent restores it as one. An
	// error refuses the restore before anything is written; its text reaches
	// the operator, so it holds no secret.
	PlanExtract(req ExtractRequest) (ExtractPlan, error)
}

// ExtractRequest is the snapshot to plan and what is known around it.
type ExtractRequest struct {
	// Snapshot is the snapshot the operator named.
	Snapshot SnapshotInfo
	// Snapshots are all the snapshots of the repository, the companions of
	// Snapshot among them (e.g. the global objects of a database).
	Snapshots []SnapshotInfo
	// Target is the absolute path of the directory the files are laid out in,
	// for the words of the hints.
	Target string
}

// ExtractPlan is what the agent lays out.
type ExtractPlan struct {
	// Label says what the snapshot is, in the words of the operator ("PostgreSQL"
	// for "a PostgreSQL snapshot"); the agent uses it in its own messages. Empty:
	// the agent names the plugin.
	Label string
	// Files are written into the root of the target, in this order.
	Files []ExtractFile
	// Hints are lines the agent prints after the files: what the operator
	// does with them next. The agent runs nothing of it.
	Hints []string
}

// ExtractFile is a file of a snapshot that appears in the target under Name.
type ExtractFile struct {
	// SnapshotID is the full id of the snapshot that holds the file.
	SnapshotID string
	// Path is the path of the file inside that snapshot.
	Path string
	// Name is the name in the target: one component, up to 255 bytes, neither
	// empty, "." nor "..", without "/" and NUL.
	Name string
}

// maxName is the longest name of a file, in bytes.
const maxName = 255

// Validate checks the plan against the rules of ExtractPlan; the agent does it
// before it writes anything, because the names come from a plugin and are
// written to the host.
func (p ExtractPlan) Validate() error {
	seen := map[string]bool{}
	for i, f := range p.Files {
		if f.SnapshotID == "" {
			return fmt.Errorf("file %d has no snapshot", i)
		}
		if f.Path == "" {
			return fmt.Errorf("file %d has no path", i)
		}
		if !validFileName(f.Name) {
			return fmt.Errorf("file %d: the name %q is not a name of a file in the root of the target", i, f.Name)
		}
		if seen[f.Name] {
			return fmt.Errorf("the name %q is used twice", f.Name)
		}
		seen[f.Name] = true
	}
	return nil
}

func validFileName(name string) bool {
	return name != "" && len(name) <= maxName && name != "." && name != ".." && !strings.ContainsAny(name, "/\x00")
}
