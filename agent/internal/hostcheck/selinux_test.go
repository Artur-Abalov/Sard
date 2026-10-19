// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package hostcheck_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/hostcheck"
)

// Rule "SELinux проверяется, только если он enforcing" (Ч19).

func TestOnlyEnforcingIsEnforcing(t *testing.T) {
	for in, want := range map[string]bool{"Enforcing\n": true, "Permissive\n": false, "Disabled\n": false, "": false} {
		if got := hostcheck.SELinuxEnforcing(in); got != want {
			t.Errorf("%q: %v", in, got)
		}
	}
}

func TestWithoutEnforcingBothLinesAreOK(t *testing.T) {
	labels, denials := hostcheck.SELinuxOffLines()
	for _, l := range []hostcheck.Line{labels, denials} {
		if l.Status != hostcheck.OK || l.Message != "SELinux is not enforcing" {
			t.Errorf("%+v", l)
		}
	}
	if labels.Check != "selinux.labels" || denials.Check != "selinux.denials" {
		t.Errorf("%q %q", labels.Check, denials.Check)
	}
}

func TestMatchpathconReportsTheFilesWhoseLabelDiffers(t *testing.T) {
	out := "/etc/sard/agent.yaml verified.\n" +
		"/etc/sard/secrets/pg has context unconfined_u:object_r:tmp_t:s0, should be system_u:object_r:etc_t:s0\n"
	got := hostcheck.ParseMatchpathcon(out)
	if len(got) != 1 || got[0].Path != "/etc/sard/secrets/pg" || got[0].Current != "unconfined_u:object_r:tmp_t:s0" || got[0].Want != "system_u:object_r:etc_t:s0" {
		t.Fatalf("%+v", got)
	}
}

func TestADifferingLabelIsAWarningWithTheRestoreconCommand(t *testing.T) {
	l := hostcheck.SELinuxLabelsLine([]hostcheck.LabelMismatch{{Path: "/etc/sard/secrets/pg", Current: "tmp_t", Want: "etc_t"}})
	if l.Status != hostcheck.Warn || !strings.Contains(l.Message, "/etc/sard/secrets/pg") || !strings.Contains(l.Message, "tmp_t") ||
		!strings.Contains(l.Message, "etc_t") || !strings.Contains(l.Hint, "sudo restorecon -Rv /etc/sard/secrets/pg") {
		t.Errorf("%+v", l)
	}
	if l := hostcheck.SELinuxLabelsLine(nil); l.Status != hostcheck.OK || l.Hint != "" {
		t.Errorf("%+v", l)
	}
}

// avc is a denial of the audit log at the time (seconds since the epoch).
func avc(comm string, n int, at int64) string {
	return fmt.Sprintf(`type=AVC msg=audit(%d.123:%d): avc:  denied  { read } for  pid=1234 comm=%q name="pg%d" dev="vda1" ino=1 scontext=system_u:system_r:sard_t:s0 tcontext=system_u:object_r:tmp_t:s0 tclass=file permissive=0`, at, n, comm, n)
}

func TestOnlyRecentDenialsOfTheAgentAndResticAreCounted(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	recent, old := now.Add(-time.Hour).Unix(), now.Add(-25*time.Hour).Unix()
	var out strings.Builder
	for i := 1; i <= 7; i++ {
		out.WriteString(avc("restic", i, recent) + "\n----\n")
	}
	out.WriteString(avc("httpd", 8, recent) + "\n" + avc("sard-agent", 9, recent) + "\n" + avc("restic", 10, old) + "\n")
	d := hostcheck.ParseDenials(out.String(), now.Add(-24*time.Hour))
	if d.Total != 8 || len(d.First) != 5 || !strings.Contains(d.First[0], "restic") || !strings.Contains(d.First[0], "read") || !strings.Contains(d.First[0], "pg1") {
		t.Fatalf("%+v", d)
	}
	if got := hostcheck.ParseDenials("<no matches>\n", now); got.Total != 0 {
		t.Errorf("%+v", got)
	}
}

func TestDenialsAreAWarningThatNamesTheNumberAndTheFirstFive(t *testing.T) {
	d := hostcheck.Denials{Total: 7, First: []string{"a", "b", "c", "d", "e"}}
	l := hostcheck.SELinuxDenialsLine(d)
	if l.Status != hostcheck.Warn || !strings.Contains(l.Message, "7 ") || !strings.Contains(l.Message, "a") || !strings.Contains(l.Hint, "ausearch") || !strings.Contains(l.Hint, "09-troubleshooting.md#selinuxdenials") {
		t.Errorf("%+v", l)
	}
	if l := hostcheck.SELinuxDenialsLine(hostcheck.Denials{}); l.Status != hostcheck.OK {
		t.Errorf("%+v", l)
	}
}
