// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package repoconnect

import (
	"bytes"
	"errors"
	"io/fs"
	"strings"

	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
	"github.com/Artur-Abalov/sard/agent/internal/refusal"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
	"github.com/Artur-Abalov/sard/agent/internal/secrets"
)

// Owned reads a secret file the way A1 allows it to be, as the service
// user owns it (secrets.ReadOwned with the uid of the service user): a
// plain file, no link followed, no group or other bits.
type Owned func(path string) ([]byte, error)

// ReadInPlace reads a file of the service user that is in place. A missing
// file is not found; one that cannot be read as a secret is
// SECRET_FILE_REJECTED and the caller leaves it as it is (R4, ADR 0050).
func ReadInPlace(read Owned, path string) (data []byte, found bool, f *refusal.Failure) {
	data, err := read(path)
	var secretErr *secrets.Error
	switch {
	case err == nil:
		return data, true, nil
	case errors.Is(err, fs.ErrNotExist):
		return nil, false, nil
	case errors.As(err, &secretErr):
		return nil, false, refusal.Fail(refusal.SecretFileRejected, "%s: %v", refusal.SecretFileRejected, secretErr)
	}
	return nil, false, refusal.Fail(refusal.SecretFileRejected, "%s: %s cannot be read as a secret file: %v", refusal.SecretFileRejected, path, err)
}

// S3Access is the keys an s3: repository is tried with (Р29, Р30, Р45).
type S3Access struct {
	// Content is the env file the keys the operator gave make; nil: the env
	// file in place is used as it is.
	Content []byte
	// Same: the env file in place holds these keys already.
	Same bool
	// Secrets are the KEY=VALUE lines whose values no message may show.
	Secrets []string
}

// ChooseS3Env decides which keys the repository is tried with: the ones
// the operator gave (sourceGiven: a flag names the source of the secret
// key), or, when they gave none and the env file in place holds the key id
// and the region asked for, those; otherwise secretKey asks for the secret
// key (the terminal, or SECRET_SOURCE_MISSING). inPlace is the env file
// in place, nil if none.
func ChooseS3Env(inPlace []byte, id, region string, sourceGiven bool, secretKey func() ([]byte, *refusal.Failure)) (S3Access, *refusal.Failure) {
	if !sourceGiven && HoldsKeys(inPlace, id, region) {
		return S3Access{Same: true, Secrets: secretAssignmentsOf(inPlace)}, nil
	}
	raw, f := secretKey()
	if f != nil {
		return S3Access{}, f
	}
	key, f := hostsetup.S3SecretKey(raw)
	if f != nil {
		return S3Access{}, f
	}
	content := hostsetup.S3EnvFile(id, key, region)
	return S3Access{Content: content, Same: bytes.Equal(inPlace, content), Secrets: secretAssignmentsOf(content)}, nil
}

// HoldsKeys: the env file in place has the key id and the region asked for;
// with another key id or region the keys change (П16), whatever the secret.
func HoldsKeys(inPlace []byte, id, region string) bool {
	if inPlace == nil {
		return false
	}
	gotID, gotRegion := hostsetup.S3EnvIdentity(inPlace)
	return gotID == id && gotRegion == region
}

// secretAssignmentsOf are the lines of an env file that hold a secret: all
// but the key id and the region, which are not.
func secretAssignmentsOf(env []byte) []string {
	assignments, _ := restic.ParseEnvFile(env)
	var secret []string
	for _, kv := range assignments {
		switch name, _, _ := strings.Cut(kv, "="); name {
		case "AWS_ACCESS_KEY_ID", "AWS_DEFAULT_REGION":
		default:
			secret = append(secret, kv)
		}
	}
	return secret
}
