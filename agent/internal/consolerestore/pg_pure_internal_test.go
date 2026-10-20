// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package consolerestore

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/restic"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

// The small readers and writers of the restore of a database, on their edges.

func TestIdentAfterReadsBareAndQuotedNames(t *testing.T) {
	tests := []struct {
		line string
		name string
		ok   bool
	}{
		{"CREATE ROLE app;", "app", true},
		{"CREATE ROLE app", "app", true},
		{"CREATE ROLE app WITH LOGIN;", "app", true},
		{"CREATE ROLE a\tb", "a", true},
		{"CREATE ROLE ;", "", false},
		{"CREATE ROLE ", "", false},
		{"ALTER ROLE app;", "", false},
		{`CREATE ROLE "app";`, "app", true},
		{`CREATE ROLE "a""b";`, `a"b`, true},
		{`CREATE ROLE "a"`, "a", true},
		{`CREATE ROLE "a""`, "", false},
		{`CREATE ROLE "ab`, "", false},
		{`CREATE ROLE "`, "", false},
	}
	for _, tt := range tests {
		name, ok := identAfter(tt.line, "CREATE ROLE ")
		if name != tt.name || ok != tt.ok {
			t.Errorf("identAfter(%q) = %q, %v, want %q, %v", tt.line, name, ok, tt.name, tt.ok)
		}
	}
}

func TestOnlyTheGuardsOfPgDumpAreNotCommands(t *testing.T) {
	tests := map[string]bool{ // refused?
		`CREATE ROLE a;`:              false,
		`\restrict AbC123`:            false,
		`\unrestrict AbC123`:          false,
		`\restrict `:                  true,
		`\unrestrict `:                true,
		`\restrict a b`:               true,
		`\restrict a-b`:               true,
		`\restrict ab\!id`:            true,
		`\restrict`:                   true,
		`\!id`:                        true,
		` \!id`:                       true,
		`select 1; \gexec`:            true,
		`COMMENT ON ROLE a IS 'x\y';`: true,
	}
	for line, want := range tests {
		if got := isMetaCommand(line); got != want {
			t.Errorf("isMetaCommand(%q) = %v, want %v", line, got, want)
		}
	}
}

func TestTheFirstCommandOfPsqlIsTheLineThatIsReported(t *testing.T) {
	g := parseGlobals("CREATE ROLE a;\n\\restrict k1\n\\!id\nCREATE ROLE b;\n\\gexec\n\\unrestrict k1\n")
	if g.meta != 3 {
		t.Errorf("meta = %d, want 3", g.meta)
	}
	if !slices.Equal(g.roles, []string{"a", "b"}) {
		t.Errorf("roles = %v", g.roles)
	}
	if clean := parseGlobals("\\restrict k1\nCREATE ROLE a;\n\\unrestrict k1\n"); clean.meta != 0 {
		t.Errorf("meta of a clean file = %d", clean.meta)
	}
}

func TestTheVersionOfPgRestoreIsReadFromItsOutput(t *testing.T) {
	if v, ok := parseClient("pg_restore (PostgreSQL) 16.4 (Debian 16.4-1)"); !ok || v.text != "16.4" || v.major != 16 {
		t.Errorf("client = %+v, %v", v, ok)
	}
	if v, ok := parseClient("pg_restore (PostgreSQL) 18beta1"); !ok || v.text != "18beta1" || v.major != 18 {
		t.Errorf("client = %+v, %v", v, ok)
	}
	if _, ok := parseClient("pg_restore 16"); ok {
		t.Error("a version without the marker was read")
	}
}

func TestTheVersionOfATagStartsWithANumber(t *testing.T) {
	for text, major := range map[string]int{"0": 0, "9": 9, "16.4": 16, "18beta1": 18, "0abc": 0, "1": 1} {
		if v, ok := parseTag(text); !ok || v.major != major || v.text != text {
			t.Errorf("parseTag(%q) = %+v, %v", text, v, ok)
		}
	}
	for _, text := range []string{"", "x16", ".5"} {
		if _, ok := parseTag(text); ok {
			t.Errorf("parseTag(%q) was read", text)
		}
	}
}

func TestTheVersionOfTheServerIsTheMajorAndTheMinor(t *testing.T) {
	if v := serverVersion(160004); v.text != "16.4" || v.major != 16 {
		t.Errorf("server = %+v", v)
	}
	if v := serverVersion(169990); v.text != "16.9990" || v.major != 16 {
		t.Errorf("server = %+v", v)
	}
}

func TestTheProbeAnswerKeepsTheRoleNameWhole(t *testing.T) {
	p, err := parseProbe(outcome{stdout: []string{"160004|t|f|we|ird"}})
	if err != nil || p.user != "we|ird" || !p.super || p.createdb || p.server.major != 16 {
		t.Errorf("probe = %+v, %v", p, err)
	}
	for _, bad := range []string{"", "160004|t|f", "x|t|f|u"} {
		if _, err := parseProbe(outcome{stdout: []string{bad}}); err == nil {
			t.Errorf("%q was read", bad)
		}
	}
}

func TestAPasswordIsTheSecretWithoutItsLineEnding(t *testing.T) {
	if v, err := Password("p", []byte("hunter2\r\n")); err != nil || string(v) != "hunter2" {
		t.Errorf("password = %q, %v", v, err)
	}
	for name, content := range map[string]string{"empty": "\n", "NUL first": "\x00abc", "NUL inside": "ab\x00c", "NUL alone": "\x00"} {
		if _, err := Password("p", []byte(content)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestTheGlobalsFileFillsUpToItsLimitAndNoFurther(t *testing.T) {
	b := &limitedBuffer{max: 4}
	if n, err := b.Write([]byte("abcd")); n != 4 || err != nil {
		t.Errorf("a write that fits: %d, %v", n, err)
	}
	if n, err := b.Write([]byte("e")); n != 0 || err == nil || !b.exceeded {
		t.Errorf("a write over the limit: %d, %v, exceeded %v", n, err, b.exceeded)
	}
	if b.String() != "abcd" {
		t.Errorf("content %q", b.String())
	}
}

func TestTheWriterThatCountsAddsWhatWasWritten(t *testing.T) {
	var seen []uint64
	w := &countingWriter{w: io.Discard, onWrite: func(total uint64) { seen = append(seen, total) }}
	_, _ = w.Write([]byte("abc"))
	_, _ = w.Write([]byte("defg"))
	if !slices.Equal(seen, []uint64{3, 7}) || w.total != 7 || w.failed() {
		t.Errorf("seen %v, total %d, failed %v", seen, w.total, w.failed())
	}
	bad := &countingWriter{w: failingWriter{}}
	if _, err := bad.Write([]byte("x")); err == nil || !bad.failed() {
		t.Errorf("a failed write: %v, %v", err, bad.failed())
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("broken pipe") }

func TestTheWordsOfAFailedRun(t *testing.T) {
	tests := []struct {
		name string
		o    outcome
		text string
		why  string
	}{
		{"killed", outcome{tool: "psql", err: errors.New("signal: killed")}, "psql failed: signal: killed", "psql failed: signal: killed"},
		{"with a reason", outcome{tool: "psql", code: 3, reason: "ERROR: boom"}, "psql failed (exit code 3): ERROR: boom", "ERROR: boom"},
		{"without", outcome{tool: "psql", code: 2}, "psql failed (exit code 2)", "psql failed (exit code 2)"},
	}
	for _, tt := range tests {
		if got := tt.o.text(); got != tt.text {
			t.Errorf("%s: text %q, want %q", tt.name, got, tt.text)
		}
		if got := tt.o.why(); got != tt.why {
			t.Errorf("%s: why %q, want %q", tt.name, got, tt.why)
		}
		if tt.o.ok() {
			t.Errorf("%s: ok", tt.name)
		}
	}
}

func TestARunIsOkOnlyIfItStartedAndEndedWithZero(t *testing.T) {
	if o := (outcome{tool: "psql", err: errors.New("x")}); o.ok() {
		t.Error("a run that did not start is ok")
	}
	if !(outcome{}).ok() {
		t.Error("a run that ended with 0 is not ok")
	}
}

func TestWhatIsToldOfAnAnswerTheAgentCannotRead(t *testing.T) {
	if got := (outcome{tool: "psql", stdout: []string{"", "  ", "oops "}}).problem(); got != `unexpected answer of psql: "oops"` {
		t.Errorf("problem %q", got)
	}
	if got := (outcome{tool: "psql", code: 1}).problem(); got != "psql failed (exit code 1)" {
		t.Errorf("problem %q", got)
	}
	if got := (outcome{stdout: []string{"a", " ", "b", ""}}).last(); got != "b" {
		t.Errorf("last %q", got)
	}
}

// --- a program run through a recording executor

type recordingExec struct {
	cmds   []restic.Command
	stderr []string
	code   int
	err    error
}

func (e *recordingExec) Run(_ context.Context, cmd restic.Command) (int, error) {
	e.cmds = append(e.cmds, cmd)
	for _, l := range e.stderr {
		cmd.Stderr([]byte(l))
	}
	return e.code, e.err
}

type logs struct {
	entries []struct {
		level agentv1.LogLevel
		text  string
	}
}

func (l *logs) Progress(agentv1.StepPhase, uint64, uint64) {}
func (l *logs) ProgressFiles(agentv1.StepPhase, uint64, uint64, uint64, uint64) {
}
func (l *logs) Log(level agentv1.LogLevel, text string) {
	l.entries = append(l.entries, struct {
		level agentv1.LogLevel
		text  string
	}{level, text})
}

func testConn(exec *recordingExec) pgConn {
	return pgConn{
		plan:    sdk.PostgresRestore{Host: "db2", Port: 5432, User: "restorer"},
		tools:   pgTools{restore: "/usr/lib/postgresql/16/bin/pg_restore", psql: "/usr/lib/postgresql/16/bin/psql"},
		environ: func() []string { return []string{"PATH=/usr/bin"} },
		exec:    exec,
	}
}

func TestTheToolIsNamedByTheLastComponentOfItsPath(t *testing.T) {
	for path, want := range map[string]string{"/usr/lib/postgresql/16/bin/psql": "psql", "/psql": "psql", "psql": "psql"} {
		exec := &recordingExec{code: 2}
		o := testConn(exec).run(context.Background(), &logs{}, path, nil, nil)
		if o.tool != want || o.text() != want+" failed (exit code 2)" {
			t.Errorf("%s: tool %q, text %q", path, o.tool, o.text())
		}
	}
}

func TestPsqlGetsItsVariablesInPairsAndNothingElse(t *testing.T) {
	exec := &recordingExec{}
	testConn(exec).psql(context.Background(), &logs{}, "postgres", "select 1;", "a", "1", "b", "2", "orphan")
	args := exec.cmds[0].Args
	var vars []string
	for i, a := range args {
		if a == "-v" {
			vars = append(vars, args[i+1])
		}
	}
	if !slices.Equal(vars, []string{"ON_ERROR_STOP=1", "a=1", "b=2"}) {
		t.Errorf("variables %v", vars)
	}
}

func TestTheLinesOfAProgramAreLoggedByTheirKindAndTheErrorIsQuoted(t *testing.T) {
	tests := []struct {
		line  string
		level agentv1.LogLevel
	}{
		{"psql: error: connection refused", agentv1.LogLevel_LOG_LEVEL_WARN},
		{"FATAL:  password authentication failed", agentv1.LogLevel_LOG_LEVEL_WARN},
		{"ERROR:  relation does not exist", agentv1.LogLevel_LOG_LEVEL_WARN},
		{"pg_restore: warning: errors ignored", agentv1.LogLevel_LOG_LEVEL_WARN},
		{"pg_restore: connecting to database", agentv1.LogLevel_LOG_LEVEL_INFO},
	}
	for _, tt := range tests {
		l := &logs{}
		exec := &recordingExec{stderr: []string{tt.line, "   "}, code: 1}
		o := testConn(exec).run(context.Background(), l, "/bin/psql", nil, nil)
		if len(l.entries) != 1 || l.entries[0].level != tt.level || l.entries[0].text != tt.line {
			t.Errorf("%q: logged %+v", tt.line, l.entries)
		}
		if o.reason != tt.line {
			t.Errorf("%q: reason %q", tt.line, o.reason)
		}
	}
	// the line that names the error wins over the last line
	exec := &recordingExec{stderr: []string{"psql: error: boom", "psql: the end"}, code: 1}
	if o := testConn(exec).run(context.Background(), &logs{}, "/bin/psql", nil, nil); o.reason != "psql: error: boom" {
		t.Errorf("reason %q", o.reason)
	}
}

func TestALongReasonIsCutAtTheLimit(t *testing.T) {
	at := strings.Repeat("e", maxQuoteBytes)
	for line, want := range map[string]string{at: at, at + "x": at + "..."} {
		exec := &recordingExec{stderr: []string{line}, code: 1}
		if o := testConn(exec).run(context.Background(), &logs{}, "/bin/psql", nil, nil); o.reason != want {
			t.Errorf("%d bytes: reason has %d bytes, want %d", len(line), len(o.reason), len(want))
		}
	}
	if maxQuoteBytes != 512 {
		t.Errorf("the limit is %d", maxQuoteBytes)
	}
}

func TestTheConnectionStringHoldsTheModeAndTheTimeouts(t *testing.T) {
	c := testConn(&recordingExec{})
	c.plan.TLSMode = "verify-full"
	c.plan.TLSRootCert = "/etc/ca.pem"
	got := c.conninfo("app")
	for _, want := range []string{"host='db2'", "port='5432'", "dbname='app'", "user='restorer'", "sslmode='verify-full'", "sslrootcert='/etc/ca.pem'",
		"connect_timeout='30'", "keepalives_idle='60'", "keepalives_interval='10'", "keepalives_count='6'", "application_name='sard-agent'"} {
		if !strings.Contains(got, want) {
			t.Errorf("%s\nlacks %s", got, want)
		}
	}
	if c.sslmode() != "verify-full" {
		t.Errorf("sslmode %q", c.sslmode())
	}
}

func TestWhyAToolCannotBeStartedLeavesTheOperatingSystemWords(t *testing.T) {
	tests := []struct {
		err  error
		want string
	}{
		{fs.ErrNotExist, "no such file or directory"},
		{&fs.PathError{Op: "stat", Path: "/x", Err: syscall.ENOTDIR}, "no such file or directory"},
		{&fs.PathError{Op: "stat", Path: "/x", Err: syscall.ENOENT}, "no such file or directory"},
		{&fs.PathError{Op: "stat", Path: "/x", Err: syscall.EACCES}, "permission denied"},
		{errors.New("odd"), "odd"},
	}
	for _, tt := range tests {
		if got := reasonOf(tt.err); got != tt.want {
			t.Errorf("reasonOf(%v) = %q, want %q", tt.err, got, tt.want)
		}
	}
}
