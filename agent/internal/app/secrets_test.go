// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package app_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/Artur-Abalov/sard/agent/internal/secretbind"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

// bindings: pg-prod is bound, ssh has none, broken cannot be read.
type bindings struct{}

func (bindings) Binding(name string) (secretbind.Binding, bool, error) {
	switch name {
	case "pg-prod":
		b, _ := secretbind.New("postgresql", []string{"db.example.com:5432/app", "[::1]:5432/*"})
		b.Auth, b.AllowTLS, b.CAFile, b.AllowNoChannelBinding = []string{"scram-sha-256", "md5"}, "require", "/etc/sard/pg-ca.pem", true
		return b, true, nil
	case "broken":
		return secretbind.Binding{}, false, errors.New(`binding of secret "broken": not a regular file`)
	}
	return secretbind.Binding{}, false, nil
}

// S11: the server learns each secret with its binding, never its file, its
// value or the CA file of the binding.
func TestRegisterRequestAnnouncesTheBindingOfEverySecret(t *testing.T) {
	a := newAgent(t, &fakeLink{})
	a.Local.Secrets["broken"] = "/etc/sard/broken"
	a.Bindings = bindings{}
	req := a.RegisterRequest(context.Background())
	want := []*agentv1.SecretInfo{
		{Name: "broken", BindingProblem: `binding of secret "broken": not a regular file`},
		{Name: "pg-prod", Plugin: "postgresql", Targets: []string{"db.example.com:5432/app", "[::1]:5432/*"}, Auth: []string{"scram-sha-256", "md5"}, AllowTls: "require", AllowNoChannelBinding: true},
		{Name: "ssh", BindingProblem: "the secret has no binding on the agent host and is given to no plugin"},
	}
	got := req.GetSecrets()
	if len(got) != len(want) {
		t.Fatalf("secrets = %v", got)
	}
	for i := range want {
		if !proto.Equal(got[i], want[i]) {
			t.Errorf("secret %d = %v, want %v", i, got[i], want[i])
		}
	}
	if wire := req.String(); strings.Contains(wire, "/etc/sard") {
		t.Errorf("register request leaks a path: %s", wire)
	}
}

func TestWithoutBindingsEverySecretIsAnnouncedUnbound(t *testing.T) {
	for _, s := range registered(t).GetSecrets() {
		if s.GetPlugin() != "" || s.GetBindingProblem() == "" {
			t.Errorf("secret %v", s)
		}
	}
	if n := len(registered(t).GetSecrets()); n != 2 {
		t.Errorf("%d secrets announced, want 2", n)
	}
}
