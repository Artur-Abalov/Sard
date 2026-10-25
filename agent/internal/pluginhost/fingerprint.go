// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package pluginhost

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/Artur-Abalov/sard/agent/internal/restic"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

// Backup is the outcome of a backup: restic's summary of the main snapshot
// and, when the plugin took one, the fingerprint of the source in canonical
// form with its hash.
type Backup struct {
	restic.BackupSummary
	Fingerprint       []byte
	FingerprintSHA256 string
}

const (
	// fingerprintTag is the tag of the snapshot that holds the fingerprint:
	// sard.fp=<hex SHA-256 of the canonical form>.
	fingerprintTag = "sard.fp"
	// fingerprintFile names the fingerprint in its own snapshot.
	fingerprintFile = "sard-fingerprint.json"
)

// sealed is a fingerprint in canonical form with its hash.
type sealed struct {
	bytes []byte
	hash  string
}

func seal(f sdk.Fingerprint) (sealed, error) {
	b, err := f.Encode()
	if err != nil {
		return sealed{}, fmt.Errorf("fingerprint of the plugin: %w", err)
	}
	return sealed{bytes: b, hash: sdk.HashBytes(b)}, nil
}

// withFingerprintTag adds sard.fp to the tags of the main snapshot when the
// fingerprint is known before restic runs. A step that has the tag itself is
// refused: only the agent writes it.
func (s *Source) withFingerprintTag(tags []string, d sdk.Dump) (sealed, []string, error) {
	if slices.ContainsFunc(tags, func(t string) bool { return strings.HasPrefix(t, fingerprintTag+"=") }) {
		return sealed{}, nil, fmt.Errorf("tag %q of the step is the tag of the fingerprint", fingerprintTag)
	}
	if _, completes := s.plugin.(sdk.FingerprintCompleter); completes || d.Fingerprint == nil {
		return sealed{}, tags, nil
	}
	f, err := seal(*d.Fingerprint)
	if err != nil {
		return sealed{}, nil, err
	}
	return f, append(tags, fingerprintTag+"="+f.hash), nil
}

// complete has the plugin complete the fingerprint of the backup, sealed.
func complete(ctx context.Context, c sdk.FingerprintCompleter, h sdk.Host, cfg sdk.Config, d sdk.Dump, out Backup) (sealed, error) {
	f, err := c.CompleteFingerprint(ctx, h, cfg, d, sdk.BackupTotals{Files: out.TotalFiles, Bytes: out.TotalBytes})
	if err != nil {
		return sealed{}, fmt.Errorf("fingerprint: %w", err)
	}
	return seal(f)
}

// withFingerprint ends a backup whose snapshots are stored: the fingerprint
// tagged early is the result's; that of a FingerprintCompleter is completed
// and stored as a snapshot of its own, linked to the main one. A failure keeps
// the main snapshot in the result, without a fingerprint.
func (s *Source) withFingerprint(ctx context.Context, h sdk.Host, cfg sdk.Config, repo restic.Repository, step []string, d sdk.Dump, out Backup, early sealed) (Backup, error) {
	c, ok := s.plugin.(sdk.FingerprintCompleter)
	if !ok {
		out.Fingerprint, out.FingerprintSHA256 = early.bytes, early.hash
		return out, nil
	}
	done, err := complete(ctx, c, h, cfg, d, out)
	if err != nil {
		return out, err
	}
	tags, err := s.withPluginTags(step, map[string]string{"main_snapshot": out.SnapshotID, "part": "fingerprint"})
	if err != nil {
		return out, err
	}
	got, err := repo.Backup(ctx, restic.BackupRequest{Tags: append(tags, fingerprintTag+"="+done.hash), StdinFilename: fingerprintFile, Stdin: func(_ context.Context, w io.Writer) error {
		_, err := w.Write(done.bytes)
		return err
	}}, nil)
	if err != nil {
		return out, &repositoryError{fmt.Errorf("snapshot of %q: %w", fingerprintFile, err)}
	}
	out.AddedBytes += got.AddedBytes
	out.AddedBytesRaw += got.AddedBytesRaw
	out.Fingerprint, out.FingerprintSHA256 = done.bytes, done.hash
	return out, nil
}
