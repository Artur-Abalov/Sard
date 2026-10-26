// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package postgresql_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/secretbind"
)

// The tests of S11 (docs/specs/agent/secret-binding.feature): the secret is
// bound on the agent host to a plugin and to targets, and the plugin sends the
// password only over SCRAM, and to a server on the network over TLS that
// checks the certificate, unless the binding allows less.

var errBroken = errors.New(`binding of secret "pg-app": the file has mode -rw-rw-rw-: it is writable by group or others`)

// noLeak: the password is in no message and no line of the log.
func noLeak(t *testing.T, r *rig, msg string) {
	t.Helper()
	if strings.Contains(msg, P) || strings.Contains(r.sink.text(), P) {
		t.Errorf("the password leaked: message %q, log %q", msg, r.sink.text())
	}
}

// Scenario 1: a target outside the binding fails the step before anything runs.
func TestATargetOutsideTheBindingFailsTheStepBeforeAnyProgramRuns(t *testing.T) {
	cases := map[string]struct {
		bind  *secretbind.Binding
		cfg   map[string]any
		words []string
	}{
		"another host": {nil, k(o{"host": "evil.example", "tls_mode": "verify-full"}),
			[]string{`secret "pg-app" is not allowed for postgresql at evil.example:5432/app`, "sudo sard-agent secret set pg-app --plugin postgresql --target evil.example:5432/app"}},
		"another port": {binding(t, "db:5432/app"), k(o{"port": 6432}),
			[]string{"is not allowed for postgresql at db:6432/app"}},
		"another database": {binding(t, "db:5432/app"), k(o{"database": "billing"}),
			[]string{"is not allowed for postgresql at db:5432/billing"}},
		"no binding": {nil, k(),
			[]string{`secret "pg-app" has no binding on this host`, "sudo sard-agent secret set pg-app --plugin postgresql --target db:5432/app"}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			if c.bind != nil || name == "no binding" {
				r.bindings.bind(c.bind)
			}
			res := r.backup(c.cfg)
			want(t, res, failed)
			mentions(t, res.GetMessage(), c.words...)
			if r.proc.started() != 0 {
				t.Errorf("programs ran: %d", r.proc.started())
			}
			noLeak(t, r, res.GetMessage())
		})
	}
}

// Scenario 1: a secret bound to another plugin is not given to this one.
func TestASecretBoundToAnotherPluginIsNotGiven(t *testing.T) {
	r := newRig(t)
	b, err := secretbind.New("files", []string{"db"})
	if err != nil {
		t.Fatal(err)
	}
	r.bindings.bind(&b)
	res := r.backup(k())
	want(t, res, failed)
	mentions(t, res.GetMessage(), `secret "pg-app" is bound to the plugin files, not postgresql`)
	if r.proc.started() != 0 {
		t.Errorf("programs ran: %d", r.proc.started())
	}
}

// Scenario 1: a binding the agent cannot judge gives nothing.
func TestABrokenBindingGivesNothing(t *testing.T) {
	r := newRig(t)
	r.bindings.err = errBroken
	res := r.backup(k())
	want(t, res, failed)
	mentions(t, res.GetMessage(), errBroken.Error())
	if r.proc.started() != 0 {
		t.Errorf("programs ran: %d", r.proc.started())
	}
}

// Scenario 2: SCRAM is required over TCP, with channel binding over TLS; the
// binding may allow weaker methods.
func TestTheServerMustAskForSCRAMUnlessTheBindingAllowsMore(t *testing.T) {
	cases := []struct {
		name     string
		allow    func(b *secretbind.Binding)
		cfg      map[string]any
		auth     string
		channel  string
		rootCert string
	}{
		{"tcp, verify-full", nil, kg(o{"tls_mode": "verify-full"}), "scram-sha-256", "require", "system"},
		{"tcp, no TLS", nil, kg(), "scram-sha-256", "", ""},
		{"md5 allowed", func(b *secretbind.Binding) { b.Auth = []string{"scram-sha-256", "md5"} }, kg(o{"tls_mode": "verify-full"}), "scram-sha-256,md5", "", "system"},
		{"no channel binding allowed", func(b *secretbind.Binding) { b.AllowNoChannelBinding = true }, kg(o{"tls_mode": "verify-full"}), "scram-sha-256", "", "system"},
		{"socket", nil, kg(o{"host": "/var/run/postgresql"}), "", "", ""},
		{"socket, SCRAM bound", func(b *secretbind.Binding) { b.Auth = []string{"scram-sha-256"} }, kg(o{"host": "/var/run/postgresql"}), "scram-sha-256", "", ""},
		{"CA of the binding", func(b *secretbind.Binding) { b.CAFile = "/etc/sard/bound-ca.pem" }, kg(o{"tls_mode": "verify-full", "tls_root_cert": "/etc/sard/pg-ca.pem"}), "scram-sha-256", "require", "/etc/sard/bound-ca.pem"},
		{"CA of the config", nil, kg(o{"tls_mode": "verify-full", "tls_root_cert": "/etc/sard/pg-ca.pem"}), "scram-sha-256", "require", "/etc/sard/pg-ca.pem"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t)
			b := binding(t, "db", "/var/run/postgresql")
			b.AllowTLS = "disable"
			if c.allow != nil {
				c.allow(b)
			}
			r.bindings.bind(b)
			want(t, r.backup(c.cfg), succeeded)
			for _, key := range []string{"psql", "psql session", "pg_dump", "pg_dumpall"} {
				conn := connection(t, r.proc.ran(key)[0])
				for param, v := range map[string]string{"require_auth": c.auth, "channel_binding": c.channel, "sslrootcert": c.rootCert} {
					if conn.values[param] != v || slices.Contains(conn.keys, param) != (v != "") {
						t.Errorf("%s: %s = %q, want %q", key, param, conn.values[param], v)
					}
				}
			}
		})
	}
}

// Scenario 2: a server that asks for a weaker method gets no password; the
// step says what to change.
func TestAServerThatAsksForAWeakerMethodGetsNoPasswordAndTheStepSaysWhatToDo(t *testing.T) {
	for line, words := range map[string][]string{
		`psql: error: connection to server at "db" (10.0.0.5), port 5432 failed: authentication method requirement "scram-sha-256" failed: server requested a hashed password`: {
			"server requested a hashed password", "the password was not sent", "password_encryption", "--auth scram-sha-256,md5"},
		`psql: error: connection to server at "db" (10.0.0.5), port 5432 failed: authentication method requirement "scram-sha-256" failed: server requested a cleartext password`: {
			"cleartext", "the password was not sent"},
		`psql: error: connection to server at "db" (10.0.0.5), port 5432 failed: channel binding is required, but server did not offer an authentication method that supports channel binding`: {
			"channel binding", "--allow-no-channel-binding"},
		`psql: error: invalid connection option "require_auth"`: {
			"older than 16", "PostgreSQL client 16 or newer"},
		`psql: error: connection to server at "db" (10.0.0.5), port 5432 failed: root certificate file "system" does not exist`: {
			"--ca-file", "tls_root_cert"},
	} {
		r := newRig(t)
		r.proc.on("psql", fail(line))
		res := r.backup(k(o{"tls_mode": "verify-full"}))
		want(t, res, failed)
		mentions(t, res.GetMessage(), words...)
		oneLine(t, res.GetMessage())
		noLeak(t, r, res.GetMessage())
	}
}

// Scenario 3: no TLS, or TLS without the certificate checked, to a server on
// the network only when the binding allows it.
func TestTLSWeakerThanVerifyFullToTheNetworkNeedsTheBinding(t *testing.T) {
	cases := []struct {
		name    string
		allowed string
		host    string
		mode    string
		ok      bool
	}{
		{"disable, nothing allowed", "", "db", "disable", false},
		{"require, nothing allowed", "", "db", "require", false},
		{"verify-full, nothing allowed", "", "db", "verify-full", true},
		{"disable, require allowed", "require", "db", "disable", false},
		{"require, require allowed", "require", "db", "require", true},
		{"disable, disable allowed", "disable", "db", "disable", true},
		{"require, disable allowed", "disable", "db", "require", true},
		{"disable to localhost", "", "localhost", "disable", true},
		{"disable to 127.0.0.1", "", "127.0.0.1", "disable", true},
		{"disable to ::1", "", "::1", "disable", true},
		{"socket", "", "/var/run/postgresql", "disable", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t)
			b := binding(t, c.host)
			b.AllowTLS = c.allowed
			r.bindings.bind(b)
			res := r.backup(k(o{"host": c.host, "tls_mode": c.mode}))
			if c.ok {
				want(t, res, succeeded)
				return
			}
			want(t, res, failed)
			mentions(t, res.GetMessage(), "tls_mode "+c.mode, `secret "pg-app"`, "--allow-tls "+c.mode)
			if r.proc.started() != 0 {
				t.Errorf("programs ran: %d", r.proc.started())
			}
			noLeak(t, r, res.GetMessage())
		})
	}
}
