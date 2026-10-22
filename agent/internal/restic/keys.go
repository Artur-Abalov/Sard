// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package restic

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Key is one key of a repository as `restic key list --json` prints it.
type Key struct {
	// ID is the full id, 64 hexadecimal characters.
	ID string
	// UserName and HostName are what the key was created with (--user,
	// --host); Sard keeps the purpose and the label of a key in UserName.
	UserName string
	HostName string
	Created  time.Time
	// Current: the password this command opened the repository with belongs
	// to this key.
	Current bool
}

// keyJSON is a key as restic prints it.
type keyJSON struct {
	ID       string `json:"id"`
	UserName string `json:"userName"`
	HostName string `json:"hostName"`
	Created  string `json:"created"`
	Current  bool   `json:"current"`
}

// UnmarshalJSON reads a key of restic key list --json; the creation time is
// the local time of the host that runs restic.
func (k *Key) UnmarshalJSON(data []byte) error {
	var raw keyJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	created, err := ParseKeyTime(raw.Created, time.Local)
	if err != nil {
		return err
	}
	*k = Key{ID: raw.ID, UserName: raw.UserName, HostName: raw.HostName, Created: created, Current: raw.Current}
	return nil
}

// keyTimeLayout is how restic 0.19.1 prints the creation time of a key: the
// local time of its host, with no zone.
const keyTimeLayout = "2006-01-02 15:04:05"

// ParseKeyTime reads the creation time of a key: restic's own layout in the
// zone loc, or RFC 3339 with its zone.
func ParseKeyTime(s string, loc *time.Location) (time.Time, error) {
	if t, err := time.ParseInLocation(keyTimeLayout, s, loc); err == nil {
		return t, nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("the creation time %q of a key is not one restic prints: %w", s, err)
	}
	return t, nil
}

// NewKey is a key restic key add creates.
type NewKey struct {
	User, Host string
	// Password is handed to restic on its stdin, never in an argument, an
	// environment variable or a file.
	Password string
}

// Keys runs `restic key list --json --no-lock`: reading the keys needs no
// lock, so a key that may not write can list them too.
func (c *CLI) Keys(ctx context.Context) ([]Key, error) {
	var out bytes.Buffer
	if _, err := c.runRepo(ctx, call{args: []string{"key", "list", "--json", "--no-lock"}, stdout: collect(&out)}); err != nil {
		return nil, err
	}
	var keys []Key
	if err := json.Unmarshal(out.Bytes(), &keys); err != nil {
		return nil, fmt.Errorf("restic key list: %w: %v", ErrBadOutput, err)
	}
	return keys, nil
}

// AddKey runs `restic key add`; the new password reaches restic on its stdin
// (restic reads one line from a stdin that is not a terminal), so no copy of it
// is left on the host. An inherited descriptor named by --new-password-file
// does not work: restic runs as the service user, and after the uid change its
// process is not dumpable, so it cannot open /proc/self/fd/N of a pipe root made.
func (c *CLI) AddKey(ctx context.Context, key NewKey) error {
	args := []string{"key", "add", "--user", key.User, "--host", key.Host}
	_, err := c.runRepo(ctx, call{args: args, stdin: strings.NewReader(key.Password + "\n")})
	return err
}

// RemoveKey runs `restic key remove`; the id goes after "--".
func (c *CLI) RemoveKey(ctx context.Context, id string) error {
	_, err := c.runRepo(ctx, call{args: []string{"key", "remove"}, tail: []string{"--", id}})
	return err
}
