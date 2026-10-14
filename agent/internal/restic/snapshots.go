// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package restic

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// Snapshot is one snapshot as `restic snapshots --json` lists it.
type Snapshot struct {
	// ID is the full id, 64 hexadecimal characters.
	ID string `json:"id"`
	// Time is the time of the snapshot, with the zone of the host that made it.
	Time     time.Time `json:"time"`
	Hostname string    `json:"hostname"`
	Paths    []string  `json:"paths"`
	// Tags are all tags of the snapshot, as restic gives them; nil if none.
	Tags []string `json:"tags"`
}

// Snapshots runs `restic snapshots --json --no-lock` (Н26: a read-only key
// is enough, and no lock file is left) for every snapshot of the repository,
// whatever host made it. Each tag of tags is passed as --tag, so only
// snapshots that carry it are listed.
func (c *CLI) Snapshots(ctx context.Context, tags []string) ([]Snapshot, error) {
	args := []string{"snapshots", "--json", "--no-lock"}
	for _, tag := range tags {
		args = append(args, "--tag", tag)
	}
	var out bytes.Buffer
	if _, err := c.runRepo(ctx, call{args: args, stdout: collect(&out)}); err != nil {
		return nil, err
	}
	var snapshots []Snapshot
	if err := json.Unmarshal(out.Bytes(), &snapshots); err != nil {
		return nil, fmt.Errorf("restic snapshots: %w: %v", ErrBadOutput, err)
	}
	return snapshots, nil
}
