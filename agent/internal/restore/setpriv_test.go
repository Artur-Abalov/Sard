// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package restore_test

import (
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/refusal"
	"github.com/Artur-Abalov/sard/agent/internal/restore"
)

// Р77: only an sftp: repository needs setpriv, to start ssh as the service user.
func TestAnSFTPRepositoryWithoutSetprivIsRefusedWithTheWayToInstallIt(t *testing.T) {
	f := restore.CheckSetpriv("sftp", func(string) bool { return false })
	if f == nil || f.Reason != refusal.SetprivMissing || f.Class != refusal.ClassAgentError {
		t.Fatalf("failure %v", f)
	}
	for _, want := range []string{"setpriv", "util-linux", "sudo apt-get install util-linux", "sudo dnf install util-linux", "other kinds of storage"} {
		if !strings.Contains(f.Detail, want) {
			t.Errorf("%q is not in %q", want, f.Detail)
		}
	}
}

func TestAnSFTPRepositoryWithSetprivPasses(t *testing.T) {
	var asked []string
	f := restore.CheckSetpriv("sftp", func(p string) bool { asked = append(asked, p); return true })
	if f != nil || len(asked) != 1 || asked[0] != "setpriv" {
		t.Fatalf("failure %v, asked %v", f, asked)
	}
}

func TestOtherStoragesNeedNoSetpriv(t *testing.T) {
	for _, kind := range []string{"local", "s3", "rest"} {
		if f := restore.CheckSetpriv(kind, func(string) bool { t.Error("looked for a program"); return false }); f != nil {
			t.Errorf("%s: %v", kind, f)
		}
	}
}
