// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package repoconnect_test

import (
	"errors"
	"io/fs"
	"os"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/refusal"
	"github.com/Artur-Abalov/sard/agent/internal/repoconnect"
	"github.com/Artur-Abalov/sard/agent/internal/secrets"
)

func givenKey(key string, asked *int) func() ([]byte, *refusal.Failure) {
	return func() ([]byte, *refusal.Failure) {
		*asked++
		return []byte(key), nil
	}
}

const inPlaceEnv = "AWS_ACCESS_KEY_ID=KEY-ID-1\nAWS_SECRET_ACCESS_KEY=OLD\nAWS_DEFAULT_REGION=r1\n"

func TestTheKeysTheOperatorGaveMakeTheEnvFileAndSaySoWhenItIsInPlace(t *testing.T) {
	var asked int
	a, f := repoconnect.ChooseS3Env([]byte(inPlaceEnv), "KEY-ID-1", "r1", true, givenKey("OLD\n", &asked))
	if f != nil || asked != 1 || string(a.Content) != inPlaceEnv || !a.Same {
		t.Fatalf("%+v %v asked %d", a, f, asked)
	}
}

func TestOtherKeysOrNoFileInPlaceAreNotTheSame(t *testing.T) {
	var asked int
	a, f := repoconnect.ChooseS3Env([]byte(inPlaceEnv), "KEY-ID-1", "r1", true, givenKey("NEW", &asked))
	if f != nil || a.Same || !strings.Contains(string(a.Content), "AWS_SECRET_ACCESS_KEY=NEW\n") {
		t.Fatalf("%+v %v", a, f)
	}
	a, _ = repoconnect.ChooseS3Env(nil, "KEY-ID-1", "", true, givenKey("NEW", &asked))
	if a.Same || string(a.Content) != "AWS_ACCESS_KEY_ID=KEY-ID-1\nAWS_SECRET_ACCESS_KEY=NEW\n" {
		t.Fatalf("%+v", a)
	}
}

func TestOnlyTheSecretKeyIsASecretOfTheEnvFile(t *testing.T) {
	var asked int
	a, _ := repoconnect.ChooseS3Env(nil, "KEY-ID-1", "r1", true, givenKey("NEW", &asked))
	if len(a.Secrets) != 1 || a.Secrets[0] != "AWS_SECRET_ACCESS_KEY=NEW" {
		t.Fatalf("secrets %v", a.Secrets)
	}
}

func TestWithoutASourceTheKeysInPlaceForTheSameIDAndRegionAreUsedWithoutAsking(t *testing.T) {
	var asked int
	a, f := repoconnect.ChooseS3Env([]byte(inPlaceEnv), "KEY-ID-1", "r1", false, givenKey("X", &asked))
	if f != nil || asked != 0 || !a.Same || a.Content != nil || len(a.Secrets) != 1 || a.Secrets[0] != "AWS_SECRET_ACCESS_KEY=OLD" {
		t.Fatalf("%+v %v asked %d", a, f, asked)
	}
}

func TestWithoutASourceAnotherIDOrRegionOrNoFileAsksForTheSecretKey(t *testing.T) {
	for _, c := range []struct {
		inPlace    []byte
		id, region string
	}{
		{[]byte(inPlaceEnv), "KEY-ID-2", "r1"},
		{[]byte(inPlaceEnv), "KEY-ID-1", "r2"},
		{[]byte(inPlaceEnv), "KEY-ID-1", ""},
		{nil, "KEY-ID-1", "r1"},
		{nil, "", ""},
	} {
		var asked int
		a, f := repoconnect.ChooseS3Env(c.inPlace, c.id, c.region, false, givenKey("NEW", &asked))
		if f != nil || asked != 1 || a.Same || a.Content == nil {
			t.Errorf("%+v: %+v %v asked %d", c, a, f, asked)
		}
	}
}

func TestAnUnusableSecretKeyIsRefusedAndNeverShown(t *testing.T) {
	var asked int
	_, f := repoconnect.ChooseS3Env(nil, "K", "", true, givenKey("A\nB", &asked))
	if f == nil || f.Reason != refusal.SecretInvalid || strings.Contains(f.Detail, "A\nB") {
		t.Fatalf("%v", f)
	}
	want := refusal.Fail(refusal.SecretMismatch, "differ")
	_, f = repoconnect.ChooseS3Env(nil, "K", "", true, func() ([]byte, *refusal.Failure) { return nil, want })
	if f != want {
		t.Fatalf("%v", f)
	}
}

func reading(err error, data string) repoconnect.Owned {
	return func(string) ([]byte, error) { return []byte(data), err }
}

func TestAFileInPlaceIsRead(t *testing.T) {
	data, found, f := repoconnect.ReadInPlace(reading(nil, "x"), "/p")
	if string(data) != "x" || !found || f != nil {
		t.Fatalf("%q %v %v", data, found, f)
	}
	_, found, f = repoconnect.ReadInPlace(reading(&fs.PathError{Op: "open", Path: "/p", Err: fs.ErrNotExist}, ""), "/p")
	if found || f != nil {
		t.Fatalf("missing: %v %v", found, f)
	}
}

func TestAFileThatCannotBeReadAsASecretIsRejectedWithItsPath(t *testing.T) {
	for _, err := range []error{
		&secrets.Error{Path: "/p"},
		&os.PathError{Op: "open", Path: "/p", Err: errors.New("too many levels of symbolic links")},
	} {
		_, found, f := repoconnect.ReadInPlace(reading(err, ""), "/p")
		if found || f == nil || f.Reason != refusal.SecretFileRejected || f.Class != refusal.ClassUsage || !strings.Contains(f.Detail, "SECRET_FILE_REJECTED") || !strings.Contains(f.Detail, "/p") {
			t.Errorf("%v: %v", err, f)
		}
	}
}

// Л5: A1's own text of a rejected secret file is told as it is.
func TestTheTextOfARejectedSecretFileIsA1sOwn(t *testing.T) {
	err := &secrets.Error{Path: "/p", Key: "k"}
	_, _, f := repoconnect.ReadInPlace(reading(err, ""), "/p")
	if f == nil || f.Detail != "SECRET_FILE_REJECTED: "+err.Error() || strings.Contains(f.Detail, "cannot be read as a secret file") {
		t.Fatalf("%v", f)
	}
	_, _, f = repoconnect.ReadInPlace(reading(errors.New("boom"), ""), "/p")
	if f == nil || f.Detail != "SECRET_FILE_REJECTED: /p cannot be read as a secret file: boom" {
		t.Fatalf("%v", f)
	}
}

// O1: a leftover password file is judged like the env file.
func TestALeftoverPasswordFileThatCannotBeReadAsASecretIsRefusedAndKept(t *testing.T) {
	for _, keysOnly := range []bool{false, true} {
		w := newWorld(t)
		ok(t, os.MkdirAll(w.conn.SecretsDir, 0o700))
		ok(t, os.WriteFile(w.conn.Final, []byte("pw\n"), 0o600))
		w.conn.Owned = func(string) ([]byte, error) {
			return nil, &os.PathError{Op: "open", Path: w.conn.Final, Err: os.ErrPermission}
		}
		w.conn.KeysOnly = keysOnly
		f := w.conn.Connect(t.Context())
		if f == nil || f.Reason != refusal.SecretFileRejected || w.repo.inits != 0 || len(w.repo.files) != 0 {
			t.Errorf("keysOnly %v: %v", keysOnly, f)
		}
		if w.content(w.conn.Final) != "pw\n" {
			t.Error("the file changed")
		}
	}
}

func TestALeftoverPasswordFileThatReadsAsASecretIsUsed(t *testing.T) {
	w := newWorld(t)
	ok(t, os.MkdirAll(w.conn.SecretsDir, 0o700))
	ok(t, os.WriteFile(w.conn.Final, []byte("Q\n"), 0o600))
	w.conn.Owned = func(p string) ([]byte, error) { return os.ReadFile(p) }
	if f := w.conn.Connect(t.Context()); f != nil || w.repo.password != "Q" {
		t.Fatalf("%v %q", f, w.repo.password)
	}
}

func TestTheKeysAreTheSameOnlyForTheSameKeyIDAndRegion(t *testing.T) {
	env := []byte("AWS_ACCESS_KEY_ID=KEY-ID-1\nAWS_SECRET_ACCESS_KEY=s\nAWS_DEFAULT_REGION=r1\n")
	for _, c := range []struct {
		env        []byte
		id, region string
		want       bool
	}{
		{env, "KEY-ID-1", "r1", true},
		{env, "KEY-ID-2", "r1", false},
		{env, "KEY-ID-1", "r2", false},
		{env, "KEY-ID-1", "", false},
		{nil, "KEY-ID-1", "r1", false},
	} {
		if got := repoconnect.HoldsKeys(c.env, c.id, c.region); got != c.want {
			t.Errorf("HoldsKeys(%q, %s, %s) = %v", c.env, c.id, c.region, got)
		}
	}
}
