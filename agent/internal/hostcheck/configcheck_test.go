// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package hostcheck_test

import (
	"errors"
	"io/fs"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/hostcheck"
)

// Rules "Каждая ошибка и предупреждение check сопровождаются подсказкой" and
// "Ссылка на несуществующий файл называет ключ и путь" (Ч10): config.load,
// config.names, config.references.

func TestAConfigThatLoadsIsOK(t *testing.T) {
	l := hostcheck.ConfigLoadLine("/etc/sard/agent.yaml", 2, nil)
	if l.Check != "config.load" || l.Status != hostcheck.OK || !strings.Contains(l.Message, "2 fragments") {
		t.Errorf("%+v", l)
	}
}

func TestAConfigThatDoesNotLoadNamesTheCauseAndTheGuide(t *testing.T) {
	l := hostcheck.ConfigLoadLine("/etc/sard/agent.yaml", 0, errors.New("/etc/sard/agent.d/x.yaml: key \"server\" is not allowed in a fragment"))
	if l.Status != hostcheck.Error || !strings.Contains(l.Message, "agent.d/x.yaml") || !strings.Contains(l.Hint, "09-troubleshooting.md#configload") {
		t.Errorf("%+v", l)
	}
}

func TestADuplicateNameIsNotAFailureToLoad(t *testing.T) {
	dup := &config.DuplicateNameError{Kind: "repository", Name: "base", First: "/c.yaml", Second: "/c/agent.d/repo-base.yaml"}
	if !hostcheck.IsDuplicateName(dup) || hostcheck.IsDuplicateName(errors.New("x")) {
		t.Fatal("IsDuplicateName")
	}
}

func TestADuplicateNameNamesBothFilesAndTheCommandThatRemovesOne(t *testing.T) {
	dup := &config.DuplicateNameError{Kind: "repository", Name: "base", First: "/c.yaml", Second: "/c/agent.d/repo-base.yaml"}
	l := hostcheck.ConfigNamesLine(dup)
	if l.Check != "config.names" || l.Status != hostcheck.Error || !strings.Contains(l.Message, "DUPLICATE_NAME") ||
		!strings.Contains(l.Message, "/c.yaml") || !strings.Contains(l.Message, "repo-base.yaml") || !strings.Contains(l.Hint, "sudo sard-agent repo remove base") {
		t.Errorf("%+v", l)
	}
}

func TestTheCommandThatRemovesADuplicateSecretIsSecretRemove(t *testing.T) {
	secret := hostcheck.ConfigNamesLine(&config.DuplicateNameError{Kind: "secret", Name: "pg", First: "a", Second: "b"})
	if !strings.Contains(secret.Hint, "sudo sard-agent secret remove pg") {
		t.Errorf("%+v", secret)
	}
	if l := hostcheck.ConfigNamesLine(nil); l.Status != hostcheck.OK {
		t.Errorf("%+v", l)
	}
}

func referencesOf() config.Config {
	return config.Config{
		TLS:          config.TLS{CAFile: "/tls/ca.pem", CertFile: "/tls/agent.pem", KeyFile: "/tls/agent.key"},
		Restic:       config.Restic{Path: "/usr/bin/restic"},
		Repositories: []config.Repository{{Name: "base", URL: "/r", PasswordFile: "/s/base.pass", EnvFile: "/s/base.env"}, {Name: "plain", URL: "/p", PasswordFile: "/s/plain.pass"}},
		Secrets:      map[string]string{"pg": "/s/pg"},
		Scripts:      map[string]string{"hook": "/opt/hook.sh"},
	}
}

func TestEveryFileTheConfigNamesIsAReference(t *testing.T) {
	var keys []string
	for _, r := range hostcheck.References(referencesOf()) {
		keys = append(keys, r.Key+"="+r.Path)
	}
	want := "tls.ca_file=/tls/ca.pem tls.cert_file=/tls/agent.pem tls.key_file=/tls/agent.key restic.path=/usr/bin/restic " +
		"repositories.base.password_file=/s/base.pass repositories.base.env_file=/s/base.env repositories.plain.password_file=/s/plain.pass " +
		"secrets.pg=/s/pg scripts.hook=/opt/hook.sh"
	if got := strings.Join(keys, " "); got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestReferencesThatExistAreOK(t *testing.T) {
	l := hostcheck.ReferencesLine(hostcheck.References(referencesOf()), func(string) error { return nil })
	if l.Check != "config.references" || l.Status != hostcheck.OK {
		t.Errorf("%+v", l)
	}
}

func TestAMissingFileIsNamedWithItsKeyAndPathAndTheCommandThatMakesIt(t *testing.T) {
	missing := map[string]bool{"/s/pg": true, "/s/base.pass": true, "/tls/agent.pem": true, "/opt/hook.sh": true, "/s/base.env": true, "/usr/bin/restic": true}
	l := hostcheck.ReferencesLine(hostcheck.References(referencesOf()), func(p string) error {
		if missing[p] {
			return fs.ErrNotExist
		}
		return nil
	})
	if l.Status != hostcheck.Error {
		t.Fatalf("%+v", l)
	}
	for _, want := range []string{"secrets.pg", "/s/pg", "repositories.base.password_file", "/s/base.pass", "tls.cert_file", "scripts.hook"} {
		if !strings.Contains(l.Message, want) {
			t.Errorf("message lacks %q: %s", want, l.Message)
		}
	}
	for _, want := range []string{"sudo sard-agent secret set pg", "sudo sard-agent repo add base", "sudo sard-agent enroll --force", "09-troubleshooting.md#configreferences"} {
		if !strings.Contains(l.Hint, want) {
			t.Errorf("hint lacks %q: %s", want, l.Hint)
		}
	}
}

func TestAReferenceThatCannotBeLookedAtIsReportedWithTheReason(t *testing.T) {
	l := hostcheck.ReferencesLine(hostcheck.References(referencesOf()), func(p string) error {
		if p == "/s/pg" {
			return &fs.PathError{Op: "stat", Path: p, Err: fs.ErrPermission}
		}
		return nil
	})
	if l.Status != hostcheck.Error || !strings.Contains(l.Message, "secrets.pg") || !strings.Contains(l.Message, "permission denied") {
		t.Errorf("%+v", l)
	}
}
