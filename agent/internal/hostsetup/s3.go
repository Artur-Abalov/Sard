// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package hostsetup

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/refusal"
)

// S3Address is what a command needs to know of an s3: address (Р28).
type S3Address struct {
	// Host is the host of the storage with its port, as given.
	Host string
	// Bucket is the first component of the path.
	Bucket string
	// Insecure: the scheme is http, requests go without TLS.
	Insecure bool
}

// CheckS3Address reads the forms of restic: s3:<scheme>://<host>[:<port>]/
// <bucket>[/<path>] with https or http, and s3:<host>[:<port>]/<bucket>[/<path>]
// without a scheme (restic takes https). The address is kept as given.
// ADDRESS_INVALID names what is wrong and never shows a credential.
func CheckS3Address(address string) (S3Address, *refusal.Failure) {
	rest := strings.TrimPrefix(address, "s3:")
	if hasControl(rest) {
		return S3Address{}, addressInvalid(address, "it holds a control character")
	}
	scheme, afterScheme, hasScheme := strings.Cut(rest, "://")
	if !hasScheme {
		scheme, afterScheme = "", rest
	}
	host, path, _ := strings.Cut(afterScheme, "/")
	if f := checkS3Host(address, scheme, host); f != nil {
		return S3Address{}, f
	}
	bucket, _, _ := strings.Cut(path, "/")
	if bucket == "" {
		return S3Address{}, addressInvalid(address, "it names no bucket: s3:<host>/<bucket>[/<path>]")
	}
	return S3Address{Host: host, Bucket: bucket, Insecure: scheme == "http"}, nil
}

func checkS3Host(address, scheme, host string) *refusal.Failure {
	if !slices.Contains([]string{"", "https", "http"}, scheme) {
		return addressInvalid(address, "the scheme must be https or http")
	}
	switch {
	case strings.Contains(host, "@"):
		return addressInvalid(address, "it holds credentials (user:password@): the keys of the storage are given with --access-key-id and a secret key source, never in the address")
	case host == "":
		return addressInvalid(address, "it names no host")
	case strings.HasPrefix(host, "-"):
		return addressInvalid(address, "the host must not start with -")
	}
	return checkPort(address, host)
}

// checkPort: a port after the host, if there is one, is 1-65535. An IPv6
// host stands in square brackets.
func checkPort(address, host string) *refusal.Failure {
	name, port, hasPort, ok := splitHost(host)
	if !ok || name == "" {
		return addressInvalid(address, "the host is not valid")
	}
	if hasPort && !validPort(port) {
		return addressInvalid(address, "the port must be 1-65535")
	}
	return nil
}

func validPort(port string) bool {
	n, err := strconv.Atoi(port)
	return err == nil && n >= 1 && n <= 65535
}

// splitHost cuts host[:port]; ok is false for a bracket that does not
// close or text after it that is not :port.
func splitHost(host string) (name, port string, hasPort, ok bool) {
	inner, isV6 := strings.CutPrefix(host, "[")
	if !isV6 {
		name, port, hasPort = strings.Cut(host, ":")
		return name, port, hasPort, true
	}
	name, rest, closed := strings.Cut(inner, "]")
	if !closed {
		return "", "", false, false
	}
	port, hasPort = strings.CutPrefix(rest, ":")
	return name, port, hasPort, rest == "" || hasPort
}

func addressInvalid(address, why string) *refusal.Failure {
	return refusal.Fail(refusal.AddressInvalid, "the address %q is not usable: %s", shown(address), why)
}

// shown is the address for a message: a password in it is hidden; an
// address with credentials that cannot be hidden is not shown at all.
func shown(address string) string {
	redacted := config.RedactURL(address)
	if redacted == address && strings.Contains(address, "@") {
		return "(an address with credentials)"
	}
	return redacted
}

func hasControl(s string) bool {
	return strings.ContainsFunc(s, func(r rune) bool { return r < 0x20 || r == 0x7f })
}

var (
	accessKeyID = regexp.MustCompile(`^[\x21-\x7e]{1,128}$`)
	regionName  = regexp.MustCompile(`^[a-z0-9-]{1,64}$`)
)

func usageError(format string, args ...any) *refusal.Failure {
	return &refusal.Failure{Class: refusal.ClassUsage, Detail: fmt.Sprintf(format, args...)}
}

// CheckAccessKeyID: 1-128 printable ASCII characters without spaces (Р29).
func CheckAccessKeyID(id string) *refusal.Failure {
	if accessKeyID.MatchString(id) {
		return nil
	}
	return usageError("--access-key-id must be 1-128 printable ASCII characters without spaces")
}

// CheckRegion: 1-64 characters a-z 0-9 - (Р29).
func CheckRegion(region string) *refusal.Failure {
	if regionName.MatchString(region) {
		return nil
	}
	return usageError("--region must be 1-64 characters of a-z 0-9 -")
}

// S3SecretKey is the secret key as it goes into the env file (Н16): one
// trailing line break (\n or \r\n) is dropped; any other control character
// is SECRET_INVALID, nothing is left is SECRET_EMPTY. Messages never show
// the key.
func S3SecretKey(raw []byte) ([]byte, *refusal.Failure) {
	key := strings.TrimSuffix(strings.TrimSuffix(string(raw), "\n"), "\r")
	if key == "" {
		return nil, refusal.Fail(refusal.SecretEmpty, "the secret key is empty")
	}
	if hasControl(key) {
		return nil, refusal.Fail(refusal.SecretInvalid, "the secret key holds a control character: it is one line, and only one line break at its end is dropped")
	}
	return []byte(key), nil
}

// S3EnvFile is the content of the env file (Р30).
func S3EnvFile(id string, secretKey []byte, region string) []byte {
	var b strings.Builder
	b.WriteString("AWS_ACCESS_KEY_ID=" + id + "\n")
	b.WriteString("AWS_SECRET_ACCESS_KEY=" + string(secretKey) + "\n")
	if region != "" {
		b.WriteString("AWS_DEFAULT_REGION=" + region + "\n")
	}
	return []byte(b.String())
}

// S3EnvIdentity is the key id and the region an env file holds, without
// the secret key; empty for what is not there.
func S3EnvIdentity(env []byte) (id, region string) {
	for line := range strings.Lines(string(env)) {
		name, value, _ := strings.Cut(strings.TrimRight(line, "\r\n"), "=")
		switch name {
		case "AWS_ACCESS_KEY_ID":
			id = value
		case "AWS_DEFAULT_REGION":
			region = value
		}
	}
	return id, region
}

// providerHosts are the presets of --provider (Р50): the host of the storage
// from the region. The agent holds no list of regions: a region that does
// not exist fails at the storage.
var providerHosts = map[string]string{
	"aws": "s3.%s.amazonaws.com",
	"b2":  "s3.%s.backblazeb2.com",
}

// ExpandProvider is the address restic gets for s3:<bucket>[/<path>] with
// the preset of a provider and a region (Р50). The address names no scheme
// and no host; every misuse is a usage error that names the flag.
func ExpandProvider(provider, region, address string) (string, *refusal.Failure) {
	host, ok := providerHosts[provider]
	if !ok {
		return "", usageError("--provider must be one of: aws, b2")
	}
	if region == "" {
		return "", usageError("--provider %s needs --region", provider)
	}
	if f := CheckRegion(region); f != nil {
		return "", f
	}
	rest := strings.TrimPrefix(address, "s3:")
	if f := checkPresetAddress(rest); f != nil {
		return "", f
	}
	return "s3:https://" + fmt.Sprintf(host, region) + "/" + rest, nil
}

// checkPresetAddress: the bucket and the path of an address given with --provider.
func checkPresetAddress(rest string) *refusal.Failure {
	bucket, _, _ := strings.Cut(rest, "/")
	switch {
	case hasControl(rest):
		return usageError("the address holds a control character")
	case strings.Contains(rest, "://"):
		return usageError("with --provider the address is s3:<bucket>[/<path>], without a scheme or a host")
	case bucket == "":
		return usageError("with --provider the address names no bucket: s3:<bucket>[/<path>]")
	}
	return nil
}
