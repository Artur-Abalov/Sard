// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package hostcheck_test

import (
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"syscall"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/hostcheck"
)

// Rule "Секрет проверяется без вывода значения" (Ч17).

func TestASecretThatCanBeReadIsOKAndNothingOfItsValueIsSaid(t *testing.T) {
	l := hostcheck.SecretLine("pg", "/s/pg", 17, nil)
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
		l := hostcheck.SecretLine("pg", "/s/pg", c.size, c.err)
		if l.Status != hostcheck.Error || !strings.Contains(l.Message, "/s/pg") || !strings.Contains(l.Message, c.want) || !strings.Contains(l.Hint, "sudo sard-agent secret set pg") {
			t.Errorf("%s: %+v", c.name, l)
		}
	}
}
