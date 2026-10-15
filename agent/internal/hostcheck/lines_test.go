// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package hostcheck_test

import (
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/hostcheck"
	"github.com/Artur-Abalov/sard/agent/internal/refusal"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
)

// The small lines of sard-agent check (Ч10, Ч12, Ч2, Ч20).

func TestTheServiceUserLine(t *testing.T) {
	l := hostcheck.ServiceUserLine("sard-agent", 990, true)
	if l.Check != "service.user" || l.Status != hostcheck.OK || !strings.Contains(l.Message, "sard-agent") || !strings.Contains(l.Message, "990") {
		t.Errorf("%+v", l)
	}
	l = hostcheck.ServiceUserLine("backup", 0, false)
	if l.Status != hostcheck.Error || !strings.Contains(l.Message, `"backup"`) || !strings.Contains(l.Hint, "09-troubleshooting.md#serviceuser") {
		t.Errorf("%+v", l)
	}
}

func TestResticOfTheShippedVersionIsOK(t *testing.T) {
	pinned := restic.Version{Major: 0, Minor: 19, Patch: 1}
	l := hostcheck.ResticVersionLine("/usr/libexec/sard/restic", pinned, pinned)
	if l.Check != "restic.binary" || l.Status != hostcheck.OK || !strings.Contains(l.Message, "0.19.1") || !strings.Contains(l.Message, "/usr/libexec/sard/restic") {
		t.Errorf("%+v", l)
	}
}

func TestResticOfAnotherVersionNotBelowTheMinimumIsAWarning(t *testing.T) {
	pinned := restic.Version{Major: 0, Minor: 19, Patch: 1}
	l := hostcheck.ResticVersionLine("/usr/bin/restic", restic.Version{Major: 0, Minor: 19, Patch: 0}, pinned)
	if l.Status != hostcheck.Warn || !strings.Contains(l.Message, "0.19.0") || !strings.Contains(l.Message, "0.19.1") ||
		!strings.Contains(l.Hint, "restic.path") || !strings.Contains(l.Hint, "reinstall") {
		t.Errorf("%+v", l)
	}
}

func TestAResticThatCannotBeUsedIsAnErrorThatNamesTheReason(t *testing.T) {
	f := refusal.Fail(refusal.ResticTooOld, "restic 0.18.0 is older than 0.19.0")
	l := hostcheck.ResticFailureLine(f)
	if l.Status != hostcheck.Error || !strings.Contains(l.Message, "RESTIC_TOO_OLD") || !strings.Contains(l.Hint, "09-troubleshooting.md#resticbinary") {
		t.Errorf("%+v", l)
	}
}

func TestThePluginsTheAgentDeclaresAreListedWithTheirVersions(t *testing.T) {
	l := hostcheck.PluginDeclaredLine([]hostcheck.PluginInfo{{Name: "files", Version: "1.2.3"}, {Name: "postgresql", Version: "1.2.3"}})
	if l.Check != "plugin.declared" || l.Status != hostcheck.OK || !strings.Contains(l.Message, "files 1.2.3") || !strings.Contains(l.Message, "postgresql 1.2.3") {
		t.Errorf("%+v", l)
	}
	if l := hostcheck.NoProgramsLine("files"); l.Check != "plugin.programs" || l.Subject != "files" || l.Status != hostcheck.OK {
		t.Errorf("%+v", l)
	}
}

func TestTheRoleOfTheAgentIsNotCheckedYet(t *testing.T) {
	l := hostcheck.TLSRoleLine()
	if l.Check != "tls.role" || l.Status != hostcheck.Skip || l.Message != "agent role is checked once V1 is available" || l.Hint == "" {
		t.Errorf("%+v", l)
	}
}

func TestTheRestoreLocationsAreNotCheckedYet(t *testing.T) {
	l := hostcheck.RestoreLocationsLine()
	if l.Check != "restore.locations" || l.Status != hostcheck.Skip || l.Message != "restore locations are checked once R2 is available" || l.Hint == "" {
		t.Errorf("%+v", l)
	}
}

func TestACheckThatDependsOnAFailedOneIsSkipped(t *testing.T) {
	l := hostcheck.NotCheckedLine("secret.readable", "pg", "config.load")
	if l.Status != hostcheck.Skip || l.Subject != "pg" || l.Message != "not checked: config.load failed" || !strings.Contains(l.Hint, "sudo sard-agent check") {
		t.Errorf("%+v", l)
	}
}

func TestOfflineLeavesAChecksOutWithTheWayToMakeIt(t *testing.T) {
	l := hostcheck.OfflineLine("server.tls", "")
	if l.Status != hostcheck.Skip || l.Message != "offline" || !strings.Contains(l.Hint, "without --offline") {
		t.Errorf("%+v", l)
	}
}

func TestACheckForRootIsSkippedForTheServiceUserWithTheReason(t *testing.T) {
	l := hostcheck.NeedsRootLine("selinux.denials", "", "the audit log")
	if l.Status != hostcheck.Skip || !strings.Contains(l.Message, "needs root") || !strings.Contains(l.Message, "the audit log") || !strings.Contains(l.Hint, "sudo sard-agent check") {
		t.Errorf("%+v", l)
	}
}
