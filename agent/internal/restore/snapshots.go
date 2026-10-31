// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package restore

import (
	"cmp"
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/refusal"
	"github.com/Artur-Abalov/sard/agent/internal/repoinit"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

// The tags Sard puts on a snapshot of a backup (FXs).
const (
	sourceTag = "sard.source"
	runTag    = "sard.run"
	stepTag   = "sard.step"
)

// Lister reads the snapshots of one repository; restic.CLI is one.
type Lister interface {
	Snapshots(ctx context.Context, tags []string) ([]restic.Snapshot, error)
}

// Snapshot is a snapshot of the repository with what Sard wrote on it.
type Snapshot struct {
	ID       string
	ShortID  string
	Time     time.Time // UTC
	Hostname string
	Paths    []string
	// Tags are all tags of the snapshot, as restic gave them.
	Tags []string
	// Source, Run and Step are the values of the tags sard.source, sard.run
	// and sard.step; nil when the tag is missing or its value is empty.
	Source, Run, Step *string
	// Completeness is whether the backup confirmed that it read every file
	// (Р89).
	Completeness Completeness
}

// Completeness of a snapshot, as the tags of the repository tell it.
type Completeness string

const (
	// NotApplicable: the snapshot is not a main snapshot of a Sard step (no
	// sard.run) or belongs to a plugin's own part.
	NotApplicable Completeness = ""
	// Confirmed: a snapshot of the fingerprint is linked to it.
	Confirmed Completeness = "confirmed"
	// Unconfirmed: the backup did not confirm completeness.
	Unconfirmed Completeness = "unconfirmed"
)

// Stamp is the time of the snapshot in UTC, RFC 3339 to the second with "Z" (Н33).
func (s Snapshot) Stamp() string { return s.Time.UTC().Format("2006-01-02T15:04:05Z") }

// List is every snapshot of the repository, whatever host made it, ordered
// by time ascending and, for the same time, by id. A non-empty source keeps
// the snapshots of that source only (Р57).
func List(ctx context.Context, l Lister, source string) ([]Snapshot, error) {
	var tags []string
	if source != "" {
		tags = []string{sourceTag + "=" + source}
	}
	listed, err := l.Snapshots(ctx, tags)
	if err != nil {
		return nil, err
	}
	snapshots := make([]Snapshot, len(listed))
	for i, s := range listed {
		snapshots[i] = fromRestic(s)
	}
	markCompleteness(snapshots)
	slices.SortFunc(snapshots, func(a, b Snapshot) int {
		return cmp.Or(a.Time.Compare(b.Time), strings.Compare(a.ID, b.ID))
	})
	return snapshots, nil
}

func fromRestic(s restic.Snapshot) Snapshot {
	return Snapshot{
		ID: s.ID, ShortID: s.ID[:min(len(s.ID), 8)], Time: s.Time.UTC(), Hostname: s.Hostname, Paths: s.Paths, Tags: s.Tags,
		Source: tagValue(s.Tags, sourceTag), Run: tagValue(s.Tags, runTag), Step: tagValue(s.Tags, stepTag),
	}
}

// tagValue is the value of the tag name=value; nil without the tag or with
// an empty value.
func tagValue(tags []string, name string) *string {
	for _, t := range tags {
		if value, ok := strings.CutPrefix(t, name+"="); ok && value != "" {
			return &value
		}
	}
	return nil
}

// maxSourceLength is the longest --source (Р57).
const maxSourceLength = 128

// CheckSource: 1-128 printable ASCII characters without a space and a
// comma (the agent does not put a comma in a tag).
func CheckSource(source string) error {
	if source == "" || len(source) > maxSourceLength || strings.ContainsFunc(source, notSourceCharacter) {
		return errors.New("--source must be 1-128 printable ASCII characters without a space or a comma")
	}
	return nil
}

func notSourceCharacter(r rune) bool { return r <= ' ' || r >= 0x7f || r == ',' }

// ListFailure explains why the list could not be read: a repository that
// is not there is REPOSITORY_NOT_INITIALIZED, the rest is told as the other
// commands tell it (repoinit.FromRestic).
func ListFailure(ctx context.Context, err error, target repoinit.Target) *refusal.Failure {
	if ctx.Err() == nil && errors.Is(err, restic.ErrNoRepository) {
		return refusal.Fail(refusal.RepositoryNotInitialized, "repository %q is not initialized: nothing is stored at its address; check the address with `sard-agent repo show %s`", target.Name, target.Name)
	}
	return repoinit.FromRestic(ctx, err, target)
}

const (
	fingerprintPartTag = "files.part=fingerprint"
	filesMainTag       = "files." + sdk.MainSnapshotTag + "="
)

// markCompleteness is Р89: reads the list only. A fingerprint snapshot links
// a snapshot when it carries files.part=fingerprint and the full id of it in
// files.main_snapshot.
func markCompleteness(list []Snapshot) {
	linked := map[string]bool{}
	for _, s := range list {
		if !slices.Contains(s.Tags, fingerprintPartTag) {
			continue
		}
		for _, t := range s.Tags {
			if id, ok := strings.CutPrefix(t, filesMainTag); ok {
				linked[id] = true
			}
		}
	}
	for i := range list {
		list[i].Completeness = completenessOf(list[i], linked)
	}
}

func completenessOf(s Snapshot, linked map[string]bool) Completeness {
	switch {
	case s.Run == nil || hasPluginLabel(s.Tags):
		return NotApplicable
	case linked[s.ID]:
		return Confirmed
	default:
		return Unconfirmed
	}
}

// hasPluginLabel: a tag whose key has a dot and does not start with "sard."
// is the label of a plugin's part.
func hasPluginLabel(tags []string) bool {
	return slices.ContainsFunc(tags, func(t string) bool {
		key, _, _ := strings.Cut(t, "=")
		return strings.Contains(key, ".") && !strings.HasPrefix(key, "sard.")
	})
}
