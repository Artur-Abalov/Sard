// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package postgresql_test

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/fingerprint"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

// withSession is the direct plugin with its own fake session of the fingerprint.
func withSession() (*direct, *fakeSession) {
	d := newDirect()
	s := newSession()
	d.proc.on("psql session", s.behaviour())
	return d, s
}

// dumped runs Prepare and Dump of cfg.
func (d *direct) dumped(t *testing.T, cfg sdk.Config) sdk.Dump {
	t.Helper()
	ctx := context.Background()
	if err := d.plugin.Prepare(ctx, d.host, cfg); err != nil {
		t.Fatal(err)
	}
	dump, err := d.plugin.Dump(ctx, d.host, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if dump.Release != nil {
			dump.Release()
		}
	})
	return dump
}

func factMap(f sdk.Fingerprint) map[string]sdk.Fact {
	out := map[string]sdk.Fact{}
	for _, x := range f.Facts {
		out[x.Name] = x
	}
	return out
}

func exactFact(name, value string) sdk.Fact {
	return sdk.Fact{Name: name, Rule: sdk.RuleExact, Value: value}
}
func infoFact(name, value string) sdk.Fact {
	return sdk.Fact{Name: name, Rule: sdk.RuleInfo, Value: value}
}

// Scenario (V2): the fingerprint of a backup is the version, the extensions,
// the tables pg_dump dumps, the size and the rows of the largest tables.
func TestTheFingerprintOfABackupDescribesTheDatabase(t *testing.T) {
	d, _ := withSession()
	dump := d.dumped(t, cfgOf(k()))
	if dump.Fingerprint == nil || dump.Fingerprint.Plugin != "postgresql" || dump.Fingerprint.Format != sdk.FingerprintFormat {
		t.Fatalf("fingerprint = %+v", dump.Fingerprint)
	}
	want := []sdk.Fact{
		exactFact("major_version", "18"), infoFact("server_version", "18.1"),
		exactFact("extension:plpgsql", "installed"), infoFact("extension_version:plpgsql", "1.0"),
		exactFact("extension:pg_trgm", "installed"), infoFact("extension_version:pg_trgm", "1.6"),
		infoFact("database_size", "81920"), exactFact("tables", "3"), infoFact("key_tables", "the 3 largest"),
		exactFact("rows:public.orders", "42"), exactFact("rows:public.users", "7"), exactFact(`rows:app.x"y`, "1"),
	}
	if got := dump.Fingerprint.Facts; !slices.Equal(got, want) {
		t.Errorf("facts =\n%v\nwant\n%v", got, want)
	}
	if _, err := dump.Fingerprint.Encode(); err != nil {
		t.Error(err)
	}
}

// Scenario (V2): pg_dump dumps the snapshot the transaction of the
// fingerprint exported, and the transaction is still open while it runs.
func TestPgDumpDumpsTheExportedSnapshotWhileTheTransactionIsOpen(t *testing.T) {
	d, s := withSession()
	dump := d.dumped(t, cfgOf(k()))
	var during int
	d.proc.on("pg_dump", func(_ context.Context, c plugCmd) (int, error) {
		during = len(s.requests())
		_, err := c.Stdout.Write([]byte(archive(100)))
		return 0, err
	})
	if err := d.plugin.Stream(context.Background(), d.host, cfgOf(k()), dump, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if got := d.proc.ran("pg_dump")[0].arg("--snapshot"); got != fakeSnapshot {
		t.Errorf("--snapshot = %q", got)
	}
	if !strings.Contains(s.requests()[0], "BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY") || during == 0 {
		t.Errorf("requests %q, %d before pg_dump", s.requests(), during)
	}
	dump.Release()
	if len(d.proc.ran("psql session")) != 1 {
		t.Errorf("sessions: %d", len(d.proc.ran("psql session")))
	}
}

// A psql of the fingerprint that ends while pg_dump runs fails the backup:
// the dump and the fingerprint may not be of one state.
func TestALostTransactionDuringPgDumpFailsTheStream(t *testing.T) {
	d, s := withSession()
	s.exitLine = "psql: error: connection to server was lost"
	dump := d.dumped(t, cfgOf(k()))
	release := make(chan struct{})
	d.proc.on("pg_dump", func(ctx context.Context, c plugCmd) (int, error) {
		_, _ = c.Stdout.Write([]byte(archive(100)))
		close(release)
		<-ctx.Done()
		return -1, errTerminated
	})
	go func() { <-release; dump.Release() }()
	err := d.plugin.Stream(context.Background(), d.host, cfgOf(k()), dump, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "pg_dump cannot be trusted: the psql session of the fingerprint ended") {
		t.Errorf("Stream: %v", err)
	}
}

func TestAStreamWithoutAnExportedSnapshotFails(t *testing.T) {
	d, _ := withSession()
	cfg := cfgOf(k())
	dump := d.dumped(t, cfg)
	dump.Release()
	if err := d.plugin.Stream(context.Background(), d.host, cfg, dump, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "no snapshot of the fingerprint") {
		t.Errorf("Stream: %v", err)
	}
	if len(d.proc.ran("pg_dump")) != 0 {
		t.Error("pg_dump ran")
	}
}

// A fingerprint that cannot be taken fails the backup before pg_dump: no
// backup goes without it.
func TestAFingerprintThatCannotBeTakenFailsTheDump(t *testing.T) {
	for name, tc := range map[string]struct {
		setup func(*fakeSession)
		want  string
	}{
		"psql cannot connect": {func(s *fakeSession) {
			s.exitAfter, s.exitCode, s.exitLine = 1, 2, `psql: error: connection to server at "db" failed: FATAL:  password authentication failed`
		}, "password authentication failed"},
		"no transaction":   {func(s *fakeSession) { s.answer("transaction_isolation", "read committed on") }, "did not open the transaction"},
		"no snapshot":      {func(s *fakeSession) { s.fail("pg_export_snapshot") }, "the snapshot could not be exported"},
		"no version":       {func(s *fakeSession) { s.fail("server_version_num") }, "the query of the version failed"},
		"bad version":      {func(s *fakeSession) { s.answer("server_version_num", "x y") }, "unexpected version"},
		"no extensions":    {func(s *fakeSession) { s.fail("pg_extension") }, "the query of the extensions failed"},
		"bad extensions":   {func(s *fakeSession) { s.answer("pg_extension", "{") }, "unexpected list of extensions"},
		"no size":          {func(s *fakeSession) { s.fail("pg_database_size") }, "the query of the size of the database failed"},
		"no tables":        {func(s *fakeSession) { s.fail("json_build_object('s'") }, "the query of the tables failed"},
		"bad tables":       {func(s *fakeSession) { s.answer("json_build_object('s'", "[1]") }, "unexpected list of tables"},
		"psql ends midway": {func(s *fakeSession) { s.exitAfter = 4 }, "the psql session of the fingerprint ended"},
	} {
		t.Run(name, func(t *testing.T) {
			d, s := withSession()
			tc.setup(s)
			cfg := cfgOf(k())
			if err := d.plugin.Prepare(context.Background(), d.host, cfg); err != nil {
				t.Fatal(err)
			}
			if _, err := d.plugin.Dump(context.Background(), d.host, cfg); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Dump: %v, want %q", err, tc.want)
			}
		})
	}
}

func TestABadExcludePatternFailsTheDump(t *testing.T) {
	d, _ := withSession()
	cfg := cfgOf(k(map[string]any{"exclude_tables": []string{"a.b.c.d"}}))
	if err := d.plugin.Prepare(context.Background(), d.host, cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := d.plugin.Dump(context.Background(), d.host, cfg); err == nil || !strings.Contains(err.Error(), "too many dotted names") {
		t.Errorf("Dump: %v", err)
	}
}

// The tables the patterns exclude are left out by the query itself.
func TestTheExcludePatternsReachTheQueryOfTheTables(t *testing.T) {
	d, s := withSession()
	d.dumped(t, cfgOf(k(map[string]any{"exclude_schemas": []string{"audit"}, "exclude_tables": []string{"log_*"}})))
	i := slices.IndexFunc(s.requests(), func(r string) bool { return strings.Contains(r, "json_build_object('s'") })
	if i < 0 || !strings.Contains(s.requests()[i], `AND NOT ((n.nspname OPERATOR(pg_catalog.~) '^(audit)$'`) || !strings.Contains(s.requests()[i], `'^(log_.*)$'`) {
		t.Errorf("query = %q", s.requests())
	}
}

// Scenario (V2): configured key tables are counted instead of the largest; one
// that the dump does not hold is missing, with a warning.
func TestConfiguredKeyTablesAreCountedAndAMissingOneIsSaid(t *testing.T) {
	d, _ := withSession()
	dump := d.dumped(t, cfgOf(k(map[string]any{"key_tables": []string{"public.users", "public.gone"}})))
	facts := factMap(*dump.Fingerprint)
	if facts["rows:public.users"] != exactFact("rows:public.users", "7") || facts["rows:public.gone"] != exactFact("rows:public.gone", "missing") {
		t.Errorf("facts = %v", facts)
	}
	if _, counted := facts["rows:public.orders"]; counted || facts["key_tables"].Value != "configured" {
		t.Errorf("facts = %v", facts)
	}
	if !slices.ContainsFunc(d.host.logs, func(l string) bool { return strings.Contains(l, "key table public.gone is not in the dump") }) {
		t.Errorf("logs = %q", d.host.logs)
	}
}

// Without key tables, the 10 largest are counted.
func TestTheTenLargestTablesAreCounted(t *testing.T) {
	d, s := withSession()
	var list []string
	for i := range 12 {
		list = append(list, `{"s":"public","t":"t`+string(rune('a'+i))+`","b":`+string(rune('0'+i%10))+`}`)
	}
	s.answer("json_build_object('s'", "["+strings.Join(list, ",")+"]")
	s.answer(`SELECT 'R' || count(*)`, "5")
	dump := d.dumped(t, cfgOf(k()))
	facts := factMap(*dump.Fingerprint)
	if facts["key_tables"].Value != "the 10 largest" || facts["tables"].Value != "12" {
		t.Errorf("facts = %v", facts)
	}
	counted := 0
	for name := range facts {
		if strings.HasPrefix(name, "rows:") {
			counted++
		}
	}
	// ta and tk weigh 0 bytes: they are the two left out.
	if _, ok := facts["rows:public.ta"]; counted != 10 || ok {
		t.Errorf("counted %d: %v", counted, facts)
	}
}

// Scenario (V2): a count over its limit is recorded as not counted; the
// backup goes on, and the copy cannot be judged on that table.
func TestACountOverTheLimitIsInformationNotAFailure(t *testing.T) {
	d, s := withSession()
	s.fail(`FROM "public"."orders"`)
	d.plugin.CountTimeout = 1500 * time.Millisecond
	dump := d.dumped(t, cfgOf(k()))
	got := factMap(*dump.Fingerprint)["rows:public.orders"]
	if got != infoFact("rows:public.orders", "not counted: the count failed or took longer than 1.5s") {
		t.Errorf("orders = %+v", got)
	}
	i := slices.IndexFunc(s.requests(), func(r string) bool { return strings.Contains(r, `FROM "public"."orders"`) })
	if !strings.Contains(s.requests()[i], "SET LOCAL statement_timeout = 1500;") {
		t.Errorf("request = %q", s.requests()[i])
	}
	if !strings.Contains(s.requests()[i+1], "ROLLBACK TO SAVEPOINT sard_q;") {
		t.Errorf("the failed count is not rolled back to its savepoint: %q", s.requests()[i+1])
	}
	if last := s.requests()[len(s.requests())-1]; !strings.Contains(last, "SET LOCAL statement_timeout = 0;") {
		t.Errorf("the limit stays after the counts: %q", last)
	}
}

func TestTheCountsStopAtTheirTimeBudget(t *testing.T) {
	d, _ := withSession()
	now := time.Date(2026, 10, 25, 3, 0, 0, 0, time.UTC)
	d.plugin.Now = func() time.Time { now = now.Add(20 * time.Second); return now }
	d.plugin.CountBudget = 50 * time.Second
	d.plugin.CountTimeout = time.Hour
	dump := d.dumped(t, cfgOf(k()))
	facts := factMap(*dump.Fingerprint)
	// The deadline is set at +20 s to +70 s: orders at +40 s, users at +60 s, x"y past it.
	if facts["rows:public.orders"].Rule != sdk.RuleExact || facts["rows:public.users"].Rule != sdk.RuleExact ||
		facts[`rows:app.x"y`] != infoFact(`rows:app.x"y`, "not counted: the time limit of the counts was spent") {
		t.Errorf("facts = %v", facts)
	}
}

// Scenario (V2): the same Fingerprint, pointed at a restored copy, counts the
// tables the source counted; a row deleted from the copy is a mismatch that
// names the table.
func TestTheFingerprintOfACopyIsComparedWithTheSource(t *testing.T) {
	d, _ := withSession()
	src := d.dumped(t, cfgOf(k())).Fingerprint
	copied := newSession()
	copied.answer(`FROM "public"."users"`, "6")
	copied.answer("json_build_object('s'", `[{"s":"public","t":"users","b":1},{"s":"public","t":"orders","b":1},{"s":"app","t":"x\"y","b":1}]`)
	d.proc.on("psql session", copied.behaviour())
	got, err := d.plugin.Fingerprint(context.Background(), d.host, cfgOf(k(map[string]any{"database": "copy"})), sdk.FingerprintTarget{Reference: src})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := factMap(got)["key_tables"]; ok {
		t.Errorf("a copy records how key tables were chosen: %v", got.Facts)
	}
	r := fingerprint.Compare(*src, got)
	if want := []fingerprint.Mismatch{{Name: "rows:public.users", Expected: "7", Actual: "6"}}; !slices.Equal(r.Mismatches, want) || !slices.Equal(r.Unverified, nil) {
		t.Errorf("Compare = %+v", r)
	}
	if slices.ContainsFunc(copied.requests(), func(r string) bool { return strings.Contains(r, "pg_export_snapshot") }) {
		t.Error("a copy exports a snapshot")
	}
}

func TestTheFingerprintOfACopyOnlyCountsTheExactCountsOfTheReference(t *testing.T) {
	d, s := withSession()
	ref := sdk.Fingerprint{Plugin: "postgresql", Format: 1, Facts: []sdk.Fact{
		exactFact("rows:public.orders", "42"), infoFact("rows:public.users", "not counted"),
	}}
	got, err := d.plugin.Fingerprint(context.Background(), d.host, cfgOf(k()), sdk.FingerprintTarget{Reference: &ref})
	if err != nil {
		t.Fatal(err)
	}
	facts := factMap(got)
	if _, ok := facts["rows:public.users"]; ok || facts["rows:public.orders"].Value != "42" {
		t.Errorf("facts = %v", facts)
	}
	if slices.ContainsFunc(s.requests(), func(r string) bool { return strings.Contains(r, `"public"."users"`) }) {
		t.Error("an inexact count of the reference was counted")
	}
}

func TestTheFingerprintOfAnInvalidConfigOrSecretFails(t *testing.T) {
	d, _ := withSession()
	if _, err := d.plugin.Fingerprint(context.Background(), d.host, sdk.Config("{"), sdk.FingerprintTarget{}); !errors.Is(err, sdk.ErrInvalidConfig) {
		t.Errorf("config: %v", err)
	}
	if _, err := d.plugin.Fingerprint(context.Background(), d.host, cfgOf(k(map[string]any{"password_ref": "other"})), sdk.FingerprintTarget{}); err == nil {
		t.Error("an unknown secret passed")
	}
	if d.proc.started() != 0 {
		t.Errorf("started %d", d.proc.started())
	}
}

func TestAFingerprintThatCannotStartPsqlFails(t *testing.T) {
	d, s := withSession()
	s.exitAfter, s.exitCode = 1, 2
	if _, err := d.plugin.Fingerprint(context.Background(), d.host, cfgOf(k()), sdk.FingerprintTarget{}); err == nil {
		t.Error("no error")
	}
}

// A cancelled step stops the fingerprint with its cause.
func TestACancelledFingerprintReturnsTheCause(t *testing.T) {
	d, s := withSession()
	cause := errors.New("cancelled")
	ctx, cancel := context.WithCancelCause(context.Background())
	s.mu.Lock()
	s.hold = make(chan struct{})
	s.mu.Unlock()
	d.proc.on("psql session", func(c context.Context, cmd plugCmd) (int, error) {
		cancel(cause)
		<-c.Done()
		return -1, errTerminated
	})
	if _, err := d.plugin.Fingerprint(ctx, d.host, cfgOf(k()), sdk.FingerprintTarget{}); !errors.Is(err, cause) {
		t.Errorf("err = %v", err)
	}
}
