// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package restic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ErrDamaged is a repository restic check found damaged: exit 1 with the
// fatal message "repository contains errors" (restic 0.19.1). A repository
// restic could not open or read is another error: its summary also counts an
// error, so only the fatal message tells the two apart.
var ErrDamaged = errors.New("repository contains errors")

// CheckReport is what restic check found.
type CheckReport struct {
	// Errors are the messages of restic's error lines, trimmed, in order.
	Errors []string
}

// VerifyData runs `restic check --json --read-data-subset=<percent>%` with
// --no-lock and --no-cache (V5): a key and storage that may only read are
// enough, and nothing is written to the repository. A damaged repository is
// ErrDamaged next to the *ExitError, with what restic found in the report.
func (c *CLI) VerifyData(ctx context.Context, percent int) (CheckReport, error) {
	var report CheckReport
	args := []string{"check", "--json", fmt.Sprintf("--read-data-subset=%d%%", percent)}
	_, err := c.WithReadOnly().runRepo(ctx, call{args: args, stderr: report.collect})
	if exit := (*ExitError)(nil); errors.As(err, &exit) && exit.Cause() == ErrDamaged.Error() {
		err = fmt.Errorf("%w: %w", ErrDamaged, err)
	}
	return report, err
}

// collect keeps the message of an error line of restic check, which carries
// it at the top level, not under "error" as backup does.
func (r *CheckReport) collect(line []byte) {
	var msg struct {
		Type    string `json:"message_type"`
		Message string `json:"message"`
	}
	if json.Unmarshal(line, &msg) != nil || msg.Type != "error" {
		return
	}
	if text := strings.TrimSpace(msg.Message); text != "" {
		r.Errors = append(r.Errors, text)
	}
}

// RestoreVerified runs `restic restore --verify` of the whole snapshot into
// target with --no-lock and --no-cache (V5): restic reads every restored file
// back and compares it with the snapshot. The snapshot goes after "--".
func (c *CLI) RestoreVerified(ctx context.Context, snapshotID, target string) error {
	if snapshotID == "" || target == "" {
		return fmt.Errorf("%w: snapshot and target are required", ErrInvalidRequest)
	}
	args := []string{"restore", "--json", "--verify", "--target", target}
	_, err := c.WithReadOnly().runRepo(ctx, call{args: args, tail: []string{"--", snapshotID}})
	return err
}
