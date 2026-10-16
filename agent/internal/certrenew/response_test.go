// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package certrenew_test

import (
	"crypto/ecdsa"
	"encoding/pem"
	"os"
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
	tenantTwo := spec
	tenantTwo.Tenant = "44444444-4444-4444-4444-444444444444"
	nameless := spec
	nameless.Agent = ""
	rows := map[string]struct {
		answer func(e *env, pub *ecdsa.PublicKey) string
		detail string
	}{
		"the key of the current certificate": {func(e *env, _ *ecdsa.PublicKey) string { return issue(e, e.ca, &e.p1.Key.PublicKey, spec) }, "the certificate is not for the new key"},
		"another agent":                      {func(e *env, pub *ecdsa.PublicKey) string { return issue(e, e.ca, pub, other) }, "the certificate names another agent or role"},
		"another tenant":                     {func(e *env, pub *ecdsa.PublicKey) string { return issue(e, e.ca, pub, tenantTwo) }, "the certificate names another agent or role"},
		"another role":                       {func(e *env, pub *ecdsa.PublicKey) string { return issue(e, e.ca, pub, verifier) }, "the certificate names another agent or role"},
		"no role":                            {func(e *env, pub *ecdsa.PublicKey) string { return issue(e, e.ca, pub, noRole) }, "the certificate carries no usable identity or role"},
		"no identity":                        {func(e *env, pub *ecdsa.PublicKey) string { return issue(e, e.ca, pub, nameless) }, "the certificate carries no usable identity or role"},
		"another CA":                         {func(e *env, pub *ecdsa.PublicKey) string { return issue(e, certtest.NewCA(e.t), pub, spec) }, "the certificate chain does not lead to tls.ca_file"},
		"no PEM":                             {func(*env, *ecdsa.PublicKey) string { return "not a certificate" }, "the answer holds no certificate"},
		"a PEM block that is no certificate": {func(e *env, pub *ecdsa.PublicKey) string {
			return asBlockOfType(e, issue(e, e.ca, pub, spec), "PRIVATE KEY")
		}, "the answer holds no certificate"},
	}
	for name, row := range rows {
		answer := row.answer
		e := newEnv(t)
		e.server.renewFn = func(_ int, pub *ecdsa.PublicKey) (string, error) { return answer(e, pub), nil }
		e.renewNow("certificate renewal failed")
		if e.names() != "agent.key agent.pem ca.pem" || e.read("agent.key") != string(e.p1.KeyPEM) || e.read("agent.pem") != string(e.p1.CertPEM) {
			t.Errorf("%s: files = %s", name, e.names())
		}
		if n := len(e.server.callsOf("confirm")); n != 0 {
			t.Errorf("%s: %d confirmations", name, n)
		}
		if got := e.readState().LastError; got != "RENEWAL_RESPONSE_INVALID: "+row.detail {
			t.Errorf("%s: last_error = %q", name, got)
		}
	}
}

// asBlockOfType is the PEM block of a certificate under another type name.
func asBlockOfType(e *env, certPEM, typ string) string {
	block, _ := pem.Decode([]byte(certPEM))
	if block == nil {
		e.t.Fatal("no PEM block")
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: typ, Bytes: block.Bytes}))
}

func issue(e *env, ca *certtest.CA, pub *ecdsa.PublicKey, spec certtest.Spec) string {
	cert, err := ca.Issue(pub, spec)
	if err != nil {
		e.t.Fatal(err)
	}
	return string(cert)
}

// Scenario: После подтверждения новая пара становится основной
func TestAnAnswerWithAnIntermediateCertificateLeadsToTheCA(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	inter := e.ca.Intermediate(t)
	e.server.renewFn = func(_ int, pub *ecdsa.PublicKey) (string, error) {
		spec := certtest.Spec{Tenant: tenant, Agent: agent, Role: "source", Serial: 9, NotBefore: day(200), NotAfter: day(500)}
		leaf, err := inter.Issue(pub, spec)
		return string(leaf) + string(inter.PEM()), err
	}
	e.renewNow("certificate renewed")
	if got := e.read("agent.pem"); !strings.Contains(got, string(inter.PEM())) {
		t.Fatalf("the chain was not kept: %s", got)
	}
}

func TestACAFileThatIsGoneOrBrokenNamesWhatIsWrong(t *testing.T) {
	t.Parallel()
	for name, row := range map[string]struct {
		setup func(e *env)
		want  string
	}{
		"gone":   {func(e *env) { _ = os.Remove(e.path("ca.pem")) }, "ca.pem"},
		"broken": {func(e *env) { e.write("ca.pem", []byte("not a certificate")) }, "tls.ca_file holds no certificate"},
	} {
		e := newEnv(t)
		row.setup(e)
		e.renewNow("certificate renewal failed")
		got := e.readState().LastError
		if !strings.Contains(got, row.want) || (name == "gone" && strings.Contains(got, "holds no certificate")) {
			t.Errorf("%s: last_error = %q", name, got)
		}
	}
}
