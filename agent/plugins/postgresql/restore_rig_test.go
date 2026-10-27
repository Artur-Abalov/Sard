// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package postgresql_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/plugins/postgresql"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

// The rig of the restore of a PostgreSQL database (R2b, R2c-2): a repository
// that holds the snapshots P and G, a PostgreSQL server that is a model of the
// parts of a real one the plugin talks to, the tools of the host, and the host
// of the agent as the SDK describes it (RestoreHost, RecoveryHost).

const (
	rpSnapshotID    = "bbbb1111bbbb1111bbbb1111bbbb1111bbbb1111bbbb1111bbbb1111bbbb1111"
	rpGlobalsID     = "cccc1111cccc1111cccc1111cccc1111cccc1111cccc1111cccc1111cccc1111"
	rpNewDatabase   = "app_restored_20261011"
	rpBin           = "/usr/lib/postgresql/16/bin"
	rpSecret        = `r'e:s\t w0rd-Ж`
	rpPasswordRef   = "pg-restore"
	rpGlobalsSQL    = "CREATE ROLE app_rw;\nALTER ROLE app_rw WITH NOSUPERUSER INHERIT;\nCREATE ROLE app_ro;\nGRANT app_ro TO app_rw;\n"
	rpScriptRefused = 3 // psql: the script was refused
)

// errStorage is what the repository fails with when the storage stops answering;
// the host words it as BACKEND_UNAVAILABLE.
var errStorage = errors.New("the storage stops answering")

// --- the tools of the host

type rpTools struct {
	nodes map[string]fs.FileMode
	// errs are the errors Stat answers with instead of looking at nodes.
	errs map[string]error
}

type rpInfo struct {
	name string
	mode fs.FileMode
}

func (i rpInfo) Name() string      { return i.name }
func (rpInfo) Size() int64         { return 0 }
func (i rpInfo) Mode() fs.FileMode { return i.mode }
func (rpInfo) ModTime() time.Time  { return time.Time{} }
func (i rpInfo) IsDir() bool       { return i.mode.IsDir() }
func (rpInfo) Sys() any            { return nil }

func (f rpTools) Stat(name string) (fs.FileInfo, error) {
	if err := f.errs[name]; err != nil {
		return nil, err
	}
	mode, ok := f.nodes[name]
	if !ok {
		return nil, &fs.PathError{Op: "stat", Path: name, Err: syscall.ENOENT}
	}
	return rpInfo{filepath.Base(name), mode}, nil
}

// --- the PostgreSQL server

// rpCall is one process the plugin started.
type rpCall struct {
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

func (c rpCall) has(arg string) bool { return slices.Contains(c.Args, arg) }

func (c rpCall) env(name string) (string, bool) {
	for _, kv := range c.Env {
		if v, ok := strings.CutPrefix(kv, name+"="); ok {
			return v, true
		}
	}
	return "", false
}

// conninfo is the --dbname= of the call.
func (c rpCall) conninfo() string {
	for _, a := range c.Args {
		if v, ok := strings.CutPrefix(a, "--dbname="); ok {
			return v
		}
	}
	return ""
}

// rpServer is a PostgreSQL server as far as the plugin sees it: it answers the
// queries of the plugin, creates and drops databases and roles, and fails where
// a test says it does. It is the Runner of the plugin.
type rpServer struct {
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
	restoreHook      func(ctx context.Context, c rpCall) (int, error)
	createHook       func(name string, oid uint32) // right after CREATE DATABASE
	afterGlobals     func()
	killOnCreate     bool // CREATE DATABASE succeeds, the psql dies before it prints the OID
	killAfterGlobals bool // the script commits, the psql dies before it ends
	globalsExit      int  // the psql ends with this code after the script committed (not 3, not killed)
	createExit       int  // CREATE DATABASE fails with this exit code instead of 3
	exitErr          bool // globalsExit and createExit come with an error of the process, as after a signal

	calls  []rpCall
	events []string
	// answers replace the output of the script that contains the key, failOn
	// make it fail with the text; hooks run when the event begins.
	answers map[string]string
	failOn  map[string]string
	// failOut is failOn that prints lines to stdout as well.
	failOut map[string][2]string
	hooks   map[string]func()
	// journal is the entry of the step as the host holds it; journalAt is what
	// it held when each event began.
	journal   func() string
	journalAt map[string]string
}

func newRPServer() *rpServer {
	return &rpServer{
		version: 160004, createdb: true, user: "restorer", systemID: "7412345678901234567",
		locks: 64, conns: 100, bootstrap: "postgres", tables: 2, toc: 10, toolVersion: "16.4",
		databases: map[string]uint32{"postgres": 5, "template0": 4, "template1": 1, "app": 16384},
		roles:     map[string]uint32{"postgres": 10, "restorer": 16390}, nextOID: 20000,
		dropRoleError: map[string]string{}, answers: map[string]string{}, failOn: map[string]string{}, hooks: map[string]func(){},
		failOut: map[string][2]string{},
	}
}

func (s *rpServer) event(format string, args ...any) {
	name := fmt.Sprintf(format, args...)
	s.events = append(s.events, name)
	if hook := s.hooks[name]; hook != nil {
		hook()
	}
	if s.journal != nil {
		if s.journalAt == nil {
			s.journalAt = map[string]string{}
		}
		s.journalAt[name] = s.journal()
	}
}

// ran returns the calls of a tool.
func (s *rpServer) ran(tool string) []rpCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []rpCall
	for _, c := range s.calls {
		if c.Tool == tool {
			out = append(out, c)
		}
	}
	return out
}

func (s *rpServer) eventLog() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.events)
}

func (s *rpServer) hasDatabase(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.databases[name]
	return ok
}

func (s *rpServer) hasRole(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.roles[name]
	return ok
}

// rpSay writes the lines to the standard output of the process.
func rpSay(cmd postgresql.Cmd, lines ...string) {
	for _, l := range lines {
		if cmd.Stdout != nil {
			_, _ = io.WriteString(cmd.Stdout, l+"\n")
		}
	}
}

func rpComplain(cmd postgresql.Cmd, lines ...string) {
	for _, l := range lines {
		if cmd.Stderr != nil {
			cmd.Stderr(l)
		}
	}
}

func yn(b bool) string {
	if b {
		return "t"
	}
	return "f"
}

// Run implements postgresql.Runner for the tools of PostgreSQL.
func (s *rpServer) Run(ctx context.Context, cmd postgresql.Cmd) (int, error) {
	if err := ctx.Err(); err != nil {
		return -1, err // a process is not started for a context that has ended
	}
	call := rpCall{Tool: filepath.Base(cmd.Path), Path: cmd.Path, Args: slices.Clone(cmd.Args), Env: slices.Clone(cmd.Env), Vars: map[string]string{}, Deadline: timeLeft(ctx)}
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

func (s *rpServer) pgRestore(ctx context.Context, call rpCall, cmd postgresql.Cmd) (int, error) {
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
	rpComplain(cmd, stderr...)
	if hook != nil {
		return hook(ctx, call)
	}
	data, _ := io.ReadAll(cmd.Stdin)
	s.mu.Lock()
	s.calls[len(s.calls)-1].Stdin = string(data)
	s.mu.Unlock()
	if restoreError != "" {
		rpComplain(cmd, restoreError)
		return 1, nil
	}
	return 0, nil
}

// pgVersion answers pg_restore --version.
func (s *rpServer) pgVersion(call rpCall, cmd postgresql.Cmd) (int, error) {
	s.mu.Lock()
	s.calls = append(s.calls, call)
	hook, out, fails := s.versionHook, s.versionOutput, s.versionFails
	s.mu.Unlock()
	if hook != nil {
		hook()
	}
	if fails {
		if out != "" {
			rpSay(cmd, out)
		}
		rpComplain(cmd, "pg_restore: error: cannot run")
		return 127, nil
	}
	if out == "" {
		out = "pg_restore (PostgreSQL) " + s.toolVersion
	}
	rpSay(cmd, out)
	return 0, nil
}

// contents answers pg_restore -l: it reads the head of the archive only.
func (s *rpServer) contents(call rpCall, cmd postgresql.Cmd) (int, error) {
	head := make([]byte, 5)
	n, _ := io.ReadFull(cmd.Stdin, head)
	call.Stdin = string(head[:n])
	s.mu.Lock()
	s.calls = append(s.calls, call)
	s.event("pg_restore -l")
	tocError, toc := s.tocError, s.toc
	s.mu.Unlock()
	if tocError != "" {
		rpComplain(cmd, tocError)
		return 1, nil
	}
	rpSay(cmd, ";", "; Archive created at 2026-10-11 03:00:00 UTC", ";     dbname: app")
	if s.tocBlank {
		rpSay(cmd, "", "   ")
	}
	for i := range toc {
		rpSay(cmd, fmt.Sprintf("%d; 1259 %d TABLE public t%d app", i+200, i+16500, i))
	}
	return 0, nil
}

// rpHandler answers the script of psql that contains marker.
type rpHandler struct {
	marker string
	run    func(s *rpServer, call rpCall, cmd postgresql.Cmd) (int, error)
}

// rpHandlers are tried in order.
var rpHandlers = []rpHandler{
	{"server_version_num", (*rpServer).probe},
	{"pg_control_system", (*rpServer).systemIdentifier},
	{"max_locks_per_transaction", (*rpServer).lockCapacity},
	{"exists(select 1 from pg_database", (*rpServer).databaseExists},
	{"CREATE DATABASE", (*rpServer).createDatabase},
	{"select oid from pg_database", (*rpServer).databaseOID},
	{"from pg_database where oid", (*rpServer).databaseByOID},
	{"DROP DATABASE", (*rpServer).dropDatabase},
	{"from pg_class", (*rpServer).countTables},
	{"oid = 10", (*rpServer).existingRoles},
	{"select oid from pg_roles", (*rpServer).roleOID},
	{"from pg_roles where oid", (*rpServer).roleByOID},
	{"DROP ROLE", (*rpServer).dropRole},
}

// psql plays the server for one script of the plugin.
func (s *rpServer) psql(call rpCall, cmd postgresql.Cmd) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.connectError != "" {
		rpComplain(cmd, s.connectError)
		return 2, nil
	}
	if code, handled := s.scripted(call, cmd); handled {
		return code, nil
	}
	for _, h := range rpHandlers {
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
func (s *rpServer) scripted(call rpCall, cmd postgresql.Cmd) (code int, handled bool) {
	for marker, text := range s.failOn {
		if strings.Contains(call.Stdin, marker) {
			rpComplain(cmd, text)
			return rpScriptRefused, true
		}
	}
	for marker, text := range s.failOut {
		if strings.Contains(call.Stdin, marker) {
			rpSay(cmd, strings.Split(text[0], "\n")...)
			rpComplain(cmd, text[1])
			return rpScriptRefused, true
		}
	}
	for marker, text := range s.answers {
		if strings.Contains(call.Stdin, marker) {
			rpSay(cmd, strings.Split(text, "\n")...)
			return 0, true
		}
	}
	return 0, false
}

func (s *rpServer) probe(_ rpCall, cmd postgresql.Cmd) (int, error) {
	s.event("probe")
	rpSay(cmd, fmt.Sprintf("%d|%s|%s|%s", s.version, yn(s.super), yn(s.createdb), s.user))
	return 0, nil
}

func (s *rpServer) systemIdentifier(_ rpCall, cmd postgresql.Cmd) (int, error) {
	s.event("system identifier")
	if s.systemIDError != "" {
		rpComplain(cmd, s.systemIDError)
		return rpScriptRefused, nil
	}
	rpSay(cmd, s.systemID)
	return 0, nil
}

func (s *rpServer) lockCapacity(_ rpCall, cmd postgresql.Cmd) (int, error) {
	s.event("lock capacity")
	rpSay(cmd, fmt.Sprint(s.locks*(s.conns+s.prepared)))
	return 0, nil
}

func (s *rpServer) databaseExists(call rpCall, cmd postgresql.Cmd) (int, error) {
	s.event("database exists?")
	_, ok := s.databases[call.Vars["dbname"]]
	rpSay(cmd, yn(ok))
	return 0, nil
}

func (s *rpServer) databaseOID(call rpCall, cmd postgresql.Cmd) (int, error) {
	s.event("database oid")
	rpSay(cmd, fmt.Sprint(s.databases[call.Vars["dbname"]]))
	return 0, nil
}

// byOID answers "<objects with the name>|<objects with the OID>" for the
// objects of a catalog.
func byOID(objects map[string]uint32, call rpCall, cmd postgresql.Cmd) {
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
	rpSay(cmd, fmt.Sprintf("%d|%d", same, any))
}

func (s *rpServer) databaseByOID(call rpCall, cmd postgresql.Cmd) (int, error) {
	s.event("database by oid")
	byOID(s.databases, call, cmd)
	return 0, nil
}

func (s *rpServer) roleByOID(call rpCall, cmd postgresql.Cmd) (int, error) {
	s.event("role by oid")
	byOID(s.roles, call, cmd)
	return 0, nil
}

func (s *rpServer) countTables(_ rpCall, cmd postgresql.Cmd) (int, error) {
	s.event("count tables")
	rpSay(cmd, fmt.Sprint(s.tables))
	return 0, nil
}

func (s *rpServer) existingRoles(call rpCall, cmd postgresql.Cmd) (int, error) {
	s.event("existing roles")
	rpSay(cmd, "t|"+s.bootstrap)
	for _, n := range strings.Split(call.Vars["names"], "\x01") {
		if _, ok := s.roles[n]; ok && n != s.bootstrap {
			rpSay(cmd, "f|"+n)
		}
	}
	return 0, nil
}

func (s *rpServer) roleOID(call rpCall, cmd postgresql.Cmd) (int, error) {
	s.event("role oid %s", call.Vars["name"])
	if s.roleOIDError != "" {
		rpComplain(cmd, s.roleOIDError)
		return rpScriptRefused, nil
	}
	rpSay(cmd, fmt.Sprint(s.roles[call.Vars["name"]]))
	return 0, nil
}

func (s *rpServer) dropRole(call rpCall, cmd postgresql.Cmd) (int, error) {
	s.event("drop role %s", call.Vars["name"])
	if msg := s.dropRoleError[call.Vars["name"]]; msg != "" {
		rpComplain(cmd, msg)
		return rpScriptRefused, nil
	}
	delete(s.roles, call.Vars["name"])
	return 0, nil
}

func (s *rpServer) createDatabase(call rpCall, cmd postgresql.Cmd) (int, error) {
	name := call.Vars["dbname"]
	s.event("create database")
	if s.createError != "" {
		rpComplain(cmd, s.createError)
		code := rpScriptRefused
		if s.createExit != 0 {
			code = s.createExit
		}
		if s.exitErr {
			return code, errors.New("signal: killed")
		}
		return code, nil
	}
	if _, ok := s.databases[name]; ok {
		rpComplain(cmd, fmt.Sprintf(`ERROR:  database "%s" already exists`, name))
		return rpScriptRefused, nil
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

func (s *rpServer) dropDatabase(call rpCall, cmd postgresql.Cmd) (int, error) {
	s.event("drop database %s", call.Vars["name"])
	if s.dropDBError != "" {
		rpComplain(cmd, s.dropDBError)
		return rpScriptRefused, nil
	}
	delete(s.databases, call.Vars["name"])
	return 0, nil
}

// applyGlobals runs a script of global objects: the roles it creates exist
// when the script ends without an error.
func (s *rpServer) applyGlobals(sql string, cmd postgresql.Cmd) (int, error) {
	s.event("globals")
	if s.globalsError != "" {
		rpComplain(cmd, s.globalsError)
		return rpScriptRefused, nil
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
		rpComplain(cmd, "psql: error: the connection was lost")
		if s.exitErr {
			return s.globalsExit, errors.New("signal: killed")
		}
		return s.globalsExit, nil
	}
	return 0, nil
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

// --- the repository

type rpRepo struct {
	mu        sync.Mutex
	snapshots []sdk.SnapshotInfo
	content   map[string]string // snapshot id + path → file
	dumpError map[string]error  // snapshot id + path → why restic dump fails after its first chunk
	listError error
	dumps     [][2]string
	// dumpHook, if set, plays restic dump instead of writing the content.
	dumpHook func(ctx context.Context, id, path string, w io.Writer) error
}

func (r *rpRepo) list() ([]sdk.SnapshotInfo, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.snapshots, r.listError
}

func (r *rpRepo) dump(ctx context.Context, id, path string, w io.Writer) error {
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
	_, err := io.WriteString(w, content[half:])
	return err
}

func (r *rpRepo) dumped() [][2]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.dumps)
}

// snapshotP is the snapshot of the database app (pg_dump 16.4, server 16.4).
func snapshotP(extra ...string) sdk.SnapshotInfo {
	tags := append([]string{"sard.source=s2", "sard.run=r1", "sard.step=st1", "postgresql.part=database", "postgresql.database=app",
		"postgresql.pg_dump_version=16.4", "postgresql.server_version=16.4", "postgresql.format=custom", "postgresql.system_identifier=9999"}, extra...)
	return sdk.SnapshotInfo{ID: rpSnapshotID, Time: time.Date(2026, 10, 11, 3, 0, 0, 0, time.UTC), Paths: []string{"/app.dump"}, Tags: tags}
}

// snapshotG is the snapshot of the global objects of P.
func snapshotG() sdk.SnapshotInfo {
	return sdk.SnapshotInfo{ID: rpGlobalsID, Time: time.Date(2026, 10, 11, 3, 0, 1, 0, time.UTC), Paths: []string{"/app.globals.sql"},
		Tags: []string{"sard.source=s2", "postgresql.part=globals", "postgresql.database=app", "postgresql.main_snapshot=" + rpSnapshotID}}
}

// --- the host of the agent

// rpEvent is what the host heard of the step: a phase, a progress or a log line.
type rpEvent struct {
	isLog      bool
	phase      sdk.Phase
	bytes      uint64
	bytesTotal uint64
	level      sdk.Level
	text       string
}

// rpJournal is the journal of the command: the host would write it durably.
type rpJournal struct {
	mu    sync.Mutex
	saves []string
	// failAt makes the save with that index (counted from 0) fail.
	failAt map[int]error
	// failFrom, if set, makes every save from that index fail.
	failFrom int
}

func (j *rpJournal) Save(entry []byte) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	n := len(j.saves)
	if err := j.failAt[n]; err != nil {
		return err
	}
	if j.failFrom > 0 && n >= j.failFrom {
		return syscall.EIO
	}
	j.saves = append(j.saves, string(entry))
	return nil
}

// current is the entry as the host holds it, "" before the first save.
func (j *rpJournal) current() string {
	j.mu.Lock()
	defer j.mu.Unlock()
	if len(j.saves) == 0 {
		return ""
	}
	return j.saves[len(j.saves)-1]
}

func (j *rpJournal) count() int {
	j.mu.Lock()
	defer j.mu.Unlock()
	return len(j.saves)
}

// rpHost is the RestoreHost (and the RecoveryHost) of the tests.
type rpHost struct {
	mu      sync.Mutex
	secrets map[string][]byte
	// grant is what the binding of every secret allows; refused, if set, is
	// what SecretFor fails with; asked are the targets it was asked for.
	grant   sdk.Grant
	refused error
	// refuseAt, if set, is the one database the binding does not allow.
	refuseAt string
	asked    []sdk.Target
	repo     *rpRepo
	journal  *rpJournal
	events   []rpEvent
	phase    sdk.Phase
	// step is the context of the step: a failure of the repository while it
	// has ended is an interruption, not the failure of the storage.
	step context.Context
	// rawListError, if set, is what Snapshots fails with, not worded by the host.
	rawListError error
}

var _ sdk.RestoreHost = (*rpHost)(nil)

func (h *rpHost) SecretFor(name string, target sdk.Target) (sdk.Secret, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.asked = append(h.asked, target)
	v, ok := h.secrets[name]
	switch {
	case !ok:
		return sdk.Secret{}, &sdk.SecretError{Name: name}
	case h.refused != nil:
		return sdk.Secret{}, h.refused
	case target.Database == h.refuseAt:
		return sdk.Secret{}, &sdk.BindingError{Name: name, Plugin: "postgresql", Target: target}
	}
	return sdk.Secret{Value: slices.Clone(v), Grant: h.grant}, nil
}

func (h *rpHost) Progress(done, total uint64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.events = append(h.events, rpEvent{phase: h.phase, bytes: done, bytesTotal: total})
}

func (h *rpHost) Log(level sdk.Level, text string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.events = append(h.events, rpEvent{isLog: true, level: level, text: text})
}

func (h *rpHost) Phase(p sdk.Phase) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.phase = p
	h.events = append(h.events, rpEvent{phase: p})
}

func (h *rpHost) Journal() sdk.Journal { return h.journal }

func (h *rpHost) Snapshots(context.Context) ([]sdk.SnapshotInfo, error) {
	if h.rawListError != nil {
		return nil, h.rawListError
	}
	list, err := h.repo.list()
	if err != nil {
		return nil, h.worded(err)
	}
	return list, nil
}

// worded is what the host makes of a failure of restic: an interruption if the
// step ended, the storage's reason, or restic's own.
func (h *rpHost) worded(err error) error {
	switch {
	case h.step != nil && h.step.Err() != nil:
		return &sdk.RestoreError{Reason: sdk.ReasonInterrupted, Detail: "the command was interrupted", Err: err}
	case errors.Is(err, errStorage):
		return &sdk.RestoreError{Reason: sdk.ReasonBackendUnavailable, Detail: "backend s3: " + err.Error() + "; the command can be repeated", Err: err}
	}
	return &sdk.RestoreError{Reason: sdk.ReasonResticFailed, Detail: err.Error(), Err: err}
}

// rpWatched remembers the failure of the receiver and stops restic on it.
type rpWatched struct {
	w         io.Writer
	onFailure func()
	mu        sync.Mutex
	err       error
}

func (f *rpWatched) Write(p []byte) (int, error) {
	n, err := f.w.Write(p)
	if err != nil {
		f.mu.Lock()
		f.err = errors.Join(f.err, err)
		f.mu.Unlock()
		f.onFailure()
	}
	return n, err
}

func (f *rpWatched) failure() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.err
}

// Dump is restic dump as the host runs it: a receiver that fails stops it and
// gets its own error back, the context of the plugin stops it quietly.
// RestoreTree: a database is never restored as a tree.
func (h *rpHost) RestoreTree(context.Context, string) (string, error) {
	return "", errors.New("rpHost: no tree")
}

func (h *rpHost) Dump(ctx context.Context, id, path string, w io.Writer) error {
	dumpCtx, stop := context.WithCancel(ctx)
	defer stop()
	watched := &rpWatched{w: w, onFailure: stop}
	err := h.repo.dump(dumpCtx, id, path, watched)
	switch {
	case err == nil:
		return nil
	case watched.failure() != nil:
		return fmt.Errorf("the receiver of the snapshot stream failed: %w", watched.failure())
	case ctx.Err() != nil && (h.step == nil || h.step.Err() == nil):
		return fmt.Errorf("the snapshot stream was stopped: %w", context.Cause(ctx))
	}
	return h.worded(err)
}

// logs are the log lines of the step.
func (h *rpHost) logs() []rpEvent {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []rpEvent
	for _, e := range h.events {
		if e.isLog {
			out = append(out, e)
		}
	}
	return out
}

// progress are the events of the step without its log lines.
func (h *rpHost) progress() []rpEvent {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []rpEvent
	for _, e := range h.events {
		if !e.isLog {
			out = append(out, e)
		}
	}
	return out
}

// --- the failure of a restore, as the agent words it

// rpFailure is the *sdk.RestoreError a restore returned; Error is the message
// of the step, worded as the agent words it (ADR 0066): the host writes
// "<REASON> (<stage>): <detail>; nothing was restored", or CLEANUP_FAILED
// with what is left.
type rpFailure struct{ *sdk.RestoreError }

func (f *rpFailure) Error() string {
	cause := f.Reason + " (" + f.Stage + "): " + f.Detail
	if len(f.Leftover) == 0 {
		return cause + "; nothing was restored"
	}
	return "CLEANUP_FAILED (rollback): " + cause + "; left behind: " + strings.Join(f.Leftover, ", ") + " (" + f.Cleanup + "); remove it by hand"
}

func hasPrefix(t *testing.T, f *rpFailure, want string) {
	t.Helper()
	if f == nil {
		t.Fatalf("the restore succeeded, want a failure that starts with %q", want)
	}
	if !strings.HasPrefix(f.Error(), want) {
		t.Fatalf("failure %q does not start with %q", f.Error(), want)
	}
}

// --- the plan of a step and the host

// rpPlan is the restore config of a step.
type rpPlan struct {
	Host        string
	Port        int
	User        string
	PasswordRef string
	TLSMode     string
	TLSRootCert string
	Name        string
	Globals     bool
	ClientPath  string
}

// rpRequest is a restore of a snapshot of a database into a new one.
type rpRequest struct {
	CommandID  string
	Repository string
	SnapshotID string
	Source     string
	Plan       rpPlan
}

// config is the restore config the plan stands for.
func (p rpPlan) config() sdk.Config {
	cfg := map[string]any{"host": p.Host, "port": p.Port, "database": p.Name, "user": p.User, "password_ref": p.PasswordRef}
	for k, v := range map[string]string{"tls_mode": p.TLSMode, "tls_root_cert": p.TLSRootCert, "pg_restore_path": p.ClientPath} {
		if v != "" {
			cfg[k] = v
		}
	}
	if p.Globals {
		cfg["restore_globals"] = true
	}
	b, _ := json.Marshal(cfg)
	return b
}

type rpHostRig struct {
	t      *testing.T
	repo   *rpRepo
	server *rpServer
	host   *rpHost
	tools  rpTools
	share  float64
	// path is the PATH of the agent.
	path string
	// started counts the programs the plugin asked the runner to start, those
	// that were refused for an ended context too.
	started atomic.Int64
	// last is the failure of the last restore, nil if it succeeded.
	last *rpFailure
	// before are the databases and the roles the server had when the test began.
	before struct{ databases, roles map[string]uint32 }
}

// mark remembers the databases and the roles of the server as they are now.
func (h *rpHostRig) mark() {
	h.server.mu.Lock()
	defer h.server.mu.Unlock()
	h.before.databases, h.before.roles = maps.Clone(h.server.databases), maps.Clone(h.server.roles)
}

func newPGHost(t *testing.T) *rpHostRig {
	t.Helper()
	repo := &rpRepo{
		snapshots: []sdk.SnapshotInfo{snapshotP(), snapshotG()},
		content: map[string]string{
			rpSnapshotID + "/app.dump":       "PGDMP" + strings.Repeat("x", 1000),
			rpGlobalsID + "/app.globals.sql": rpGlobalsSQL,
		},
		dumpError: map[string]error{},
	}
	jr := &rpJournal{}
	h := &rpHostRig{t: t, repo: repo, server: newRPServer(), share: 0.5,
		host:  &rpHost{secrets: map[string][]byte{rpPasswordRef: []byte(rpSecret)}, grant: sdk.Grant{TLS: "disable"}, repo: repo, journal: jr},
		tools: rpTools{nodes: map[string]fs.FileMode{rpBin + "/pg_restore": 0o755, rpBin + "/psql": 0o755}},
		path:  rpBin + ":/usr/bin",
	}
	h.server.journal = jr.current
	h.mark()
	return h
}

// plugin is the plugin as the agent builds it for this host.
func (h *rpHostRig) plugin() postgresql.Plugin {
	share := h.share
	return postgresql.Plugin{
		AgentVersion: "1.2.3", Runner: h, FS: h.tools, LockShare: &share,
		Environ: func() []string {
			return []string{"PATH=" + h.path, "HOME=/home/sard", "PGPASSWORD=leaked", "PGHOST=evil", "LC_ALL=ru_RU.UTF-8"}
		},
	}
}

// Run is the runner of the plugin: the server, with the starts counted.
func (h *rpHostRig) Run(ctx context.Context, cmd postgresql.Cmd) (int, error) {
	h.started.Add(1)
	return h.server.Run(ctx, cmd)
}

func (h *rpHostRig) request() rpRequest {
	return rpRequest{
		CommandID: "c1", Repository: "main", SnapshotID: rpSnapshotID, Source: "s2",
		Plan: rpPlan{Host: "db2", Port: 5432, User: "restorer", PasswordRef: rpPasswordRef, TLSMode: "disable", Name: rpNewDatabase},
	}
}

func (h *rpHostRig) run(req rpRequest) (sdk.RestoreResult, *rpFailure) {
	h.t.Helper()
	return h.runCtx(context.Background(), req)
}

// runCtx runs the restore the way the host does and judges what the plugin
// returned: an error is a *sdk.RestoreError with a reason and a stage that
// fit the patterns.
func (h *rpHostRig) runCtx(ctx context.Context, req rpRequest) (sdk.RestoreResult, *rpFailure) {
	h.t.Helper()
	h.host.step = ctx
	res, err := h.plugin().Restore(ctx, h.host, req.Plan.config(), sdk.RestoreRequest{
		CommandID: req.CommandID, SnapshotID: req.SnapshotID, Repository: req.Repository, Source: req.Source,
	})
	h.last = nil
	if err == nil {
		return res, nil
	}
	var re *sdk.RestoreError
	if !errors.As(err, &re) {
		h.t.Fatalf("the plugin returned %v, not a *sdk.RestoreError", err)
	}
	if !sdk.ValidReason(re.Reason) || !sdk.ValidStage(re.Stage) {
		h.t.Errorf("reason %q or stage %q does not fit the patterns", re.Reason, re.Stage)
	}
	h.last = &rpFailure{re}
	return res, h.last
}

// withGlobals asks for the global objects and makes the role a superuser.
func (h *rpHostRig) withGlobals() rpRequest {
	h.server.super = true
	req := h.request()
	req.Plan.Globals = true
	return req
}

// logText is the log of the step.
func (h *rpHostRig) logText() string {
	var b strings.Builder
	for _, l := range h.host.logs() {
		b.WriteString(l.text + "\n")
	}
	return b.String()
}

// noTrace: the server has the databases and the roles it had, and the entry of
// the journal will go with the result: nothing in it asks to be kept.
func (h *rpHostRig) noTrace() {
	h.t.Helper()
	h.server.mu.Lock()
	defer h.server.mu.Unlock()
	if !maps.Equal(h.server.databases, h.before.databases) || !maps.Equal(h.server.roles, h.before.roles) {
		h.t.Errorf("databases %v, roles %v; before: %v, %v", h.server.databases, h.server.roles, h.before.databases, h.before.roles)
	}
	if h.last != nil && h.last.Retain {
		h.t.Errorf("the failure asks to keep the journal entry: %s", h.host.journal.current())
	}
}

// untouched: the step changed nothing on the server and left nothing.
func (h *rpHostRig) untouched(t *testing.T) {
	t.Helper()
	for _, e := range h.server.eventLog() {
		if e == "create database" || e == "globals" || e == "pg_restore" {
			t.Errorf("the step changed the server: %s", e)
		}
	}
	h.noTrace()
}

// scriptsWith are the psql calls whose script contains text.
func (h *rpHostRig) scriptsWith(text string) []rpCall {
	var out []rpCall
	for _, c := range h.server.ran("psql") {
		if strings.Contains(c.Stdin, text) {
			out = append(out, c)
		}
	}
	return out
}

// lastRestore is the pg_restore that restored the data.
func (h *rpHostRig) lastRestore() rpCall {
	restores := h.server.ran("pg_restore")
	return restores[len(restores)-1]
}

// lines are the log lines of the level.
func (h *rpHostRig) lines(level sdk.Level) []string {
	var out []string
	for _, l := range h.host.logs() {
		if l.level == level {
			out = append(out, l.text)
		}
	}
	return out
}

// before fails the test unless the first event happened before the second.
func before(t *testing.T, events []string, first, second string) {
	t.Helper()
	if i, j := slices.Index(events, first), slices.Index(events, second); i < 0 || j < 0 || i > j {
		t.Errorf("%q is not before %q in %v", first, second, events)
	}
}

// dumpsOf is how many times restic dump was asked for path.
func (h *rpHostRig) dumpsOf(path string) int {
	n := 0
	for _, d := range h.repo.dumped() {
		if d[1] == path {
			n++
		}
	}
	return n
}
