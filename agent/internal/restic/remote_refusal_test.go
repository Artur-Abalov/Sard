// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package restic_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/restic"
)

func fatal(message string) error {
	return fmt.Errorf("restic init: %w", &restic.ExitError{Code: 1, Message: message})
}

// Р34: the class of a refusal of a remote storage is read from what restic
// printed; the first matching class wins.
func TestRemoteRefusalIsReadFromTheWordsOfRestic(t *testing.T) {
	cases := []struct {
		name    string
		message string
		want    restic.RemoteRefusal
	}{
		{"key id", "Fatal: unable to open repository: The Access Key Id you provided does not exist in our records. InvalidAccessKeyId", restic.KeyRejected},
		{"signature", "Fatal: unable to open repository: SignatureDoesNotMatch", restic.KeyRejected},
		{"no bucket", "Fatal: unable to open repository: NoSuchBucket", restic.BucketMissing},
		{"bucket not found", "Fatal: Bucket Not Found", restic.BucketMissing},
		{"denied", "Fatal: unable to open repository: Access Denied.", restic.AccessDenied},
		{"ssh key", "Fatal: unable to open repository: Permission denied (publickey).", restic.AccessDenied},
		{"lock", "Fatal: unable to create lock in backend", restic.AccessDenied},
		{"creation of the bucket refused", "Fatal: create repository: client.MakeBucket: Access Denied.", restic.BucketMissing},
		{"key beats access", "Fatal: InvalidAccessKeyId: access denied", restic.KeyRejected},
		{"other words", "Fatal: something else", restic.NoRemoteRefusal},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := restic.RemoteRefusalOf(fatal(tc.message)); got != tc.want {
				t.Errorf("RemoteRefusalOf = %v, want %v", got, tc.want)
			}
		})
	}
}

// A bucket or host named "forbidden" must not decide the class.
func TestRemoteRefusalIgnoresTheAddressesResticEchoes(t *testing.T) {
	err := fatal(`Fatal: unable to open repository at s3:https://forbidden.example.com/bucket: connection refused`)
	if got := restic.RemoteRefusalOf(err); got != restic.NoRemoteRefusal {
		t.Errorf("RemoteRefusalOf = %v", got)
	}
}

// An error of the agent is not a word of the storage.
func TestRemoteRefusalOfAnErrorWithoutWordsOfResticIsNone(t *testing.T) {
	for _, err := range []error{
		errors.New("access denied"),
		fmt.Errorf("restic: %w", &restic.ExitError{Code: 1}),
		nil,
	} {
		if got := restic.RemoteRefusalOf(err); got != restic.NoRemoteRefusal {
			t.Errorf("RemoteRefusalOf(%v) = %v", err, got)
		}
	}
}

func TestRefusalWordsAreTheCauseWithoutAddresses(t *testing.T) {
	words, ok := restic.RefusalWords(fatal("Fatal: unable to open repository at s3:https://s3.example.com/bkt: Access Denied."))
	if !ok || words != "unable to open repository at   Access Denied." {
		t.Errorf("RefusalWords = %q, %v", words, ok)
	}
	if _, ok := restic.RefusalWords(errors.New("x")); ok {
		t.Error("an error of the agent has no words of restic")
	}
}

func TestBucketCreationRefusedIsToldByTheMakeBucketCause(t *testing.T) {
	if !restic.BucketCreationRefused("create repository: client.MakeBucket: Access Denied.") {
		t.Error("MakeBucket not recognised")
	}
	if restic.BucketCreationRefused("Access Denied.") {
		t.Error("plain denial taken for a bucket creation")
	}
}
