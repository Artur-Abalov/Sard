// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package repoinit

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/Artur-Abalov/sard/agent/internal/refusal"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
)

// The causes restic and the S3 storages print, lower case (Р34). Garage
// answers a wrong secret with Access Denied, so that one is told as a key
// problem too. The exact strings were measured on the F2 stand.
var (
	keyRejectedCauses   = []string{"invalidaccesskeyid", "signaturedoesnotmatch", "does not exist in our records", "signature we calculated does not match"}
	bucketMissingCauses = []string{"nosuchbucket", "bucket does not exist", "bucket not found"}
	accessDeniedCauses  = []string{"access denied", "accessdenied", "forbidden", "permission denied"}
)

func mentions(text string, causes []string) bool {
	text = strings.ToLower(text)
	for _, c := range causes {
		if strings.Contains(text, c) {
			return true
		}
	}
	return false
}

// storageReason is the reason of a refusal of the storage that restic
// gave no known answer for: BACKEND_REFUSED, or for a remote target the
// class its cause belongs to.
func storageReason(err error, t Target) refusal.Reason {
	if !t.Remote {
		return refusal.BackendRefused
	}
	cause, ok := storageWords(err)
	if !ok {
		return refusal.BackendRefused // an error of the agent, not a word of the storage
	}
	switch {
	case mentions(cause, keyRejectedCauses):
		return refusal.S3KeyRejected
	case mentions(cause, bucketMissingCauses):
		return refusal.BucketNotFound
	case mentions(cause, accessDeniedCauses):
		return refusal.StorageAccessDenied
	}
	return refusal.BackendRefused
}

// remoteMessages are the messages of a remote target's refusals: the
// address, the scrubbed cause and what to do.
var remoteMessages = map[refusal.Reason]func(cause string, t Target) string{
	refusal.S3KeyRejected: func(cause string, t Target) string {
		return fmt.Sprintf("the storage at %s does not accept the key: %s; check --access-key-id and the secret key", t.Where, cause)
	},
	refusal.StorageAccessDenied: func(cause string, t Target) string {
		return fmt.Sprintf("the storage at %s denied access: %s; the key id or the secret may be wrong (some storages answer a wrong secret with Access Denied): check both; the key needs read, write and delete in the bucket %s", t.Where, cause, t.Bucket)
	},
	refusal.BucketNotFound: func(cause string, t Target) string {
		return fmt.Sprintf("the bucket %s does not exist at %s: %s; create the bucket, or give a key that may create buckets", t.Bucket, t.Where, cause)
	},
	refusal.BackendUnavailable: func(cause string, t Target) string {
		return fmt.Sprintf("the storage at %s is unreachable: %s; the command can be repeated", t.Where, cause)
	},
	refusal.BackendRefused: func(cause string, t Target) string {
		return fmt.Sprintf("the storage at %s refused: %s", t.Where, cause)
	},
}

// storageWords is what restic said, without the addresses it echoes; false
// when restic said nothing: the error is the agent's own.
func storageWords(err error) (string, bool) {
	var exit *restic.ExitError
	if !errors.As(err, &exit) || exit.Message == "" {
		return "", false
	}
	return addressPattern.ReplaceAllString(exit.Cause(), " "), true
}

// describeRemote is the message of a remote target's refusal. Nil for
// what a remote target explains like any other.
func describeRemote(reason refusal.Reason, cause string, t Target) *refusal.Failure {
	message, ok := remoteMessages[reason]
	if !t.Remote || !ok {
		return nil
	}
	return refusal.Fail(reason, "%s", message(cause, t))
}

// addressPattern finds the addresses restic echoes in its messages: a bucket
// or host named "forbidden" must not decide the class.
var addressPattern = regexp.MustCompile(`(?:s3:)?https?://[^\s"]+|s3:[^\s"]+`)

var retryReason = regexp.MustCompile(`retrying after [^:]*: (.+)$`)

// RetryReason is the reason of the last retry restic printed in its
// stderr ("returned error, retrying after 1.2s: <reason>"); "" if it
// printed none (Р33).
func RetryReason(stderr string) string {
	reason := ""
	for line := range strings.Lines(stderr) {
		if m := retryReason.FindStringSubmatch(strings.TrimRight(line, "\r\n")); m != nil {
			reason = m[1]
		}
	}
	return reason
}
