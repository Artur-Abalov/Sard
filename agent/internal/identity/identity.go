// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

// Package identity reads who the agent is from its own certificate: tenant
// and agent id from the identity URI, the role from the role URI, both
// names in the subjectAltName (docs/specs/agent/agent-role.feature). The
// role is set by the server when the agent enrolls and never sent back.
package identity

import (
	"crypto/x509"
	"encoding/asn1"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/tlsid"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

// Role is what the server allows the agent to do.
type Role string

const (
	// Source runs BACKUP, RESTORE and RUN.
	Source Role = "source"
	// Verifier runs VERIFY only.
	Verifier Role = "verifier"
)

// Valid reports whether r is one of the two roles.
func (r Role) Valid() bool { return r == Source || r == Verifier }

// Allows reports whether the role may run the action. ACTION_UNSPECIFIED and
// unknown actions pass: the executor's plugin check rejects them as before.
func (r Role) Allows(a agentv1.Action) bool {
	switch a {
	case agentv1.Action_ACTION_BACKUP, agentv1.Action_ACTION_RESTORE, agentv1.Action_ACTION_RUN:
		return r == Source
	case agentv1.Action_ACTION_VERIFY:
		return r == Verifier
	}
	return true
}

// Identity is what the agent's certificate says.
type Identity struct {
	TenantID string
	AgentID  string
	Role     Role
	NotAfter time.Time
}

// RoleError is a certificate without a usable role. Its text names the
// reason, the config key, the file and the way out; never the role URI.
type RoleError struct {
	Path    string
	Missing bool
}

func (e *RoleError) Error() string {
	reason, what := "CERT_ROLE_INVALID", "carries a role that is not exactly sard://roles/source or sard://roles/verifier, or more than one"
	if e.Missing {
		reason, what = "CERT_ROLE_MISSING", "carries no agent role"
	}
	return fmt.Sprintf("tls.cert_file %q: %s: the certificate %s; run `sard-agent enroll --force`", e.Path, reason, what)
}

var identityURI = regexp.MustCompile(`^sard://tenants/([^/]+)/agents/([^/]+)$`)

var sanOID = asn1.ObjectIdentifier{2, 5, 29, 17}

// Read parses the first certificate of the file at path. The role is
// checked after the identity: a certificate without an identity URI is a
// broken file, not a role problem.
func Read(path string, read func(string) ([]byte, error)) (Identity, error) {
	cert, err := tlsid.ReadCertificate(path, read)
	if err != nil {
		return Identity{}, err
	}
	uris := sanURIs(cert)
	id := Identity{NotAfter: cert.NotAfter.UTC()}
	if !findIdentity(uris, &id) {
		return Identity{}, &tlsid.FileError{Key: "tls.cert_file", Path: path, Err: errNoIdentity}
	}
	role, err := roleOf(path, uris)
	id.Role = role
	return id, err
}

var errNoIdentity = fmt.Errorf("no agent identity URI (sard://tenants/<tenant>/agents/<agent>) in the subjectAltName")

func findIdentity(uris []string, id *Identity) bool {
	for _, u := range uris {
		if m := identityURI.FindStringSubmatch(u); m != nil {
			id.TenantID, id.AgentID = m[1], m[2]
			return true
		}
	}
	return false
}

// sanURIs are the URI names of the SAN exactly as written in the
// certificate: x509.Certificate.URIs is parsed and would hide the
// differences that make a role URI inexact.
func sanURIs(cert *x509.Certificate) []string {
	var uris []string
	for _, ext := range cert.Extensions {
		if ext.Id.Equal(sanOID) {
			uris = append(uris, uriNames(ext.Value)...)
		}
	}
	return uris
}

// uriNames are the uniformResourceIdentifier names of a SAN value.
func uriNames(der []byte) []string {
	var names []asn1.RawValue
	if _, err := asn1.Unmarshal(der, &names); err != nil {
		return nil
	}
	var uris []string
	for _, n := range names {
		if n.Class == asn1.ClassContextSpecific && n.Tag == 6 {
			uris = append(uris, string(n.Bytes))
		}
	}
	return uris
}

// roleOf finds the URI names with scheme sard and host roles (case
// insensitive, userinfo and port ignored); exactly one of them, written
// exactly, is the role.
func roleOf(path string, uris []string) (Role, error) {
	found := slices.DeleteFunc(slices.Clone(uris), func(raw string) bool { return !isRoleURI(raw) })
	if len(found) == 0 {
		return "", &RoleError{Path: path, Missing: true}
	}
	if role, ok := exactRole(found); ok {
		return role, nil
	}
	return "", &RoleError{Path: path}
}

// exactRole is the role of a single URI that is exactly sard://roles/<role>.
func exactRole(found []string) (Role, bool) {
	if len(found) != 1 {
		return "", false
	}
	name, ok := strings.CutPrefix(found[0], "sard://roles/")
	role := Role(name)
	return role, ok && role.Valid()
}

func isRoleURI(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && strings.EqualFold(u.Scheme, "sard") && strings.EqualFold(u.Hostname(), "roles")
}
