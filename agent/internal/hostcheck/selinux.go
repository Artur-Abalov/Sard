// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package hostcheck

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// SELinuxEnforcing reads the answer of getenforce.
func SELinuxEnforcing(getenforce string) bool { return strings.TrimSpace(getenforce) == "Enforcing" }

// SELinuxOffLines are the two lines of a host where SELinux does not enforce.
func SELinuxOffLines() (labels, denials Line) {
	off := func(check string) Line {
		return Line{Check: check, Status: OK, Message: "SELinux is not enforcing"}
	}
	return off(CheckSELinuxLabels), off(CheckSELinuxDenials)
}

// LabelMismatch is a file whose label is not the one of the policy.
type LabelMismatch struct{ Path, Current, Want string }

var mismatchPattern = regexp.MustCompile(`^(.+) has context (\S+), should be (\S+)$`)

// ParseMatchpathcon reads the answer of `matchpathcon -V`.
func ParseMatchpathcon(output string) []LabelMismatch {
	var got []LabelMismatch
	for line := range strings.Lines(output) {
		if m := mismatchPattern.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
			got = append(got, LabelMismatch{Path: m[1], Current: strings.TrimSuffix(m[2], ","), Want: m[3]})
		}
	}
	return got
}

// SELinuxLabelsLine is selinux.labels of a host where SELinux enforces.
func SELinuxLabelsLine(mismatches []LabelMismatch) Line {
	if len(mismatches) == 0 {
		return Line{Check: CheckSELinuxLabels, Status: OK, Message: "the files of the agent have the labels of the policy"}
	}
	var texts, fixes []string
	for _, m := range mismatches {
		texts = append(texts, fmt.Sprintf("%s has the label %s, the policy says %s", m.Path, m.Current, m.Want))
		fixes = append(fixes, "sudo restorecon -Rv "+m.Path)
	}
	return Line{Check: CheckSELinuxLabels, Status: Warn, Message: strings.Join(texts, "; "), Hint: strings.Join(fixes, "; ")}
}

// Denials are the recent denials of SELinux to the agent and to restic.
type Denials struct {
	Total int
	// First are the first few of them, one short text each.
	First []string
}

const shownDenials = 5

var (
	deniedPattern = regexp.MustCompile(`avc:\s+denied\s+\{ ([^}]*)\}`)
	commPattern   = regexp.MustCompile(`comm="([^"]*)"`)
	fieldPattern  = regexp.MustCompile(`(name|scontext|tcontext)=("[^"]*"|\S+)`)
)

// ParseDenials reads the answer of `ausearch -m avc`: the denials since the
// time of processes called sard-agent or restic.
func ParseDenials(output string, since time.Time) Denials {
	var d Denials
	for line := range strings.Lines(output) {
		text, ok := denialOf(line, since)
		if !ok {
			continue
		}
		d.Total++
		if len(d.First) < shownDenials {
			d.First = append(d.First, text)
		}
	}
	return d
}

// denialOf describes a denial of the audit log, if the line is a recent one of
// the agent or restic.
func denialOf(line string, since time.Time) (string, bool) {
	perm := deniedPattern.FindStringSubmatch(line)
	comm := commPattern.FindStringSubmatch(line)
	if perm == nil || comm == nil || (comm[1] != "restic" && comm[1] != "sard-agent") || happenedBefore(line, since) {
		return "", false
	}
	return describeDenial(strings.TrimSpace(perm[1]), comm[1], line), true
}

var auditTime = regexp.MustCompile(`msg=audit\((\d+)`)

// happenedBefore: the record of the audit log is older than since.
func happenedBefore(line string, since time.Time) bool {
	m := auditTime.FindStringSubmatch(line)
	if m == nil {
		return false
	}
	seconds, err := strconv.ParseInt(m[1], 10, 64)
	return err == nil && time.Unix(seconds, 0).Before(since)
}

func describeDenial(perm, comm, line string) string {
	parts := []string{comm + " was denied " + perm}
	for _, f := range fieldPattern.FindAllStringSubmatch(line, -1) {
		parts = append(parts, f[1]+"="+strings.Trim(f[2], `"`))
	}
	return strings.Join(parts, " ")
}

// SELinuxDenialsLine is selinux.denials of a host where SELinux enforces.
func SELinuxDenialsLine(d Denials) Line {
	if d.Total == 0 {
		return Line{Check: CheckSELinuxDenials, Status: OK, Message: "no denials of SELinux to sard-agent or restic in the last 24 hours"}
	}
	return Line{Check: CheckSELinuxDenials, Status: Warn,
		Message: fmt.Sprintf("%d denials of SELinux to sard-agent or restic in the last 24 hours; the first %d: %s", d.Total, len(d.First), strings.Join(d.First, " | ")),
		Hint:    "list them with `sudo ausearch -m avc -ts yesterday`; see " + HelpURL(CheckSELinuxDenials)}
}
