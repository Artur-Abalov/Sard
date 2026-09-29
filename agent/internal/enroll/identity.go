// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package enroll

import (
	"crypto/x509"
	"encoding/pem"
	"os"
	"regexp"

	"github.com/Artur-Abalov/sard/agent/internal/config"
)

// agentURI matches the URI SAN the server's CA puts in an agent
// certificate (docs/adr/0014-local-ca.md): sard://tenants/<t>/agents/<a>.
// It is the authoritative source of agent_id (server/pki/CertificateAuthority.kt).
var agentURI = regexp.MustCompile(`^sard://tenants/[^/]+/agents/([^/]+)$`)

// IdentityStatus is what tls.cert_file and tls.key_file say about an
// existing identity (В19, docs/specs/agent/agent-enroll.feature).
type IdentityStatus struct {
	// Exists is true when tls.cert_file or tls.key_file exists.
	Exists bool
	// AgentID is read from the certificate's URI SAN; empty when it is
	// unknown (no certificate, or one that cannot be parsed or carries no
	// agent URI).
	AgentID string
	// Unreadable is set when tls.cert_file exists but is not a parsable
	// certificate.
	Unreadable bool
}

// InspectIdentity reads the current identity, if any, from files. A lone
// tls.ca_file does not count as an identity and is not read here.
func InspectIdentity(files config.TLS) (IdentityStatus, error) {
	certExists, err := fileExists(files.CertFile)
	if err != nil {
		return IdentityStatus{}, err
	}
	keyExists, err := fileExists(files.KeyFile)
	if err != nil {
		return IdentityStatus{}, err
	}
	return identityFromExistence(files.CertFile, certExists, keyExists), nil
}

func identityFromExistence(certFile string, certExists, keyExists bool) IdentityStatus {
	if !certExists && !keyExists {
		return IdentityStatus{}
	}
	if !certExists {
		return IdentityStatus{Exists: true}
	}
	agentID, readable := agentIDFromCertFile(certFile)
	return IdentityStatus{Exists: true, AgentID: agentID, Unreadable: !readable}
}

func fileExists(path string) (bool, error) {
	if path == "" {
		return false, nil
	}
	_, err := os.Stat(path)
	switch {
	case err == nil:
		return true, nil
	case os.IsNotExist(err):
		return false, nil
	default:
		return false, err
	}
}

// agentIDFromCertFile returns the agent_id from the certificate's URI SAN
// and whether the certificate could be parsed at all.
func agentIDFromCertFile(path string) (agentID string, readable bool) {
	cert, ok := readCertificate(path)
	if !ok {
		return "", false
	}
	return findAgentID(cert), true
}

func readCertificate(path string) (*x509.Certificate, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, false
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, false
	}
	return cert, true
}

func findAgentID(cert *x509.Certificate) string {
	for _, u := range cert.URIs {
		if m := agentURI.FindStringSubmatch(u.String()); m != nil {
			return m[1]
		}
	}
	return ""
}
