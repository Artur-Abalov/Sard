// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package hostcheck_test

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/hostcheck"
)

// Rule "Секрет проверяется без вывода значения" (Ч17).

func TestASecretThatCanBeReadIsOKAndNothingOfItsValueIsSaid(t *testing.T) {
	l := hostcheck.SecretLine("pg", "/s/pg", hostcheck.SecretSetHint("pg", ""), 17, nil)
	if l.Check != "secret.readable" || l.Subject != "pg" || l.Status != hostcheck.OK || l.Hint != "" {
		t.Errorf("%+v", l)
	}
}

func TestASecretThatCannotBeUsedIsAnErrorWithThePathAndTheReason(t *testing.T) {
	for _, c := range []struct {
		name string
		size int
		err  error
		want string
	}{
		{"empty", 0, nil, "empty"},
		{"a link", 0, &fs.PathError{Op: "open", Path: "/s/pg", Err: syscall.ELOOP}, "symbolic link"},
		{"a directory", 0, errors.New("secret file /s/pg is not a regular file"), "not a regular file"},
		{"missing", 0, &fs.PathError{Op: "open", Path: "/s/pg", Err: fs.ErrNotExist}, "does not exist"},
		{"refused", 0, &fs.PathError{Op: "open", Path: "/s/pg", Err: fs.ErrPermission}, "permission denied"},
		{"too long", 0, fmt.Errorf("secret file /s/pg is larger than 1048576 bytes"), "larger than"},
	} {
		l := hostcheck.SecretLine("pg", "/s/pg", hostcheck.SecretSetHint("pg", ""), c.size, c.err)
		if l.Status != hostcheck.Error || !strings.Contains(l.Message, "/s/pg") || !strings.Contains(l.Message, c.want) || !strings.Contains(l.Hint, "sudo sard-agent secret set pg") {
			t.Errorf("%s: %+v", c.name, l)
		}
	}
}

// Rule "Подсказка ведёт к команде, которая сработает" (S11): secret set
// refuses a secret of the main config (DEFINED_IN_CONFIG).

func TestASecretOfAFragmentIsSetAndBoundBySecretSet(t *testing.T) {
	want := "sudo sard-agent secret set pg --plugin <plugin> --target <host:port/database>"
	if got := hostcheck.SecretSetHint("pg", ""); got != want {
		t.Errorf("%q", got)
	}
	if got := hostcheck.BindingHint("pg", "/etc/sard/secret-bindings/pg.yaml", ""); got != want {
		t.Errorf("%q", got)
	}
}

func TestASecretOfTheMainConfigIsFixedThereAndBoundByHand(t *testing.T) {
	if got := hostcheck.SecretSetHint("pg", "/etc/sard/agent.yaml"); got != `secret "pg" is defined in /etc/sard/agent.yaml, which commands never change: fix it there` {
		t.Errorf("%q", got)
	}
	want := `secret "pg" is defined in /etc/sard/agent.yaml, which commands never change: write its binding into ` +
		"/etc/sard/secret-bindings/pg.yaml by hand (docs/operator/09-troubleshooting.md#secretbound)"
	if got := hostcheck.BindingHint("pg", "/etc/sard/secret-bindings/pg.yaml", "/etc/sard/agent.yaml"); got != want {
		t.Errorf("%q", got)
	}
}

func TestOnlyASecretTheMainConfigDefinesIsInIt(t *testing.T) {
	dir := t.TempDir()
	main := filepath.Join(dir, "agent.yaml")
	write(t, main, "server:\n  address: s:1\nsecrets:\n  pg: /s/pg\n")
	write(t, filepath.Join(dir, "agent.d", "10-db.yaml"), "secrets:\n  db: /s/db\n")
	cfg, err := config.Load(main)
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"pg": main, "db": "", "none": ""} {
		if got := hostcheck.InMainConfig(cfg, name); got != want {
			t.Errorf("%s: %q, want %q", name, got, want)
		}
	}
	if got := hostcheck.InMainConfig(config.Config{Secrets: map[string]string{"pg": "/s/pg"}}, "pg"); got != "" {
		t.Errorf("a config of no file: %q", got)
	}
}

func write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o640); err != nil {
		t.Fatal(err)
	}
}
