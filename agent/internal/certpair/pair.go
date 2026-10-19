// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

// Package certpair knows the agent's key and certificate files: which pair
// to use at start when a renewal left a second one next to the current
// (docs/specs/agent/agent-certificate-renewal.feature, П21), how a renewed
// pair replaces the current one, and how files are written without ever
// leaving half a pair.
package certpair

import (
	"time"
)

// NewSuffix is appended to tls.key_file and tls.cert_file to name a renewed
// pair that has not replaced the current one yet (П16).
const NewSuffix = ".new"

// Pair is a key file and a certificate file.
type Pair struct {
	KeyFile, CertFile string
}

// Cert is what the selection reads from a certificate.
type Cert struct {
	Serial    string
	NotBefore time.Time
	NotAfter  time.Time
}

// Selection is the outcome of Select.
type Selection struct {
	// Pair is the files to use now.
	Pair Pair
	// KeyIsNew and CertIsNew tell which of them is a ".new" file that has to
	// be confirmed and renamed (see Pending).
	KeyIsNew, CertIsNew bool
	Cert                Cert
	// Stale are the files to delete: renewal files that belong to no usable pair.
	Stale []string
	// Expired is set when every consistent pair has expired; Pair is then the
	// current one.
	Expired *ExpiredError
	// Latest is, when Expired is set, the consistent pair issued last (the
	// latest NotBefore, the current pair on a tie): the one sard-agent check
	// describes (П45). Startup uses Pair.
	Latest Pair
}

// Pending reports whether the selected pair still has to be confirmed and renamed.
func (s Selection) Pending() bool { return s.KeyIsNew || s.CertIsNew }
