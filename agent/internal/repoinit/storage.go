// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package repoinit

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/Artur-Abalov/sard/agent/internal/refusal"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
)

// classReasons maps the class restic's words belong to (Р34) to a Reason.
var classReasons = map[restic.RemoteRefusal]refusal.Reason{
	restic.KeyRejected:   refusal.S3KeyRejected,
	restic.BucketMissing: refusal.BucketNotFound,
	restic.AccessDenied:  refusal.StorageAccessDenied,
}

// storageReason is the reason of a refusal of the storage that restic
// gave no known answer for: BACKEND_REFUSED, or for a remote target the
// class its cause belongs to.
func storageReason(err error, t Target) refusal.Reason {
	if !t.Remote {
		return refusal.BackendRefused
	}
	if reason, ok := classReasons[restic.RemoteRefusalOf(err)]; ok {
		return reason
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
		if t.Directory != "" {
			return fmt.Sprintf("the server of %s denied access: %s; the user of the address needs the right to read, write and delete in the directory %s", t.Where, cause, t.Directory)
		}
		return fmt.Sprintf("the storage at %s denied access: %s; the key id or the secret may be wrong (some storages answer a wrong secret with Access Denied): check both; the key needs read, write and delete in the bucket %s", t.Where, cause, t.Bucket)
	},
	refusal.BucketNotFound: func(cause string, t Target) string {
		if restic.BucketCreationRefused(cause) {
			return fmt.Sprintf("the bucket %s does not exist at %s and the key may not create it: %s; create the bucket, or give a key that may create buckets", t.Bucket, t.Where, cause)
		}
		return fmt.Sprintf("the bucket %s does not exist at %s: %s; create the bucket, or give a key that may create buckets", t.Bucket, t.Where, cause)
	},
	refusal.BackendUnavailable: func(cause string, t Target) string {
		return fmt.Sprintf("the storage at %s is unreachable: %s; the command can be repeated", t.Where, cause)
	},
	refusal.BackendRefused: func(cause string, t Target) string {
		return fmt.Sprintf("the storage at %s refused: %s", t.Where, cause)
	},
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

// RemoteReason is the class of a refusal of a remote storage that err says,
// by what restic printed (Р34): S3_KEY_REJECTED, BUCKET_NOT_FOUND or
// STORAGE_ACCESS_DENIED; "" for any other error.
func RemoteReason(err error) refusal.Reason {
	if reason := storageReason(err, Target{Remote: true}); reason != refusal.BackendRefused {
		return reason
	}
	return ""
}
