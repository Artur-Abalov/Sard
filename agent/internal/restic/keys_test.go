// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package restic_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/restic"
)

// The shape of restic key list --json (0.19).
const keyListJSON = `[{"current":true,"id":"aaaa1111aaaa1111aaaa1111aaaa1111aaaa1111aaaa1111aaaa1111aaaa1111","userName":"sard:recovery:box","hostName":"src1","created":"2026-10-20T10:00:00Z"},` +
	`{"current":false,"id":"bbbb2222bbbb2222bbbb2222bbbb2222bbbb2222bbbb2222bbbb2222bbbb2222","userName":"","hostName":"","created":"2026-10-21T12:30:00+03:00"}]`

func TestKeysListsTheKeysWithoutTheLock(t *testing.T) {
	f := newFixture(t, map[string]reply{"key": {inline: keyListJSON}})
	got, err := f.build().Keys(context.Background())
	if err != nil || len(got) != 2 {
		t.Fatalf("Keys = %v, %v", got, err)
	}
	if args := f.exec.call("key").Args; !slices.Equal(args, []string{"key", "list", "--json", "--no-lock"}) {
		t.Errorf("args %q", args)
	}
}

func TestKeysReadsTheFieldsOfRestic(t *testing.T) {
	f := newFixture(t, map[string]reply{"key": {inline: keyListJSON}})
	got, _ := f.build().Keys(context.Background())
	first := got[0]
	if !first.Current || first.UserName != "sard:recovery:box" || first.HostName != "src1" ||
		!first.Created.Equal(time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)) || len(first.ID) != 64 {
		t.Errorf("first = %+v", first)
	}
	if got[1].Current || got[1].UserName != "" {
		t.Errorf("second = %+v", got[1])
	}
}

func TestKeysRefusesOutputItCannotRead(t *testing.T) {
	f := newFixture(t, map[string]reply{"key": {inline: "not json"}})
	if _, err := f.build().Keys(context.Background()); !errors.Is(err, restic.ErrBadOutput) {
		t.Fatalf("err = %v", err)
	}
}

func TestKeysReportsTheExitCodeOfRestic(t *testing.T) {
	f := newFixture(t, map[string]reply{"key": {code: 12}})
	if _, err := f.build().Keys(context.Background()); !errors.Is(err, restic.ErrWrongPassword) {
		t.Fatalf("err = %v", err)
	}
}

// restic run as the service user cannot open /proc/self/fd/N of a descriptor
// root created (the process is not dumpable after the uid change), so the new
// password goes over restic's stdin; this guard is the fake-Exec side of the
// e2e shard (HostRecoveryKeyTest, SelfRecoveryKeyTest) that runs it for real.
func TestAddKeyHandsTheNewPasswordToResticOnStdin(t *testing.T) {
	f := newFixture(t, map[string]reply{"key": {readStdin: true}})
	err := f.build().AddKey(context.Background(), restic.NewKey{User: "sard:verifier:vrf-1", Host: "src1", Password: "NEW-PASSWORD"})
	if err != nil {
		t.Fatal(err)
	}
	c := f.exec.call("key")
	want := []string{"key", "add", "--user", "sard:verifier:vrf-1", "--host", "src1"}
	if !slices.Equal(c.Args, want) {
		t.Errorf("args %q, want %q", c.Args, want)
	}
	if string(f.exec.stdin) != "NEW-PASSWORD\n" {
		t.Errorf("stdin held %q, want the password and a line end", f.exec.stdin)
	}
	if len(c.ExtraFiles) != 0 {
		t.Errorf("inherited %v, want none for the new password", c.ExtraFiles)
	}
	if slices.ContainsFunc(c.Args, func(a string) bool { return strings.Contains(a, "NEW-PASSWORD") }) {
		t.Errorf("the password is in the arguments: %q", c.Args)
	}
	if slices.ContainsFunc(c.Env, func(e string) bool { return strings.Contains(e, "NEW-PASSWORD") }) {
		t.Errorf("the password is in the environment: %q", c.Env)
	}
}

func TestAddKeyReportsTheExitCodeOfRestic(t *testing.T) {
	f := newFixture(t, map[string]reply{"key": {code: 1}})
	var exit *restic.ExitError
	err := f.build().AddKey(context.Background(), restic.NewKey{User: "u", Host: "h", Password: "p"})
	if !errors.As(err, &exit) || exit.Code != 1 {
		t.Fatalf("err = %v", err)
	}
}

func TestRemoveKeyNamesTheKeyAfterTheOptionTerminator(t *testing.T) {
	f := newFixture(t, map[string]reply{"key": {}})
	id := strings.Repeat("ab", 32)
	if err := f.build().RemoveKey(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if args := f.exec.call("key").Args; !slices.Equal(args, []string{"key", "remove", "--", id}) {
		t.Errorf("args %q", args)
	}
}

func TestRemoveKeyReportsTheExitCodeOfRestic(t *testing.T) {
	f := newFixture(t, map[string]reply{"key": {code: 12}})
	if err := f.build().RemoveKey(context.Background(), "abcd1234"); !errors.Is(err, restic.ErrWrongPassword) {
		t.Fatalf("err = %v", err)
	}
}

// restic 0.19.1 prints the creation time of a key in the time zone of the host,
// without the zone: "2026-10-21 12:09:21" (measured on the pinned restic).
func TestParseKeyTimeReadsTheLocalTimeOfRestic(t *testing.T) {
	msk := time.FixedZone("MSK", 3*3600)
	for in, want := range map[string]time.Time{
		"2026-10-21 12:09:21":       time.Date(2026, 10, 21, 9, 9, 21, 0, time.UTC),
		"2026-10-20T10:00:00Z":      time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC),
		"2026-10-21T12:30:00+03:00": time.Date(2026, 10, 21, 9, 30, 0, 0, time.UTC),
	} {
		got, err := restic.ParseKeyTime(in, msk)
		if err != nil || !got.Equal(want) {
			t.Errorf("ParseKeyTime(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	if _, err := restic.ParseKeyTime("yesterday", msk); err == nil {
		t.Error("a time restic cannot have printed is no error")
	}
}

func TestKeysReadsTheLocalTimeOfRestic(t *testing.T) {
	f := newFixture(t, map[string]reply{"key": {inline: `[{"current":true,"id":"` + strings.Repeat("ab", 32) + `","userName":"root","hostName":"vm","created":"2026-10-21 12:09:21"}]`}})
	got, err := f.build().Keys(context.Background())
	if err != nil || len(got) != 1 {
		t.Fatalf("Keys = %v, %v", got, err)
	}
	want, _ := restic.ParseKeyTime("2026-10-21 12:09:21", time.Local)
	if !got[0].Created.Equal(want) {
		t.Errorf("created %v, want %v", got[0].Created, want)
	}
}
