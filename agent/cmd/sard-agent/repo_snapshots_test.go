// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
)

// Rules of A8c-1 (docs/specs/agent/repo-restore.feature): repo snapshots.

// The "набор снимков" of the conventions: F, F2, P, G, M.
const (
	idF  = "aaaa1111" + "0000000000000000000000000000000000000000000000000000000f"
	idF2 = "aaaa2222" + "000000000000000000000000000000000000000000000000000000f2"
	idP  = "bbbb1111" + "00000000000000000000000000000000000000000000000000000000"
	idG  = "cccc1111" + "00000000000000000000000000000000000000000000000000000000"
	idM  = "dddd1111" + "00000000000000000000000000000000000000000000000000000000"
)

var msk = time.FixedZone("MSK", 3*3600)

func snapshotSet() []restic.Snapshot {
	return []restic.Snapshot{
		{ID: idF2, Time: time.Date(2026, 10, 12, 6, 0, 0, 0, msk), Hostname: "db1", Paths: []string{"/srv/data"},
			Tags: []string{"sard.run=r3", "sard.source=s1", "sard.step=st3"}},
		{ID: idG, Time: time.Date(2026, 10, 11, 6, 0, 7, 0, msk), Hostname: "db1", Paths: []string{"/app.globals.sql"},
			Tags: []string{"postgresql.database=app", "postgresql.format=plain", "postgresql.main_snapshot=" + idP, "postgresql.part=globals",
				"postgresql.pg_dump_version=16.4", "postgresql.role_passwords=false", "postgresql.server_version=16.4",
				"sard.run=r2", "sard.source=s2", "sard.step=st2"}},
		{ID: idM, Time: time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC), Hostname: "old-host", Paths: []string{"/etc"}},
		{ID: idF, Time: time.Date(2026, 10, 10, 6, 0, 0, 123456789, msk), Hostname: "db1", Paths: []string{"/srv/data"},
			Tags: []string{"sard.run=r1", "sard.source=s1", "sard.step=st1"}},
		{ID: idP, Time: time.Date(2026, 10, 11, 6, 0, 0, 0, msk), Hostname: "db1", Paths: []string{"/app.dump"},
			Tags: []string{"postgresql.database=app", "postgresql.format=custom", "postgresql.part=database",
				"postgresql.pg_dump_version=16.4", "postgresql.server_version=16.4", "sard.run=r2", "sard.source=s2", "sard.step=st2"}},
	}
}

// snapshotsHost is a host whose repository base holds the set of snapshots.
func snapshotsHost(t *testing.T) *setupHost {
	t.Helper()
	h := newSetupHost(t)
	h.base().initialized = true
	h.base().snapshots = snapshotSet()
	return h
}

func (h *setupHost) snapshots(extra ...string) (int, string, string) {
	return h.sudo(append([]string{"repo", "snapshots", "base", "--config", "C"}, extra...)...)
}

// rowsOf are the lines of the table below the header, split into columns.
func rowsOf(t *testing.T, stdout string) [][]string {
	t.Helper()
	lines := strings.Split(strings.TrimSuffix(stdout, "\n"), "\n")
	if len(lines) == 0 || strings.Join(strings.Fields(lines[0]), " ") != "ID TIME HOST SOURCE RUN COMPLETE PATHS" {
		t.Fatalf("header of %q", stdout)
	}
	var rows [][]string
	for _, l := range lines[1:] {
		rows = append(rows, strings.Fields(l))
	}
	return rows
}

func TestTheListNamesTheSourceAndTheRunFromTheSardTags(t *testing.T) {
	h := snapshotsHost(t)
	code, stdout, stderr := h.snapshots()
	assertCode(t, code, exitOK)
	if !strings.Contains(stderr, "completeness of 2 snapshot(s)") {
		t.Errorf("stderr %q", stderr)
	}
	byID := map[string][]string{}
	for _, r := range rowsOf(t, stdout) {
		byID[r[0]] = r
	}
	for id, want := range map[string][]string{
		"aaaa1111": {"aaaa1111", "2026-10-10T03:00:00Z", "db1", "s1", "r1", "unconfirmed", "/srv/data"},
		"bbbb1111": {"bbbb1111", "2026-10-11T03:00:00Z", "db1", "s2", "r2", "-", "/app.dump"},
		"dddd1111": {"dddd1111", "2026-10-09T12:00:00Z", "old-host", "-", "-", "-", "/etc"},
	} {
		if !slices.Equal(byID[id], want) {
			t.Errorf("row %s = %v, want %v", id, byID[id], want)
		}
	}
}

func TestAnEmptySourceTagIsADash(t *testing.T) {
	h := newSetupHost(t)
	h.base().snapshots = []restic.Snapshot{{ID: idF, Time: time.Now(), Hostname: "h", Paths: []string{"/x"}, Tags: []string{"sard.run=r9", "sard.source="}}}
	_, stdout, _ := h.snapshots()
	if rows := rowsOf(t, stdout); len(rows) != 1 || rows[0][3] != "-" || rows[0][4] != "r9" {
		t.Fatalf("rows %v", rows)
	}
}

func TestSeveralPathsAreJoinedByCommas(t *testing.T) {
	h := newSetupHost(t)
	h.base().snapshots = []restic.Snapshot{{ID: idF, Time: time.Now(), Hostname: "h", Paths: []string{"/a", "/b"}}}
	_, stdout, _ := h.snapshots()
	if rows := rowsOf(t, stdout); rows[0][6] != "/a,/b" {
		t.Fatalf("rows %v", rows)
	}
}

func TestSnapshotsAreOrderedByTimeAndTheTimeIsUTC(t *testing.T) {
	h := snapshotsHost(t)
	_, stdout, _ := h.snapshots()
	var order []string
	for _, r := range rowsOf(t, stdout) {
		order = append(order, r[0])
	}
	if want := []string{"dddd1111", "aaaa1111", "bbbb1111", "cccc1111", "aaaa2222"}; !slices.Equal(order, want) {
		t.Fatalf("order %v, want %v", order, want)
	}
}

func TestSnapshotsOfOtherHostsAreListedWithoutAHostFilter(t *testing.T) {
	h := snapshotsHost(t)
	_, stdout, _ := h.snapshots()
	if !strings.Contains(stdout, "old-host") {
		t.Errorf("stdout %q", stdout)
	}
	for _, c := range h.restic.callsTo(h.path("base"), "snapshots") {
		if slices.Contains(c.args, "--host") {
			t.Errorf("args %q", c.args)
		}
	}
}

func TestTheSourceFilterListsOnlyThatSource(t *testing.T) {
	h := snapshotsHost(t)
	code, stdout, _ := h.snapshots("--source", "s2")
	assertCode(t, code, exitOK)
	calls := h.restic.callsTo(h.path("base"), "snapshots")
	if len(calls) != 1 || !slices.Contains(calls[0].args, "sard.source=s2") {
		t.Fatalf("calls %v", calls)
	}
	var ids []string
	for _, r := range rowsOf(t, stdout) {
		ids = append(ids, r[0])
	}
	if !slices.Equal(ids, []string{"bbbb1111", "cccc1111"}) {
		t.Fatalf("ids %v", ids)
	}
}

func TestASourceWithoutSnapshotsIsAnEmptyListAndSuccess(t *testing.T) {
	h := snapshotsHost(t)
	code, stdout, _ := h.snapshots("--source", "s9")
	assertCode(t, code, exitOK)
	if stdout != "no snapshots of source s9 in repository base\n" {
		t.Fatalf("stdout %q", stdout)
	}
}

func TestAnEmptyRepositoryIsALineWithoutAHeader(t *testing.T) {
	h := newSetupHost(t)
	h.base().initialized = true
	code, stdout, _ := h.snapshots()
	assertCode(t, code, exitOK)
	if stdout != "no snapshots in repository base\n" {
		t.Fatalf("stdout %q", stdout)
	}
}

func TestAnUnusableSourceIsAUsageErrorBeforeTheStorageIsTouched(t *testing.T) {
	for _, source := range []string{"", "a,b", "a b", strings.Repeat("x", 129)} {
		h := snapshotsHost(t)
		code, stdout, stderr := h.snapshots("--source", source)
		assertCode(t, code, exitUsage)
		if !strings.Contains(stderr, "--source") || stdout != "" {
			t.Errorf("%q: stdout %q stderr %q", source, stdout, stderr)
		}
		h.assertNoBackendCalls()
	}
}

func TestThousandsOfSnapshotsAreAllPrinted(t *testing.T) {
	h := newSetupHost(t)
	for i := range 10000 {
		h.base().snapshots = append(h.base().snapshots, restic.Snapshot{
			ID: fmt.Sprintf("%08x", i) + strings.Repeat("0", 56), Time: time.Unix(int64(i), 0), Hostname: "h", Paths: []string{"/x"},
		})
	}
	code, stdout, _ := h.snapshots()
	assertCode(t, code, exitOK)
	if n := len(rowsOf(t, stdout)); n != 10000 {
		t.Fatalf("%d rows", n)
	}
}

// Rule "repo snapshots в JSON отдаёт полные id и все теги".

type snapshotsJSON struct {
	Repository string `json:"repository"`
	Snapshots  []struct {
		ID       string   `json:"id"`
		ShortID  string   `json:"short_id"`
		Time     string   `json:"time"`
		Hostname string   `json:"hostname"`
		Paths    []string `json:"paths"`
		Source   *string  `json:"source"`
		Run      *string  `json:"run"`
		Step     *string  `json:"step"`
		Tags     []string `json:"tags"`
	} `json:"snapshots"`
}

// listedAsJSON is repo snapshots --json of the set, by short id.
func listedAsJSON(t *testing.T) (snapshotsJSON, string) {
	t.Helper()
	h := snapshotsHost(t)
	code, stdout, _ := h.snapshots("--json")
	assertCode(t, code, exitOK)
	var got snapshotsJSON
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("stdout %q: %v", stdout, err)
	}
	return got, stdout
}

func (l snapshotsJSON) byShortID(t *testing.T, short string) (s struct {
	ID       string   `json:"id"`
	ShortID  string   `json:"short_id"`
	Time     string   `json:"time"`
	Hostname string   `json:"hostname"`
	Paths    []string `json:"paths"`
	Source   *string  `json:"source"`
	Run      *string  `json:"run"`
	Step     *string  `json:"step"`
	Tags     []string `json:"tags"`
}) {
	t.Helper()
	for _, s := range l.Snapshots {
		if s.ShortID == short {
			return s
		}
	}
	t.Fatalf("no snapshot %s", short)
	return s
}

func TestTheListInJSONHasTheFullIDAndTheValuesOfTheTags(t *testing.T) {
	got, _ := listedAsJSON(t)
	if got.Repository != "base" || len(got.Snapshots) != 5 {
		t.Fatalf("got %+v", got)
	}
	p := got.byShortID(t, "bbbb1111")
	shown := fmt.Sprintf("%s %s %s %v %s %s %s %d", p.ID, p.Time, p.Hostname, p.Paths, *p.Source, *p.Run, *p.Step, len(p.Tags))
	if want := idP + " 2026-10-11T03:00:00Z db1 [/app.dump] s2 r2 st2 8"; shown != want {
		t.Errorf("P = %q, want %q", shown, want)
	}
}

func TestInJSONASnapshotWithoutSardTagsHasNullsAndAnEmptyArrayOfTags(t *testing.T) {
	got, stdout := listedAsJSON(t)
	m := got.byShortID(t, "dddd1111")
	if m.Source != nil || m.Run != nil || m.Step != nil || len(m.Tags) != 0 {
		t.Errorf("M = %+v", m)
	}
	if !strings.Contains(stdout, `"tags":[]`) {
		t.Errorf("a snapshot without tags has no empty array: %s", stdout)
	}
}

func TestAnEmptyRepositoryInJSONIsAnEmptyArray(t *testing.T) {
	h := newSetupHost(t)
	h.base().initialized = true
	code, stdout, _ := h.snapshots("--json")
	assertCode(t, code, exitOK)
	if stdout != `{"repository":"base","snapshots":[]}`+"\n" {
		t.Fatalf("stdout %q", stdout)
	}
}

func TestAFailureInJSONIsTextOnStderr(t *testing.T) {
	h := snapshotsHost(t)
	h.base().script = exitsWith("snapshots", 12)
	code, stdout, stderr := h.snapshots("--json")
	assertRefusal(t, code, stderr, exitUsage, "WRONG_PASSWORD")
	if stdout != "" || !strings.Contains(stderr, "sard-agent repo snapshots:") {
		t.Fatalf("stdout %q stderr %q", stdout, stderr)
	}
}

// exitsWith makes the restic subcommand exit with code and print line.
func exitsWith(sub string, code int) func(string, []string) (string, int, bool) {
	return func(s string, _ []string) (string, int, bool) { return "", code, s == sub }
}

// Rule "repo snapshots только читает".

func TestTheListTakesNoLockAndWritesNothing(t *testing.T) {
	h := snapshotsHost(t)
	before := h.hostTree()
	code, _, _ := h.snapshots()
	assertCode(t, code, exitOK)
	calls := h.restic.callsTo(h.path("base"), "snapshots")
	if n := h.restic.backendCalls(); n != 1 || len(calls) != 1 {
		t.Fatalf("calls %v", h.restic.subs())
	}
	if !slices.Contains(calls[0].args, "--json") || !slices.Contains(calls[0].args, "--no-lock") {
		t.Errorf("args %q", calls[0].args)
	}
	h.assertHostUnchanged(before)
	if len(h.log.lines) != 0 {
		t.Errorf("audit lines %v", h.log.lines)
	}
}

func TestWhoMayListTheSnapshots(t *testing.T) {
	h := snapshotsHost(t)
	code, _, _ := h.sudo("repo", "snapshots", "base", "--config", "C")
	assertCode(t, code, exitOK)
	for _, c := range h.restic.callsTo(h.path("base"), "snapshots") {
		if c.runAs == nil || c.runAs.UID != serviceUID || c.runAs.GID != serviceUID {
			t.Errorf("as root restic ran as %+v", c.runAs)
		}
	}

	h = snapshotsHost(t)
	code, _, _ = h.asService("repo", "snapshots", "base", "--config", "C")
	assertCode(t, code, exitOK)
	for _, c := range h.restic.callsTo(h.path("base"), "snapshots") {
		if c.runAs != nil {
			t.Errorf("as the service user restic ran as %+v", c.runAs)
		}
	}

	h = snapshotsHost(t)
	code, _, stderr := h.asOther("repo", "snapshots", "base", "--config", "C")
	assertRefusal(t, code, stderr, exitUsage, "PRIVILEGES_REQUIRED")
	h.assertNoBackendCalls()
}

func TestWithoutTheServiceUserRootGetsNoList(t *testing.T) {
	h := snapshotsHost(t)
	h.people = nil
	code, _, stderr := h.snapshots()
	assertRefusal(t, code, stderr, exitUsage, "SERVICE_USER_UNKNOWN")
	h.assertNoBackendCalls()
}

// Rule "Отказ списка снимков называет причину".

func TestTheRefusalsOfTheList(t *testing.T) {
	for _, c := range []struct {
		name    string
		arrange func(h *setupHost)
		repo    string
		code    int
		reason  string
	}{
		{"unknown", func(*setupHost) {}, "nope", exitUsage, "REPOSITORY_UNKNOWN"},
		{"not initialized", func(h *setupHost) { h.base().script = exitsWith("snapshots", 10) }, "base", exitUsage, "REPOSITORY_NOT_INITIALIZED"},
		{"wrong password", func(h *setupHost) { h.base().script = exitsWith("snapshots", 12) }, "base", exitUsage, "WRONG_PASSWORD"},
		{"no password file", func(h *setupHost) { ok(h.t, os.Remove(h.path("secrets/base.pass"))) }, "base", exitUsage, "PASSWORD_FILE_MISSING"},
		{"unreachable", func(h *setupHost) {
			h.base().script = func(sub string, _ []string) (string, int, bool) {
				return "Fatal: unable to open config file: dial tcp: connection refused", 1, sub == "snapshots"
			}
		}, "base", exitTemporary, "BACKEND_UNAVAILABLE"},
		{"no restic", func(h *setupHost) { h.cfg.Restic.Path = h.path("missing-restic"); h.saveConfig() }, "base", exitAgentError, "RESTIC_NOT_FOUND"},
	} {
		h := snapshotsHost(t)
		c.arrange(h)
		before := h.hostTree()
		code, stdout, stderr := h.sudo("repo", "snapshots", c.repo, "--config", "C")
		assertRefusal(t, code, stderr, c.code, c.reason)
		if stdout != "" {
			t.Errorf("%s: stdout %q", c.name, stdout)
		}
		h.assertHostUnchanged(before)
	}
}

func TestAnUnknownNameNamesTheConnectedRepositories(t *testing.T) {
	h := snapshotsHost(t)
	_, _, stderr := h.sudo("repo", "snapshots", "nope", "--config", "C")
	if !strings.Contains(stderr, "base") {
		t.Fatalf("stderr %q", stderr)
	}
}

func TestAStorageThatDoesNotAnswerIsCutOffByTheConnectTimeout(t *testing.T) {
	h := snapshotsHost(t)
	vc := newVirtualClock()
	h.deps.clock = vc
	h.base().hangSnapshots = true
	done := h.runInBackground("repo", "snapshots", "base", "--config", "C", "--connect-timeout", "2s")
	for range 2000 {
		if len(h.restic.callsTo(h.path("base"), "snapshots")) > 0 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	vc.waitTimers(t, 2)
	vc.advance(2 * time.Second)
	r := within(t, done)
	assertRefusal(t, r.code, r.stderr, exitTemporary, "BACKEND_UNAVAILABLE")
	if !strings.Contains(r.stderr, "2s") || !h.base().terminated {
		t.Errorf("stderr %q, terminated %v", r.stderr, h.base().terminated)
	}
}

func TestTheOverallTimeoutEndsTheList(t *testing.T) {
	h := snapshotsHost(t)
	vc := newVirtualClock()
	h.deps.clock = vc
	h.base().hangSnapshots = true
	done := h.runInBackground("repo", "snapshots", "base", "--config", "C", "--timeout", "10s")
	for range 2000 {
		if len(h.restic.callsTo(h.path("base"), "snapshots")) > 0 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	vc.waitTimers(t, 2)
	vc.advance(10 * time.Second)
	r := within(t, done)
	assertRefusal(t, r.code, r.stderr, exitTemporary, "TIMEOUT")
}

func TestAnS3EnvFileThatFailsTheOwnerCheckIsNotRead(t *testing.T) {
	h := snapshotsHost(t)
	env := h.path("secrets/restic-extra.env")
	h.write(env, "AWS_ACCESS_KEY_ID=K\nAWS_SECRET_ACCESS_KEY="+s3Marker+"\n", 0o644)
	h.write(h.path("secrets/restic-extra.pass"), "extra-password\n", 0o600)
	h.write(h.path("agent.d/repo-extra.yaml"), string(hostsetup.RepositoryYAMLWithEnv("extra", s3Address, h.path("secrets/restic-extra.pass"), env)), 0o640)
	code, _, stderr := h.sudo("repo", "snapshots", "extra", "--config", "C")
	assertRefusal(t, code, stderr, exitUsage, "SECRET_FILE_REJECTED")
	if !strings.Contains(stderr, env) {
		t.Errorf("stderr %q", stderr)
	}
	h.assertNoBackendCalls()
}

func TestARemoteStorageThatRefusesIsToldApartByItsCause(t *testing.T) {
	for _, c := range []struct{ url, fatal, reason string }{
		{s3Address, "Fatal: Access Denied.", "STORAGE_ACCESS_DENIED"},
		{s3Address, "Fatal: The specified bucket does not exist: NoSuchBucket", "BUCKET_NOT_FOUND"},
		{"sftp://backup@sftp.example.com/srv/restic", "Fatal: permission denied", "STORAGE_ACCESS_DENIED"},
	} {
		h := snapshotsHost(t)
		h.cfg.Repositories = append(h.cfg.Repositories, h.cfg.Repositories[0])
		h.cfg.Repositories[1].Name, h.cfg.Repositories[1].URL = "remote", c.url
		h.saveConfig()
		h.restic.repo(c.url).script = func(sub string, _ []string) (string, int, bool) { return c.fatal, 1, sub == "snapshots" }
		code, _, stderr := h.sudo("repo", "snapshots", "remote", "--config", "C")
		assertRefusal(t, code, stderr, exitUsage, c.reason)
		if !strings.Contains(stderr, c.url) {
			t.Errorf("%s: stderr %q does not name the address", c.reason, stderr)
		}
	}
}

// Rule "Пароли и ключи не появляются в выводе и журналах".

// leakyS3Host has the repository extra whose restic prints every secret.
func leakyS3Host(t *testing.T) (*setupHost, string) {
	t.Helper()
	h := snapshotsHost(t)
	url := "s3:https://u:" + urlMarker + "@s3.example.com/bucket-b/extra"
	env := h.path("secrets/restic-extra.env")
	h.write(env, "AWS_ACCESS_KEY_ID=K\nAWS_SECRET_ACCESS_KEY="+s3Marker+"\n", 0o600)
	h.write(h.path("secrets/restic-extra.pass"), passMarker+"\n", 0o600)
	h.write(h.path("agent.d/repo-extra.yaml"), string(hostsetup.RepositoryYAMLWithEnv("extra", url, h.path("secrets/restic-extra.pass"), env)), 0o640)
	h.restic.repo(url).script = func(sub string, _ []string) (string, int, bool) {
		return "Fatal: " + s3Marker + " " + passMarker + " " + url, 1, sub == "snapshots"
	}
	return h, url
}

func TestNoSecretReachesTheOutputOfTheList(t *testing.T) {
	for _, extra := range [][]string{nil, {"--json"}} {
		h, _ := leakyS3Host(t)
		code, stdout, stderr := h.sudo(append([]string{"repo", "snapshots", "extra", "--config", "C"}, extra...)...)
		if code == exitOK {
			t.Fatalf("the list succeeded: %q", stdout)
		}
		if !strings.Contains(stderr, "[REDACTED]") {
			t.Errorf("restic's text is not scrubbed: %q", stderr)
		}
		for _, out := range append([]string{stdout, stderr}, h.log.lines...) {
			for _, secret := range []string{passMarker, "MARKER", "PASS-"} {
				if strings.Contains(out, secret) {
					t.Errorf("%s is exposed:\n%s", secret, out)
				}
			}
		}
	}
}

func TestThePasswordIsPassedToResticAsAFileOnly(t *testing.T) {
	h, url := leakyS3Host(t)
	h.sudo("repo", "snapshots", "extra", "--config", "C")
	calls := h.restic.callsTo(url, "snapshots")
	if len(calls) != 1 {
		t.Fatalf("calls %v", calls)
	}
	if strings.Contains(strings.Join(calls[0].args, " "), passMarker) {
		t.Errorf("a password in the arguments: %q", calls[0].args)
	}
	if envValue(calls[0].env, "RESTIC_PASSWORD_FILE") != h.path("secrets/restic-extra.pass") {
		t.Errorf("the password is not passed as a file: %q", calls[0].env)
	}
}

// Rule "Справка есть у repo snapshots".

func TestSnapshotsHelp(t *testing.T) {
	h := newSetupHost(t)
	code, stdout, _ := h.sudo("repo", "snapshots", "--help")
	assertCode(t, code, exitOK)
	for _, want := range []string{"--source", "--json", "--timeout", "--connect-timeout", "ID", "TIME", "HOST", "SOURCE", "RUN", "COMPLETE", "PATHS", "confirmed", "unconfirmed",
		"only reads", "sudo", "service user", "  0  ", "  1  ", "  2  ", "  6  "} {
		if !strings.Contains(stdout, want) {
			t.Errorf("help lacks %q:\n%s", want, stdout)
		}
	}
	h.assertNoBackendCalls()
}
