// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package consolerestore_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/consolerestore"
	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

// The rig of the restore of a PostgreSQL database (R2b): a repository that
// holds the snapshots P and G, a PostgreSQL server that is a model of the
// parts of a real one the agent talks to, and the tools of the host.

const (
	pgSnapshotID      = "bbbb1111bbbb1111bbbb1111bbbb1111bbbb1111bbbb1111bbbb1111bbbb1111"
	pgGlobalsID       = "cccc1111cccc1111cccc1111cccc1111cccc1111cccc1111cccc1111cccc1111"
	pgNewDatabase     = "app_restored_20261011"
	pgBin             = "/usr/lib/postgresql/16/bin"
	secretValue       = `r'e:s\t w0rd-Ж`
	passwordSecret    = "pg-restore"
	defaultGlobalsSQL = "CREATE ROLE app_rw;\nALTER ROLE app_rw WITH NOSUPERUSER INHERIT;\nCREATE ROLE app_ro;\nGRANT app_ro TO app_rw;\n"
)

// --- the tools of the host

type toolFS struct {
	nodes map[string]fs.FileMode
	// errs are the errors Stat answers with instead of looking at nodes.
	errs map[string]error
}

type toolInfo struct {
	name string
	mode fs.FileMode
}

func (i toolInfo) Name() string      { return i.name }
func (toolInfo) Size() int64         { return 0 }
func (i toolInfo) Mode() fs.FileMode { return i.mode }
func (toolInfo) ModTime() time.Time  { return time.Time{} }
func (i toolInfo) IsDir() bool       { return i.mode.IsDir() }
func (toolInfo) Sys() any            { return nil }
func (f toolFS) Stat(name string) (fs.FileInfo, error) {
	if err := f.errs[name]; err != nil {
		return nil, err
	}
	mode, ok := f.nodes[name]
	if !ok {
		return nil, &fs.PathError{Op: "stat", Path: name, Err: syscall.ENOENT}
	}
	return toolInfo{filepath.Base(name), mode}, nil
}

// --- the PostgreSQL server

// pgCall is one process the agent started.
type pgCall struct {
	Tool  string
	Path  string
	Args  []string
	Env   []string
	Stdin string
	// Vars are the psql variables (-v name=value).
	Vars map[string]string
	// StdinReader is the standard input of a pg_restore of the dump, as the process sees it.
	StdinReader io.Reader
	// Deadline is the time the context of the call had left when it started, 0 if it had none.
	Deadline time.Duration
}

func (c pgCall) has(arg string) bool { return slices.Contains(c.Args, arg) }

func (c pgCall) env(name string) (string, bool) {
	for _, kv := range c.Env {
		if v, ok := strings.CutPrefix(kv, name+"="); ok {
			return v, true
		}
	}
	return "", false
}

// conninfo is the --dbname= of the call.
func (c pgCall) conninfo() string {
	for _, a := range c.Args {
		if v, ok := strings.CutPrefix(a, "--dbname="); ok {
			return v
		}
	}
	return ""
}

// pgServer is a PostgreSQL server as far as the agent sees it: it answers the
// queries of the agent, creates and drops databases and roles, and fails where
// a test says it does.
type pgServer struct {
	mu sync.Mutex

	version       int // server_version_num
	super         bool
	createdb      bool
	user          string
	systemID      string
	systemIDError string
	locks, conns  int
	prepared      int
	bootstrap     string
	tables        int  // tables the new database holds after a restore
	toc           int  // entries of the table of contents of the dump
	tocBlank      bool // the listing holds blank lines

	databases map[string]uint32
	roles     map[string]uint32
	nextOID   uint32

	// failures
	connectError     string // psql cannot sign in
	createError      string // CREATE DATABASE
	globalsError     string // the script of the global objects
	dropDBError      string
	dropRoleError    map[string]string
	roleOIDError     string
	tocError         string
	toolVersion      string // pg_restore --version
	versionOutput    string // replaces the answer of pg_restore --version
	versionFails     bool
	versionHook      func()
	stderr           []string // printed by pg_restore of the dump
	restoreError     string   // pg_restore of the dump, exit 1
	restoreHook      func(ctx context.Context, c pgCall) (int, error)
	createHook       func(name string, oid uint32) // right after CREATE DATABASE
	afterGlobals     func()
	killOnCreate     bool // CREATE DATABASE succeeds, the psql dies before it prints the OID
	killAfterGlobals bool // the script commits, the psql dies before it ends
	globalsExit      int  // the psql ends with this code after the script committed (not 3, not killed)
	createExit       int  // CREATE DATABASE fails with this exit code instead of 3
	exitErr          bool // globalsExit and createExit come with an error of the process, as after a signal

	calls  []pgCall
	events []string
	// answers replace the output of the script that contains the key, failOn
	// make it fail with the text; hooks run when the event begins.
	answers map[string]string
	failOn  map[string]string
	// failOut is failOn that prints lines to stdout as well.
	failOut map[string][2]string
	hooks   map[string]func()
	// journalFile is the file of the rollback journal of the step; journalAt
	// is what it held when each event began.
	journalFile string
	journalAt   map[string]string
}

func newPGServer() *pgServer {
	return &pgServer{
		version: 160004, createdb: true, user: "restorer", systemID: "7412345678901234567",
		locks: 64, conns: 100, bootstrap: "postgres", tables: 2, toc: 10, toolVersion: "16.4",
		databases: map[string]uint32{"postgres": 5, "template0": 4, "template1": 1, "app": 16384},
		roles:     map[string]uint32{"postgres": 10, "restorer": 16390}, nextOID: 20000,
		dropRoleError: map[string]string{}, answers: map[string]string{}, failOn: map[string]string{}, hooks: map[string]func(){},
		failOut: map[string][2]string{},
	}
}

func (s *pgServer) event(format string, args ...any) {
	name := fmt.Sprintf(format, args...)
	s.events = append(s.events, name)
	if hook := s.hooks[name]; hook != nil {
		hook()
	}
	if s.journalFile != "" {
		data, _ := os.ReadFile(s.journalFile)
		if s.journalAt == nil {
			s.journalAt = map[string]string{}
		}
		s.journalAt[name] = string(data)
	}
}

// ran returns the calls of a tool.
func (s *pgServer) ran(tool string) []pgCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []pgCall
	for _, c := range s.calls {
		if c.Tool == tool {
			out = append(out, c)
		}
	}
	return out
}

func (s *pgServer) eventLog() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.events)
}

func (s *pgServer) hasDatabase(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.databases[name]
	return ok
}

func (s *pgServer) hasRole(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.roles[name]
	return ok
}

// say writes the lines to the callback of stdout.
func say(cmd restic.Command, lines ...string) {
	for _, l := range lines {
		if cmd.Stdout != nil {
			cmd.Stdout([]byte(l))
		}
	}
}

func complain(cmd restic.Command, lines ...string) {
	for _, l := range lines {
		if cmd.Stderr != nil {
			cmd.Stderr([]byte(l))
		}
	}
}

func yn(b bool) string {
	if b {
		return "t"
	}
	return "f"
}

// Run implements restic.Executor for the tools of PostgreSQL.
func (s *pgServer) Run(ctx context.Context, cmd restic.Command) (int, error) {
	if err := ctx.Err(); err != nil {
		return -1, err // a process is not started for a context that has ended
	}
	call := pgCall{Tool: filepath.Base(cmd.Path), Path: cmd.Path, Args: slices.Clone(cmd.Args), Env: slices.Clone(cmd.Env), Vars: map[string]string{}, Deadline: timeLeft(ctx)}
	for i, a := range cmd.Args {
		if a == "-v" && i+1 < len(cmd.Args) {
			k, v, _ := strings.Cut(cmd.Args[i+1], "=")
			call.Vars[k] = v
		}
	}
	switch call.Tool {
	case "pg_restore":
		return s.pgRestore(ctx, call, cmd)
	case "psql":
		if cmd.Stdin != nil {
			data, _ := io.ReadAll(cmd.Stdin)
			call.Stdin = string(data)
		}
		s.mu.Lock()
		s.calls = append(s.calls, call)
		s.mu.Unlock()
		return s.psql(call, cmd)
	}
	return -1, fmt.Errorf("fake: unexpected tool %s", cmd.Path)
}

func (s *pgServer) pgRestore(ctx context.Context, call pgCall, cmd restic.Command) (int, error) {
	switch {
	case slices.Equal(cmd.Args, []string{"--version"}):
		return s.pgVersion(call, cmd)
	case call.has("-l"):
		return s.contents(call, cmd)
	}
	call.StdinReader = cmd.Stdin
	s.mu.Lock()
	s.calls = append(s.calls, call)
	s.event("pg_restore")
	hook, restoreError, stderr := s.restoreHook, s.restoreError, s.stderr
	s.mu.Unlock()
	complain(cmd, stderr...)
	if hook != nil {
		return hook(ctx, call)
	}
	data, _ := io.ReadAll(cmd.Stdin)
	s.mu.Lock()
	s.calls[len(s.calls)-1].Stdin = string(data)
	s.mu.Unlock()
	if restoreError != "" {
		complain(cmd, restoreError)
		return 1, nil
	}
	return 0, nil
}

// pgVersion answers pg_restore --version.
func (s *pgServer) pgVersion(call pgCall, cmd restic.Command) (int, error) {
	s.mu.Lock()
	s.calls = append(s.calls, call)
	hook, out, fails := s.versionHook, s.versionOutput, s.versionFails
	s.mu.Unlock()
	if hook != nil {
		hook()
	}
	if fails {
		if out != "" {
			say(cmd, out)
		}
		complain(cmd, "pg_restore: error: cannot run")
		return 127, nil
	}
	if out == "" {
		out = "pg_restore (PostgreSQL) " + s.toolVersion
	}
	say(cmd, out)
	return 0, nil
}

// contents answers pg_restore -l: it reads the head of the archive only.
func (s *pgServer) contents(call pgCall, cmd restic.Command) (int, error) {
	head := make([]byte, 5)
	n, _ := io.ReadFull(cmd.Stdin, head)
	call.Stdin = string(head[:n])
	s.mu.Lock()
	s.calls = append(s.calls, call)
	s.event("pg_restore -l")
	tocError, toc := s.tocError, s.toc
	s.mu.Unlock()
	if tocError != "" {
		complain(cmd, tocError)
		return 1, nil
	}
	say(cmd, ";", "; Archive created at 2026-10-11 03:00:00 UTC", ";     dbname: app")
	if s.tocBlank {
		say(cmd, "", "   ")
	}
	for i := range toc {
		say(cmd, fmt.Sprintf("%d; 1259 %d TABLE public t%d app", i+200, i+16500, i))
	}
	return 0, nil
}

// psqlHandler answers the script of psql that contains marker.
type psqlHandler struct {
	marker string
	run    func(s *pgServer, call pgCall, cmd restic.Command) (int, error)
}

// psqlHandlers are tried in order.
var psqlHandlers = []psqlHandler{
	{"server_version_num", (*pgServer).probe},
	{"pg_control_system", (*pgServer).systemIdentifier},
	{"max_locks_per_transaction", (*pgServer).lockCapacity},
	{"exists(select 1 from pg_database", (*pgServer).databaseExists},
	{"CREATE DATABASE", (*pgServer).createDatabase},
	{"select oid from pg_database", (*pgServer).databaseOID},
	{"from pg_database where oid", (*pgServer).databaseByOID},
	{"DROP DATABASE", (*pgServer).dropDatabase},
	{"from pg_class", (*pgServer).countTables},
	{"oid = 10", (*pgServer).existingRoles},
	{"select oid from pg_roles", (*pgServer).roleOID},
	{"from pg_roles where oid", (*pgServer).roleByOID},
	{"DROP ROLE", (*pgServer).dropRole},
}

// psql plays the server for one script of the agent.
func (s *pgServer) psql(call pgCall, cmd restic.Command) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.connectError != "" {
		complain(cmd, s.connectError)
		return 2, nil
	}
	if code, handled := s.scripted(call, cmd); handled {
		return code, nil
	}
	for _, h := range psqlHandlers {
		if strings.Contains(call.Stdin, h.marker) {
			return h.run(s, call, cmd)
		}
	}
	if call.has("--single-transaction") {
		return s.applyGlobals(call.Stdin, cmd)
	}
	return -1, fmt.Errorf("fake: a query the model does not know: %q", call.Stdin)
}

// scripted plays the answers and the failures a test put in for a script.
func (s *pgServer) scripted(call pgCall, cmd restic.Command) (code int, handled bool) {
	for marker, text := range s.failOn {
		if strings.Contains(call.Stdin, marker) {
			complain(cmd, text)
			return 3, true
		}
	}
	for marker, text := range s.failOut {
		if strings.Contains(call.Stdin, marker) {
			say(cmd, strings.Split(text[0], "\n")...)
			complain(cmd, text[1])
			return 3, true
		}
	}
	for marker, text := range s.answers {
		if strings.Contains(call.Stdin, marker) {
			say(cmd, strings.Split(text, "\n")...)
			return 0, true
		}
	}
	return 0, false
}

func (s *pgServer) probe(_ pgCall, cmd restic.Command) (int, error) {
	s.event("probe")
	say(cmd, fmt.Sprintf("%d|%s|%s|%s", s.version, yn(s.super), yn(s.createdb), s.user))
	return 0, nil
}

func (s *pgServer) systemIdentifier(_ pgCall, cmd restic.Command) (int, error) {
	s.event("system identifier")
	if s.systemIDError != "" {
		complain(cmd, s.systemIDError)
		return 3, nil
	}
	say(cmd, s.systemID)
	return 0, nil
}

func (s *pgServer) lockCapacity(_ pgCall, cmd restic.Command) (int, error) {
	s.event("lock capacity")
	say(cmd, fmt.Sprint(s.locks*(s.conns+s.prepared)))
	return 0, nil
}

func (s *pgServer) databaseExists(call pgCall, cmd restic.Command) (int, error) {
	s.event("database exists?")
	_, ok := s.databases[call.Vars["dbname"]]
	say(cmd, yn(ok))
	return 0, nil
}

func (s *pgServer) databaseOID(call pgCall, cmd restic.Command) (int, error) {
	s.event("database oid")
	say(cmd, fmt.Sprint(s.databases[call.Vars["dbname"]]))
	return 0, nil
}

// byOID answers "<objects with the name>|<objects with the OID>" for the
// objects of a catalog.
func byOID(objects map[string]uint32, call pgCall, cmd restic.Command) {
	name, oid := call.Vars["name"], call.Vars["oid"]
	same, any := 0, 0
	for n, o := range objects {
		if fmt.Sprint(o) == oid {
			any = 1
			if n == name {
				same = 1
			}
		}
	}
	say(cmd, fmt.Sprintf("%d|%d", same, any))
}

func (s *pgServer) databaseByOID(call pgCall, cmd restic.Command) (int, error) {
	s.event("database by oid")
	byOID(s.databases, call, cmd)
	return 0, nil
}

func (s *pgServer) roleByOID(call pgCall, cmd restic.Command) (int, error) {
	s.event("role by oid")
	byOID(s.roles, call, cmd)
	return 0, nil
}

func (s *pgServer) countTables(_ pgCall, cmd restic.Command) (int, error) {
	s.event("count tables")
	say(cmd, fmt.Sprint(s.tables))
	return 0, nil
}

func (s *pgServer) existingRoles(call pgCall, cmd restic.Command) (int, error) {
	s.event("existing roles")
	say(cmd, "t|"+s.bootstrap)
	for _, n := range strings.Split(call.Vars["names"], "\x01") {
		if _, ok := s.roles[n]; ok && n != s.bootstrap {
			say(cmd, "f|"+n)
		}
	}
	return 0, nil
}

func (s *pgServer) roleOID(call pgCall, cmd restic.Command) (int, error) {
	s.event("role oid %s", call.Vars["name"])
	if s.roleOIDError != "" {
		complain(cmd, s.roleOIDError)
		return 3, nil
	}
	say(cmd, fmt.Sprint(s.roles[call.Vars["name"]]))
	return 0, nil
}

func (s *pgServer) dropRole(call pgCall, cmd restic.Command) (int, error) {
	s.event("drop role %s", call.Vars["name"])
	if msg := s.dropRoleError[call.Vars["name"]]; msg != "" {
		complain(cmd, msg)
		return 3, nil
	}
	delete(s.roles, call.Vars["name"])
	return 0, nil
}

func (s *pgServer) createDatabase(call pgCall, cmd restic.Command) (int, error) {
	name := call.Vars["dbname"]
	s.event("create database")
	if s.createError != "" {
		complain(cmd, s.createError)
		code := 3
		if s.createExit != 0 {
			code = s.createExit
		}
		if s.exitErr {
			return code, errors.New("signal: killed")
		}
		return code, nil
	}
	if _, ok := s.databases[name]; ok {
		complain(cmd, fmt.Sprintf(`ERROR:  database "%s" already exists`, name))
		return 3, nil
	}
	s.nextOID++
	s.databases[name] = s.nextOID
	if s.createHook != nil {
		s.createHook(name, s.nextOID)
	}
	if s.killOnCreate {
		return -1, errors.New("signal: killed")
	}
	return 0, nil
}

func (s *pgServer) dropDatabase(call pgCall, cmd restic.Command) (int, error) {
	s.event("drop database %s", call.Vars["name"])
	if s.dropDBError != "" {
		complain(cmd, s.dropDBError)
		return 3, nil
	}
	delete(s.databases, call.Vars["name"])
	return 0, nil
}

// applyGlobals runs a script of global objects: the roles it creates exist
// when the script ends without an error.
func (s *pgServer) applyGlobals(sql string, cmd restic.Command) (int, error) {
	s.event("globals")
	if s.globalsError != "" {
		complain(cmd, s.globalsError)
		return 3, nil
	}
	for _, line := range strings.Split(sql, "\n") {
		if rest, ok := strings.CutPrefix(line, "CREATE ROLE "); ok {
			s.nextOID++
			s.roles[firstIdentifier(rest)] = s.nextOID
		}
	}
	if s.afterGlobals != nil {
		s.afterGlobals()
	}
	if s.killAfterGlobals {
		return -1, errors.New("signal: killed")
	}
	if s.globalsExit != 0 {
		complain(cmd, "psql: error: the connection was lost")
		if s.exitErr {
			return s.globalsExit, errors.New("signal: killed")
		}
		return s.globalsExit, nil
	}
	return 0, nil
}

// --- the repository

type pgRepo struct {
	mu        sync.Mutex
	snapshots []restic.Snapshot
	content   map[string]string // snapshot id + path → file
	dumpError map[string]error  // snapshot id + path → why restic dump fails after its first chunk
	listError error
	dumps     [][2]string
	// dumpHook, if set, plays restic dump instead of writing the content.
	dumpHook func(ctx context.Context, id, path string, w io.Writer) error
}

func (r *pgRepo) Snapshots(_ context.Context, _ []string) ([]restic.Snapshot, error) {
	return r.snapshots, r.listError
}

func (r *pgRepo) Dump(ctx context.Context, id, path string, w io.Writer) error {
	r.mu.Lock()
	r.dumps = append(r.dumps, [2]string{id, path})
	hook, content, failure := r.dumpHook, r.content[id+path], r.dumpError[id+path]
	r.mu.Unlock()
	if hook != nil {
		return hook(ctx, id, path, w)
	}
	half := len(content) / 2
	if _, err := io.WriteString(w, content[:half]); err != nil {
		return err
	}
	if failure != nil {
		return failure
	}
	if _, err := io.WriteString(w, content[half:]); err != nil {
		return err
	}
	return nil
}

func (r *pgRepo) dumped() [][2]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.dumps)
}

// snapshotP is the snapshot of the database app (pg_dump 16.4, server 16.4).
func snapshotP(extra ...string) restic.Snapshot {
	tags := append([]string{"sard.source=s2", "sard.run=r1", "sard.step=st1", "postgresql.part=database", "postgresql.database=app",
		"postgresql.pg_dump_version=16.4", "postgresql.server_version=16.4", "postgresql.format=custom", "postgresql.system_identifier=9999"}, extra...)
	return restic.Snapshot{ID: pgSnapshotID, Time: time.Date(2026, 10, 11, 3, 0, 0, 0, time.UTC), Paths: []string{"/app.dump"}, Tags: tags}
}

// snapshotG is the snapshot of the global objects of P.
func snapshotG() restic.Snapshot {
	return restic.Snapshot{ID: pgGlobalsID, Time: time.Date(2026, 10, 11, 3, 0, 1, 0, time.UTC), Paths: []string{"/app.globals.sql"},
		Tags: []string{"sard.source=s2", "postgresql.part=globals", "postgresql.database=app", "postgresql.main_snapshot=" + pgSnapshotID}}
}

// --- the host

type pgHost struct {
	t        *testing.T
	base     string
	stateDir string
	faults   *faults
	engine   *consolerestore.Engine
	repo     *pgRepo
	server   *pgServer
	rep      *reporter
	log      *bytes.Buffer
	tools    toolFS
	share    float64
	secrets  map[string][]byte
	// path is the PATH of the agent.
	path string
	// noSecrets: the engine has no way to read a secret.
	noSecrets bool
	// noState: the host keeps no rollback journal (no executor state directory).
	noState bool
	// before are the databases and the roles the server had when the test began.
	before struct{ databases, roles map[string]uint32 }
}

// mark remembers the databases and the roles of the server as they are now.
func (h *pgHost) mark() {
	h.server.mu.Lock()
	defer h.server.mu.Unlock()
	h.before.databases, h.before.roles = maps.Clone(h.server.databases), maps.Clone(h.server.roles)
}

func newPGHost(t *testing.T) *pgHost {
	t.Helper()
	base := t.TempDir()
	h := &pgHost{t: t, base: base, stateDir: filepath.Join(base, "executor"), faults: &faults{}, log: &bytes.Buffer{}, rep: &reporter{},
		server: newPGServer(), share: 0.5, secrets: map[string][]byte{passwordSecret: []byte(secretValue)}}
	if err := os.MkdirAll(h.stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	h.tools = toolFS{nodes: map[string]fs.FileMode{pgBin + "/pg_restore": 0o755, pgBin + "/psql": 0o755}}
	h.repo = &pgRepo{
		snapshots: []restic.Snapshot{snapshotP(), snapshotG()},
		content: map[string]string{
			pgSnapshotID + "/app.dump":       "PGDMP" + strings.Repeat("x", 1000),
			pgGlobalsID + "/app.globals.sql": defaultGlobalsSQL,
		},
		dumpError: map[string]error{},
	}
	h.path = pgBin + ":/usr/bin"
	h.server.journalFile = filepath.Join(h.journal(), "c1.json")
	h.rebuild()
	h.mark()
	return h
}

func (h *pgHost) rebuild() {
	h.engine = consolerestore.New(consolerestore.Options{
		Dir:    filepath.Join(h.base, "restore"),
		FS:     faultFS{hostsetup.OS{}, h.faults},
		Owner:  hostsetup.Attrs{UID: os.Getuid(), GID: os.Getgid(), Mode: 0o700},
		Logger: slog.New(slog.NewTextHandler(h.log, nil)),
		Postgres: consolerestore.PostgresOptions{
			Exec:  h.server,
			Tools: h.tools,
			Environ: func() []string {
				return []string{"PATH=" + h.path, "HOME=/home/sard", "PGPASSWORD=leaked", "PGHOST=evil", "LC_ALL=ru_RU.UTF-8"}
			},
			StateDir:  h.stateDirOrNone(),
			LockShare: h.share,
			Secret:    h.secretReader(),
		},
	})
}

func (h *pgHost) stateDirOrNone() string {
	if h.noState {
		return ""
	}
	return h.stateDir
}

func (h *pgHost) secret(name string) ([]byte, error) {
	if v, ok := h.secrets[name]; ok {
		return v, nil
	}
	return nil, fmt.Errorf("no secret %q", name)
}

func (h *pgHost) secretReader() func(string) ([]byte, error) {
	if h.noSecrets {
		return nil
	}
	return h.secret
}

// rebuildWithoutSecrets is an engine of a host that has no way to read secrets.
func (h *pgHost) rebuildWithoutSecrets() {
	h.noSecrets = true
	h.rebuild()
}

func (h *pgHost) request() consolerestore.DatabaseRequest {
	return consolerestore.DatabaseRequest{
		CommandID: "c1", Repository: "main", SnapshotID: pgSnapshotID, Source: "s2",
		Plan:     sdk.PostgresRestore{Host: "db2", Port: 5432, User: "restorer", PasswordRef: passwordSecret, TLSMode: "disable", Name: pgNewDatabase},
		Password: []byte(secretValue),
	}
}

func (h *pgHost) run(req consolerestore.DatabaseRequest) (consolerestore.DatabaseOutput, *consolerestore.Failure) {
	h.t.Helper()
	return h.runCtx(context.Background(), req)
}

func (h *pgHost) runCtx(ctx context.Context, req consolerestore.DatabaseRequest) (consolerestore.DatabaseOutput, *consolerestore.Failure) {
	h.t.Helper()
	return h.engine.RestoreDatabase(ctx, h.repo, req, h.rep)
}

// withGlobals asks for the global objects and makes the role a superuser.
func (h *pgHost) withGlobals() consolerestore.DatabaseRequest {
	h.server.super = true
	req := h.request()
	req.Plan.Globals = true
	return req
}

func (h *pgHost) journal() string { return filepath.Join(h.stateDir, "pg-restore") }

// journalEntries are the files of the rollback journal.
func (h *pgHost) journalEntries() []string {
	entries, err := os.ReadDir(h.journal())
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// logText is the log of the step.
func (h *pgHost) logText() string {
	var b strings.Builder
	for _, l := range h.rep.logs() {
		b.WriteString(l.text + "\n")
	}
	return b.String()
}

// noTrace: the server has the databases and the roles it had, the journal is gone.
func (h *pgHost) noTrace() {
	h.t.Helper()
	h.server.mu.Lock()
	defer h.server.mu.Unlock()
	if !maps.Equal(h.server.databases, h.before.databases) || !maps.Equal(h.server.roles, h.before.roles) {
		h.t.Errorf("databases %v, roles %v; before: %v, %v", h.server.databases, h.server.roles, h.before.databases, h.before.roles)
	}
	if entries := h.journalEntries(); len(entries) != 0 {
		h.t.Errorf("rollback journal: %v", entries)
	}
}

// firstIdentifier is the identifier a statement starts with: bare, or quoted
// with a quote doubled.
func firstIdentifier(rest string) string {
	if !strings.HasPrefix(rest, `"`) {
		end := strings.IndexAny(rest, "; ")
		if end < 0 {
			end = len(rest)
		}
		return rest[:end]
	}
	var b strings.Builder
	for i := 1; i < len(rest); i++ {
		if rest[i] == '"' && i+1 < len(rest) && rest[i+1] == '"' {
			b.WriteByte('"')
			i++
		} else if rest[i] == '"' {
			break
		} else {
			b.WriteByte(rest[i])
		}
	}
	return b.String()
}

// timeLeft is what the context has left before its deadline, 0 if it has none.
func timeLeft(ctx context.Context) time.Duration {
	if d, ok := ctx.Deadline(); ok {
		return time.Until(d)
	}
	return 0
}
