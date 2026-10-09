// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package postgresql_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

// conninfo is a parsed libpq connection string; keys keeps every key in
// order, so that a repeated key is visible.
type conninfo struct {
	keys   []string
	values map[string]string
}

// parseConninfo reads key='value' pairs with \\ and \' escapes.
func parseConninfo(t *testing.T, s string) conninfo {
	t.Helper()
	c := conninfo{values: map[string]string{}}
	for i := 0; i < len(s); {
		eq := strings.Index(s[i:], "='")
		if eq < 0 {
			t.Fatalf("conninfo %q: bad pair at %d", s, i)
		}
		key := s[i : i+eq]
		value, end, ok := quoted(s, i+eq+2)
		if !ok {
			t.Fatalf("conninfo %q: unterminated value of %s", s, key)
		}
		c.keys = append(c.keys, key)
		c.values[key] = value
		i = end + 1
		if i < len(s) && s[i] == ' ' {
			i++
		}
	}
	return c
}

// quoted reads a value from s[from:] up to its closing quote; end is the
// index of that quote.
func quoted(s string, from int) (value string, end int, ok bool) {
	var v strings.Builder
	for j := from; j < len(s); j++ {
		switch {
		case s[j] == '\'':
			return v.String(), j, true
		case s[j] == '\\' && j+1 < len(s):
			j++
			v.WriteByte(s[j])
		default:
			v.WriteByte(s[j])
		}
	}
	return "", 0, false
}

// connection is the connection string psql and pg_dump got.
func connection(t *testing.T, c call) conninfo {
	t.Helper()
	return parseConninfo(t, c.arg("--dbname"))
}

// Scenario: Фазы шага идут от подготовки к загрузке.
func TestPhasesGoFromPreparingToUploading(t *testing.T) {
	r := newRig(t)
	want(t, r.backup(k()), succeeded)
	if got, want := r.sink.phases(), []agentv1.StepPhase{agentv1.StepPhase_STEP_PHASE_ACCEPTED, preparing, dumping, uploading}; !slices.Equal(got, want) {
		t.Errorf("phases = %v, want %v", got, want)
	}
	if got := r.proc.ran("psql"); len(got) != 1 || got[0].Phase != preparing {
		t.Errorf("psql: %+v", got)
	}
	if got := r.proc.ran("pg_dump"); len(got) != 1 || got[0].Phase != uploading {
		t.Errorf("pg_dump: %+v", got)
	}
}

// Scenario: pg_dumpall выгружает глобальные объекты в фазе DUMPING до restic.
func TestPgDumpallDumpsTheGlobalObjectsWhileDumpingBeforeRestic(t *testing.T) {
	r := newRig(t)
	finished := false
	inner := say(globalsSQL, nil, 0)
	r.proc.on("pg_dumpall", func(ctx context.Context, c plugCmd) (int, error) {
		code, err := inner(ctx, c)
		finished = true
		return code, err
	})
	var finishedBeforeRestic bool
	r.restic.backup = func(ctx context.Context, n int, cmd resticCommand) int {
		if n == 0 {
			finishedBeforeRestic = finished
		}
		return r.restic.defaultBackup(ctx, n, cmd)
	}
	want(t, r.backup(kg()), succeeded)
	if got := r.proc.ran("pg_dumpall"); len(got) != 1 || got[0].Phase != dumping || !finishedBeforeRestic {
		t.Errorf("pg_dumpall: %+v, finished before restic: %v", got, finishedBeforeRestic)
	}
}

// Scenario: Ошибка подключения или входа проваливает шаг с причиной PostgreSQL.
func TestConnectionFailureFailsTheStepWithTheReasonOfPostgreSQL(t *testing.T) {
	const conn = `psql: error: connection to server at "db" (10.0.0.2), port 5432 failed: `
	// The first six are the output of psql 16 (testdata), the rest is how
	// libpq words the other failures.
	cases := []struct{ name, stderr, reason string }{
		{"wrong password", testdata(t, "psql-pg16-wrong-password.stderr"), "password authentication failed for user"},
		{"no database", testdata(t, "psql-pg16-no-database.stderr"), `database "nope" does not exist`},
		{"no role", testdata(t, "psql-pg16-no-role.stderr"), "password authentication failed for user"},
		{"nobody listens", testdata(t, "psql-pg16-refused.stderr"), "Connection refused"},
		{"unknown host", testdata(t, "psql-pg16-unknown-host.stderr"), "could not translate host name"},
		{"no TLS on the server", testdata(t, "psql-pg16-no-ssl.stderr"), "server does not support SSL"},
		{"pg_hba", conn + `FATAL:  no pg_hba.conf entry for host "10.0.0.9", user "backup", database "app", no encryption`, "no pg_hba.conf entry"},
		{"no CONNECT", conn + `FATAL:  permission denied for database "app"`, `permission denied for database "app"`},
		{"wrong host name in the certificate", conn + `server certificate for "x" does not match host name "db"`, "does not match host name"},
		{"wrong CA", conn + `SSL error: certificate verify failed`, "certificate verify failed"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t)
			r.proc.on("psql", say("", strings.Split(strings.TrimSpace(c.stderr), "\n"), 2))
			res := r.backup(k())
			want(t, res, failed)
			mentions(t, res.GetMessage(), c.reason)
			oneLine(t, res.GetMessage())
			r.notDisclosed(res)
			r.noDump(t)
			r.lastPhase(t, preparing)
			noOutput(t, res)
		})
	}
}

// The tools of PostgreSQL 16 say what the plugin expects (testdata is their
// real output).
func TestRealOutputOfPostgreSQL16IsUnderstood(t *testing.T) {
	r := newRig(t)
	r.proc.on("pg_dump --version", say(testdata(t, "pg_dump-pg16.version"), nil, 0))
	r.proc.on("psql", say(testdata(t, "psql-pg16-probe.stdout"), nil, 0))
	want(t, r.backup(k()), succeeded)
	tags := tagsOf(r.restic.backups()[0])
	for _, w := range []string{"postgresql.pg_dump_version=16.15", "postgresql.server_version=16.15"} {
		if !slices.Contains(tags, w) {
			t.Errorf("tags %q lack %s", tags, w)
		}
	}
}

// Scenario: Таблица без права чтения проваливает шаг без снимка и называется.
func TestTableWithoutSelectFailsTheStepAndIsNamed(t *testing.T) {
	r := newRig(t)
	r.proc.on("pg_dump", say("", strings.Split(strings.TrimSpace(testdata(t, "pg_dump-pg16-denied.stderr")), "\n"), 1))
	res := r.backup(k())
	want(t, res, failed)
	mentions(t, res.GetMessage(), "pg_dump", "permission denied for table t")
	oneLine(t, res.GetMessage())
	if len(r.restic.stdin) != 0 {
		t.Errorf("snapshots stored: %d", len(r.restic.stdin))
	}
}

// Scenario: Сообщение PostgreSQL с паролем маскируется.
func TestMessageOfPostgreSQLWithThePasswordIsMasked(t *testing.T) {
	r := newRig(t)
	r.proc.on("psql", fail(`psql: error: connection failed: FATAL:  password "`+P+`" rejected`))
	res := r.backup(k())
	want(t, res, failed)
	r.notDisclosed(res)
	if !strings.Contains(res.GetMessage(), "rejected") {
		t.Errorf("message = %q", res.GetMessage())
	}
}

// Scenario: Недоступный хост проваливает подготовку не позже чем через 30 секунд.
func TestUnreachableHostFailsPreparationWithTheTimeoutOfLibpq(t *testing.T) {
	r := newRig(t)
	r.proc.on("psql", fail(`psql: error: connection to server at "db" (10.0.0.2), port 5432 failed: timeout expired`))
	res := r.backup(k())
	want(t, res, failed)
	mentions(t, res.GetMessage(), "timeout expired")
	if got := connection(t, r.proc.ran("psql")[0]).values["connect_timeout"]; got != "30" {
		t.Errorf("connect_timeout = %q", got)
	}
}

// Scenario: Роль без pg_read_all_data получает предупреждение, шаг продолжается.
func TestRoleWithoutReadAllDataGetsAWarning(t *testing.T) {
	r := newRig(t)
	r.proc.on("psql", say(psqlRow(180000, false, false, "backup"), nil, 0))
	want(t, r.backup(k()), succeeded)
	w := r.sink.warnings()
	if len(w) != 1 || !strings.Contains(w[0], `"backup"`) || !strings.Contains(w[0], "pg_read_all_data") {
		t.Errorf("warnings = %q", w)
	}
	if len(r.proc.ran("pg_dump")) != 1 {
		t.Error("pg_dump did not start")
	}
}

// Scenario: Роль с pg_read_all_data предупреждения не получает.
func TestRoleWithReadAllDataGetsNoWarning(t *testing.T) {
	r := newRig(t)
	want(t, r.backup(k()), succeeded)
	if w := r.sink.warnings(); len(w) != 0 {
		t.Errorf("warnings = %q", w)
	}
}

// A superuser reads everything: no warning either.
func TestSuperuserGetsNoWarning(t *testing.T) {
	r := newRig(t)
	r.proc.on("psql", say(psqlRow(180000, true, true, "postgres"), nil, 0))
	want(t, r.backup(k()), succeeded)
	if w := r.sink.warnings(); len(w) != 0 {
		t.Errorf("warnings = %q", w)
	}
}

// A superuser who is not a member of pg_read_all_data reads everything anyway.
func TestSuperuserWithoutMembershipGetsNoWarning(t *testing.T) {
	r := newRig(t)
	r.proc.on("psql", say(psqlRow(180000, true, false, "postgres"), nil, 0))
	want(t, r.backup(k()), succeeded)
	if w := r.sink.warnings(); len(w) != 0 {
		t.Errorf("warnings = %q", w)
	}
}

// The role name may hold the separator of the row.
func TestRoleNameMayContainTheSeparatorOfTheAnswer(t *testing.T) {
	r := newRig(t)
	r.proc.on("psql", say(psqlRow(180000, false, false, "ba|ckup"), nil, 0))
	want(t, r.backup(k()), succeeded)
	if w := r.sink.warnings(); len(w) != 1 || !strings.Contains(w[0], `"ba|ckup"`) {
		t.Errorf("warnings = %q", w)
	}
}

// The answer of psql is not what the plugin asked for.
func TestUnintelligibleAnswerOfPsqlFailsTheStep(t *testing.T) {
	for _, answer := range []string{"", "180000|t|t\n", "x|t|t|backup\n"} {
		r := newRig(t)
		r.proc.on("psql", say(answer, nil, 0))
		res := r.backup(k())
		want(t, res, failed)
		mentions(t, res.GetMessage(), "psql")
		r.noDump(t)
	}
}

// psql asks with the flags of F1 ПГ1.
func TestPsqlRunsWithTheFlagsOfTheProbe(t *testing.T) {
	r := newRig(t)
	want(t, r.backup(k()), succeeded)
	args := r.proc.ran("psql")[0].Args
	for _, f := range []string{"-X", "-w", "-A", "-t", "ON_ERROR_STOP=1", "-c"} {
		if !slices.Contains(args, f) {
			t.Errorf("args %q lack %s", args, f)
		}
	}
}

// Scenario: Пароль доходит до psql и pg_dump только через PGPASSWORD.
func TestPasswordReachesTheToolsOnlyThroughPGPASSWORD(t *testing.T) {
	r := newRig(t)
	res := r.backup(kg())
	want(t, res, succeeded)
	for _, key := range []string{"psql", "pg_dump", "pg_dumpall"} {
		c := r.proc.ran(key)[0]
		if v, _ := c.env("PGPASSWORD"); v != P {
			t.Errorf("%s: PGPASSWORD = %q", key, v)
		}
	}
	r.notDisclosed(res)
	if files := r.filesContaining(P); len(files) != 0 {
		t.Errorf("files with the password: %q", files)
	}
}

// Scenario: Завершающий перевод строки в файле секрета не входит в пароль.
func TestLineEndingOfTheSecretFileIsNotPartOfThePassword(t *testing.T) {
	r := newRig(t)
	r.secrets.set(P + "\r\n")
	want(t, r.backup(k()), succeeded)
	for _, key := range []string{"psql", "pg_dump"} {
		if v, _ := r.proc.ran(key)[0].env("PGPASSWORD"); v != P {
			t.Errorf("%s: PGPASSWORD = %q", key, v)
		}
	}
}

// Scenario: Пустой или содержащий NUL секрет проваливает шаг до подключения.
func TestEmptySecretOrOneWithNULFailsTheStepBeforeConnecting(t *testing.T) {
	cases := []struct{ name, content, reason string }{
		{"empty", "", "is empty"},
		{"a line ending", "\n", "is empty"},
		{"NUL", "ab\x00cd", "contains a NUL byte"},
		{"NUL first", "\x00abcd", "contains a NUL byte"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t)
			r.secrets.set(c.content)
			res := r.backup(k())
			want(t, res, failed)
			mentions(t, res.GetMessage(), "pg-app", c.reason)
			if r.proc.started() != 0 {
				t.Errorf("processes started: %d", r.proc.started())
			}
		})
	}
}

// Scenario: Переменные PG и LC_ALL окружения агента не влияют на подключение.
func TestPGVariablesAndLC_ALLOfTheAgentDoNotReachTheTools(t *testing.T) {
	r := newRig(t)
	r.env = append(r.env, "PGHOST=other", "PGPASSWORD=wrong", "PGSSLMODE=disable", "PGOPTIONS=-c x=1", "LC_ALL=ru_RU.UTF-8", "LC_MESSAGES=ru_RU.UTF-8")
	want(t, r.backup(kg()), succeeded)
	for _, key := range []string{"psql", "pg_dump", "pg_dumpall", "pg_dump --version"} {
		checkChildEnv(t, key, r.proc.ran(key)[0])
	}
	for _, key := range []string{"psql", "pg_dump", "pg_dumpall"} {
		if v, _ := r.proc.ran(key)[0].env("PGPASSWORD"); v != P {
			t.Errorf("%s: PGPASSWORD = %q", key, v)
		}
	}
	if v, ok := r.proc.ran("pg_dump --version")[0].env("PGPASSWORD"); ok {
		t.Errorf("--version got PGPASSWORD %q", v)
	}
}

// checkChildEnv: no variable of libpq or of the locale, English messages,
// the rest of the environment of the agent.
func checkChildEnv(t *testing.T, key string, c call) {
	t.Helper()
	for _, name := range []string{"PGHOST", "PGSSLMODE", "PGOPTIONS", "LC_ALL"} {
		if v, ok := c.env(name); ok {
			t.Errorf("%s: %s = %q", key, name, v)
		}
	}
	if v, _ := c.env("LC_MESSAGES"); v != "C" {
		t.Errorf("%s: LC_MESSAGES = %q", key, v)
	}
	if home, _ := c.env("HOME"); home != "/var/lib/sard" {
		t.Errorf("%s: HOME = %q", key, home)
	}
}

// Scenario: Имя базы, похожее на строку подключения, остаётся именем базы.
func TestDatabaseNameLookingLikeAConnectionStringStaysAName(t *testing.T) {
	const name = "app' host=evil password=x"
	r := newRig(t)
	want(t, r.backup(k(o{"database": name})), succeeded)
	for _, key := range []string{"psql", "pg_dump"} {
		raw := r.proc.ran(key)[0].arg("--dbname")
		if !strings.Contains(raw, `dbname='app\' host=evil password=x'`) {
			t.Errorf("%s: conninfo = %q", key, raw)
		}
		c := parseConninfo(t, raw)
		if c.values["dbname"] != name || c.values["host"] != "db" || slices.Contains(c.keys, "password") {
			t.Errorf("%s: parsed %+v", key, c)
		}
		if n := strings.Count(strings.Join(c.keys, " ")+" ", "host "); n != 1 {
			t.Errorf("%s: host appears %d times in %q", key, n, c.keys)
		}
	}
}

// Scenario: Параметры подключения передаются строкой подключения с таймаутами.
func TestConnectionParametersGoInAConnectionStringWithTimeouts(t *testing.T) {
	r := newRig(t)
	want(t, r.backup(kg(o{"port": 6432})), succeeded)
	for _, key := range []string{"psql", "pg_dump", "pg_dumpall"} {
		c := connection(t, r.proc.ran(key)[0])
		for k, v := range map[string]string{
			"host": "db", "port": "6432", "dbname": "app", "user": "backup", "sslmode": "disable",
			"connect_timeout": "30", "keepalives_idle": "60", "keepalives_interval": "10", "keepalives_count": "6",
			"application_name": "sard-agent",
		} {
			if c.values[k] != v {
				t.Errorf("%s: %s = %q, want %q", key, k, c.values[k], v)
			}
		}
	}
}

// Scenario: Без tls_mode для TCP-хоста используется require.
func TestWithoutTlsModeATcpHostGetsRequire(t *testing.T) {
	r := newRig(t)
	want(t, r.backup(k(o{"tls_mode": nil})), succeeded)
	if got := connection(t, r.proc.ran("psql")[0]).values["sslmode"]; got != "require" {
		t.Errorf("sslmode = %q", got)
	}
}

// Scenario: Для сокета без tls_mode используется disable.
func TestWithoutTlsModeASocketGetsDisable(t *testing.T) {
	r := newRig(t)
	want(t, r.backup(k(o{"host": "/var/run/postgresql", "tls_mode": nil})), succeeded)
	c := connection(t, r.proc.ran("psql")[0])
	if c.values["host"] != "/var/run/postgresql" || c.values["sslmode"] != "disable" {
		t.Errorf("conninfo = %+v", c)
	}
}

// Scenario: tls_root_cert передаётся как sslrootcert.
func TestTlsRootCertGoesAsSslrootcert(t *testing.T) {
	r := newRig(t)
	want(t, r.backup(k(o{"tls_mode": "verify-full", "tls_root_cert": "/etc/sard/pg-ca.pem"})), succeeded)
	c := connection(t, r.proc.ran("psql")[0])
	if c.values["sslmode"] != "verify-full" || c.values["sslrootcert"] != "/etc/sard/pg-ca.pem" {
		t.Errorf("conninfo = %+v", c)
	}
	r = newRig(t)
	want(t, r.backup(k()), succeeded)
	if c := connection(t, r.proc.ran("psql")[0]); slices.Contains(c.keys, "sslrootcert") {
		t.Errorf("conninfo = %+v", c)
	}
}

// Scenario: Отмена во время подготовки не запускает дамп.
func TestCancelDuringPreparationDoesNotStartTheDump(t *testing.T) {
	r := newRig(t)
	stopped := make(chan struct{})
	r.proc.on("psql", func(ctx context.Context, _ plugCmd) (int, error) {
		<-ctx.Done()
		close(stopped)
		return -1, errTerminated
	})
	r.exec.Submit(step(js(k())))
	waitFor(t, "psql", func() bool { return len(r.proc.ran("psql")) == 1 })
	r.exec.Cancel("c1")
	res := r.result()
	want(t, res, cancelled)
	<-stopped
	r.noDump(t)
}

// testdata is a file of the real output of the tools of PostgreSQL 16.
func testdata(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
