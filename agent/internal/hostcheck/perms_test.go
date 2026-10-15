// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package hostcheck_test

import (
	"io/fs"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/hostcheck"
)

// Rule "Каждая ошибка и предупреждение check сопровождаются подсказкой" (Ч11):
// files.permissions.

var svc = hostcheck.ServiceUser{Name: "sard-agent", UID: 990}

func facts(mode fs.FileMode, uid uint32) hostcheck.FileFacts {
	return hostcheck.FileFacts{Mode: mode, UID: uid, GID: 990}
}

func TestPermissionsOfAHealthyHostAreOK(t *testing.T) {
	l := hostcheck.PermissionsLine(svc, []hostcheck.PermFile{
		{Path: "/etc/sard/tls/agent.key", Kind: hostcheck.PermSecret, Facts: facts(0o600, 990)},
		{Path: "/etc/sard/secrets", Kind: hostcheck.PermSecretDir, Facts: facts(fs.ModeDir|0o700, 990)},
		{Path: "/etc/sard/agent.yaml", Kind: hostcheck.PermConfig, Facts: facts(0o640, 0)},
		{Path: "/etc/sard/agent.d/repo-x.yaml", Kind: hostcheck.PermConfig, Facts: facts(0o644, 0)},
	})
	if l.Check != "files.permissions" || l.Status != hostcheck.OK || l.Hint != "" {
		t.Errorf("%+v", l)
	}
}

func TestEveryViolationIsNamedWithTheCommandThatFixesIt(t *testing.T) {
	for _, c := range []struct {
		name   string
		file   hostcheck.PermFile
		status hostcheck.Status
		hint   []string
	}{
		{"secret with group bits", hostcheck.PermFile{Path: "/s/key", Kind: hostcheck.PermSecret, Facts: facts(0o644, 990)}, hostcheck.Error, []string{"sudo chmod 0600 /s/key"}},
		{"secret of root", hostcheck.PermFile{Path: "/s/pg", Kind: hostcheck.PermSecret, Facts: facts(0o600, 0)}, hostcheck.Error, []string{"sudo chown sard-agent /s/pg"}},
		{"secret of root with group bits", hostcheck.PermFile{Path: "/s/pg", Kind: hostcheck.PermSecret, Facts: facts(0o640, 0)}, hostcheck.Error, []string{"sudo chown sard-agent /s/pg", "sudo chmod 0600 /s/pg"}},
		{"directory 0755", hostcheck.PermFile{Path: "/s", Kind: hostcheck.PermSecretDir, Facts: facts(fs.ModeDir|0o755, 990)}, hostcheck.Error, []string{"sudo chmod 0700 /s"}},
		{"directory of root", hostcheck.PermFile{Path: "/s", Kind: hostcheck.PermSecretDir, Facts: facts(fs.ModeDir|0o700, 0)}, hostcheck.Error, []string{"sudo chown sard-agent /s"}},
		{"config writable by the group", hostcheck.PermFile{Path: "/c.yaml", Kind: hostcheck.PermConfig, Facts: facts(0o660, 0)}, hostcheck.Warn, []string{"sudo chmod g-w,o-w /c.yaml"}},
		{"fragment 0666", hostcheck.PermFile{Path: "/agent.d/repo-x.yaml", Kind: hostcheck.PermFragment, Facts: facts(0o666, 0)}, hostcheck.Warn, []string{"sudo chmod 0640 /agent.d/repo-x.yaml"}},
		{"config of the service user", hostcheck.PermFile{Path: "/c.yaml", Kind: hostcheck.PermConfig, Facts: facts(0o640, 990)}, hostcheck.Warn, []string{"sudo chown root /c.yaml"}},
		{"config directory of the service user", hostcheck.PermFile{Path: "/agent.d", Kind: hostcheck.PermConfig, Facts: facts(fs.ModeDir|0o750, 990)}, hostcheck.Warn, []string{"sudo chown root /agent.d"}},
	} {
		l := hostcheck.PermissionsLine(svc, []hostcheck.PermFile{c.file})
		if l.Status != c.status || !strings.Contains(l.Message, c.file.Path) {
			t.Errorf("%s: %+v", c.name, l)
		}
		for _, want := range c.hint {
			if !strings.Contains(l.Hint, want) {
				t.Errorf("%s: hint %q lacks %q", c.name, l.Hint, want)
			}
		}
	}
}

func TestAllViolationsAreOneLineAndTheWorstStatusWins(t *testing.T) {
	l := hostcheck.PermissionsLine(svc, []hostcheck.PermFile{
		{Path: "/c.yaml", Kind: hostcheck.PermConfig, Facts: facts(0o660, 0)},
		{Path: "/tls/agent.key", Kind: hostcheck.PermSecret, Facts: facts(0o644, 990)},
		{Path: "/secrets/pg", Kind: hostcheck.PermSecret, Facts: facts(0o640, 990)},
	})
	if l.Status != hostcheck.Error {
		t.Fatalf("%+v", l)
	}
	for _, p := range []string{"/tls/agent.key", "/secrets/pg", "/c.yaml"} {
		if !strings.Contains(l.Message, p) || !strings.Contains(l.Hint, p) {
			t.Errorf("%s is not in %+v", p, l)
		}
	}
}

func TestWhoCanReadAFileOrSearchADirectory(t *testing.T) {
	for _, c := range []struct {
		name  string
		f     hostcheck.FileFacts
		isDir bool
		want  bool
	}{
		{"owner reads", hostcheck.FileFacts{Mode: 0o400, UID: 990, GID: 1}, false, true},
		{"owner without the bit", hostcheck.FileFacts{Mode: 0o040, UID: 990, GID: 1}, false, false},
		{"group reads", hostcheck.FileFacts{Mode: 0o640, UID: 0, GID: 990}, false, true},
		{"group without the bit", hostcheck.FileFacts{Mode: 0o600, UID: 0, GID: 990}, false, false},
		{"others read", hostcheck.FileFacts{Mode: 0o604, UID: 0, GID: 5}, false, true},
		{"nobody", hostcheck.FileFacts{Mode: 0o600, UID: 0, GID: 5}, false, false},
		{"directory of root 0700", hostcheck.FileFacts{Mode: fs.ModeDir | 0o700, UID: 0, GID: 0}, true, false},
		{"directory 0750 of the group", hostcheck.FileFacts{Mode: fs.ModeDir | 0o750, UID: 0, GID: 990}, true, true},
		{"directory only to read", hostcheck.FileFacts{Mode: fs.ModeDir | 0o640, UID: 990, GID: 990}, true, false},
		{"owner is not the group", hostcheck.FileFacts{Mode: 0o070, UID: 990, GID: 990}, false, false},
	} {
		if got := hostcheck.CanRead(c.f, c.isDir, 990, 990); got != c.want {
			t.Errorf("%s: %v", c.name, got)
		}
	}
}
