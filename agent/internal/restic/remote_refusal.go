// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package restic

import (
	"errors"
	"regexp"
	"strings"
)

// RemoteRefusal is the class of a refusal of a remote storage, read from
// what restic printed (Р34).
type RemoteRefusal int

// The classes RemoteRefusalOf tells.
const (
	NoRemoteRefusal RemoteRefusal = iota
	KeyRejected
	BucketMissing
	AccessDenied
)

// The causes restic and the S3 storages print, lower case. Garage answers a
// wrong secret with Access Denied, so that one is told as a key problem too.
// The exact strings were measured on the F2 stand.
var (
	keyRejectedCauses   = []string{"invalidaccesskeyid", "signaturedoesnotmatch", "does not exist in our records", "signature we calculated does not match"}
	bucketMissingCauses = []string{"nosuchbucket", "bucket does not exist", "bucket not found"}
	accessDeniedCauses  = []string{"access denied", "accessdenied", "forbidden", "permission denied", "unable to create lock"}
	// makeBucketCause is how restic's init reports the creation of a bucket
	// it asked for (П30); a refusal of it is the lack of the bucket.
	makeBucketCause = []string{"client.makebucket:"}
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

// refusalClasses maps what a storage said to a class; the first match wins.
var refusalClasses = []struct {
	matches func(cause string) bool
	class   RemoteRefusal
}{
	{func(c string) bool { return mentions(c, keyRejectedCauses) }, KeyRejected},
	// a refused creation of the bucket is the lack of the bucket (П30)
	{func(c string) bool { return BucketCreationRefused(c) && mentions(c, accessDeniedCauses) }, BucketMissing},
	{func(c string) bool { return mentions(c, bucketMissingCauses) }, BucketMissing},
	{func(c string) bool { return mentions(c, accessDeniedCauses) }, AccessDenied},
}

// RemoteRefusalOf is the class of a refusal of a remote storage that err
// says, by the words of restic; NoRemoteRefusal for any other error.
func RemoteRefusalOf(err error) RemoteRefusal {
	cause, ok := refusalWords(err)
	if !ok {
		return NoRemoteRefusal // an error of the agent, not a word of the storage
	}
	for _, c := range refusalClasses {
		if c.matches(cause) {
			return c.class
		}
	}
	return NoRemoteRefusal
}

// BucketCreationRefused tells that the cause is restic's report of a bucket
// it asked the storage to create.
func BucketCreationRefused(cause string) bool { return mentions(cause, makeBucketCause) }

// refusalWords is what restic said, without the addresses it echoes; false
// when restic said nothing: the error is the agent's own.
func refusalWords(err error) (string, bool) {
	var exit *ExitError
	if !errors.As(err, &exit) || exit.Message == "" {
		return "", false
	}
	return addressPattern.ReplaceAllString(exit.Cause(), " "), true
}

// addressPattern finds the addresses restic echoes in its messages: a bucket
// or host named "forbidden" must not decide the class.
var addressPattern = regexp.MustCompile(`(?:s3:)?https?://[^\s"]+|s3:[^\s"]+`)
