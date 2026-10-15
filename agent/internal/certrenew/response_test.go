// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package certrenew_test

import (
	"crypto/ecdsa"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/certtest"
)

// docs/specs/agent/agent-certificate-renewal.feature: P18, the check of the answer.

// Scenario: Негодный ответ на продление не записывается
func TestAnUnfitAnswerToRenewCertificateIsNotWritten(t *testing.T) {
	t.Parallel()
	spec := certtest.Spec{Tenant: tenant, Agent: agent, Role: "source", Serial: 9, NotBefore: day(200), NotAfter: day(500)}
	other := spec
	other.Agent = "33333333-3333-3333-3333-333333333333"
	verifier := spec
	verifier.Role = "verifier"
	noRole := spec
	noRole.Role = ""
	rows := map[string]func(e *env, pub *ecdsa.PublicKey) string{
		"the key of the current certificate": func(e *env, _ *ecdsa.PublicKey) string { return issue(e, e.ca, &e.p1.Key.PublicKey, spec) },
		"another agent":                      func(e *env, pub *ecdsa.PublicKey) string { return issue(e, e.ca, pub, other) },
		"another role":                       func(e *env, pub *ecdsa.PublicKey) string { return issue(e, e.ca, pub, verifier) },
		"no role":                            func(e *env, pub *ecdsa.PublicKey) string { return issue(e, e.ca, pub, noRole) },
		"another CA":                         func(e *env, pub *ecdsa.PublicKey) string { return issue(e, certtest.NewCA(e.t), pub, spec) },
		"no PEM":                             func(*env, *ecdsa.PublicKey) string { return "not a certificate" },
	}
	for name, answer := range rows {
		e := newEnv(t)
		e.server.renewFn = func(_ int, pub *ecdsa.PublicKey) (string, error) { return answer(e, pub), nil }
		e.renewNow("certificate renewal failed")
		if e.names() != "agent.key agent.pem ca.pem" || e.read("agent.key") != string(e.p1.KeyPEM) || e.read("agent.pem") != string(e.p1.CertPEM) {
			t.Errorf("%s: files = %s", name, e.names())
		}
		if n := len(e.server.callsOf("confirm")); n != 0 {
			t.Errorf("%s: %d confirmations", name, n)
		}
		if got := e.readState().LastError; !strings.Contains(got, "RENEWAL_RESPONSE_INVALID") {
			t.Errorf("%s: last_error = %q", name, got)
		}
	}
}

func issue(e *env, ca *certtest.CA, pub *ecdsa.PublicKey, spec certtest.Spec) string {
	cert, err := ca.Issue(pub, spec)
	if err != nil {
		e.t.Fatal(err)
	}
	return string(cert)
}
