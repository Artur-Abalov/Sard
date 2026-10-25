// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Artur Abalov

package sdk

import "context"

// Fingerprinter is implemented by a plugin whose backups carry a fingerprint
// of the source (V-D3). The same Fingerprint measures the source and a
// restored copy of it, so that the two can be compared fact by fact.
type Fingerprinter interface {
	// Fingerprint measures what cfg points at: the source itself, or, with
	// t.Reference set, a restored copy, whose cfg is the Config of the
	// RestoredCopy. On a copy it measures the facts of the reference that
	// cannot be chosen again (the tables counted, the files hashed) and
	// leaves out those whose rule is RuleInfo when they cannot be measured.
	// It only reads.
	Fingerprint(ctx context.Context, h Host, cfg Config, t FingerprintTarget) (Fingerprint, error)
}

// FingerprintTarget says where Fingerprint measures.
type FingerprintTarget struct {
	// Root is the directory a copy of files was restored under, the original
	// absolute paths inside it; "" measures the paths themselves.
	Root string
	// Reference is the fingerprint of the source when a copy is measured;
	// nil when the source is.
	Reference *Fingerprint
}

// BackupTotals is what restic counted in the main snapshot.
type BackupTotals struct {
	// Files and Bytes are the number of files and their size in the snapshot.
	Files, Bytes uint64
}

// FingerprintCompleter is implemented by a Fingerprinter whose fingerprint is
// only known once restic has stored the main snapshot (files that changed
// while it read them). The agent then stores the completed fingerprint as a
// snapshot of its own, tagged sard.fp=<hash> and
// "<plugin>.main_snapshot=<id>", instead of tagging the main snapshot: restic
// cannot add a tag to a snapshot without making a new one.
type FingerprintCompleter interface {
	// CompleteFingerprint turns the draft d.Fingerprint into the fingerprint
	// of the backup. It is called with the Host of the step, after the main
	// snapshot succeeded; an error fails the step.
	CompleteFingerprint(ctx context.Context, h Host, cfg Config, d Dump, totals BackupTotals) (Fingerprint, error)
}

// CopyRestorer is implemented by a Fingerprinter that a restore check can
// restore a copy with (V5): it restores the snapshot into a target given by a
// restore config, valid against RestoreConfigSchema, and says how Fingerprint
// reaches the copy.
type CopyRestorer interface {
	RestoreInto(ctx context.Context, h RestoreHost, target Config, req RestoreRequest) (RestoredCopy, error)
}

// RestoredCopy is a copy restored for a check.
type RestoredCopy struct {
	// Config points Fingerprint at the copy; Root is FingerprintTarget.Root.
	Config Config
	Root   string
	// Result is what the restore achieved.
	Result RestoreResult
}

// CopySizer is implemented by a CopyRestorer that can tell from the
// fingerprint of a source how many bytes a restored copy of it takes, so that
// a restore check knows before it restores whether the copy fits (V5).
type CopySizer interface {
	// CopySize is the size of the copy in bytes; false when f does not say.
	CopySize(f Fingerprint) (uint64, bool)
}
