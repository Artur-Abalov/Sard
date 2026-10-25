// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package postgresql

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

var _ sdk.Fingerprinter = Plugin{}

// Names of the facts of a fingerprint of a database (V2).
const (
	factMajor      = "major_version"
	factVersion    = "server_version"
	factTables     = "tables"
	factSize       = "database_size"
	factKeyTables  = "key_tables"
	extensionFact  = "extension:"
	extVersionFact = "extension_version:"
	rowsFact       = "rows:"

	present       = "installed"
	missing       = "missing"
	largestTables = 10
)

// Limits of the counts when the host sets none: a minute per table, ten for
// all of them (not measured on a 15 GB source yet: V0 has no report).
const (
	defaultCountTimeout = time.Minute
	defaultCountBudget  = 10 * time.Minute
)

func (p Plugin) countTimeout() time.Duration {
	if p.CountTimeout == 0 {
		return defaultCountTimeout
	}
	return p.CountTimeout
}

func (p Plugin) countBudget() time.Duration {
	if p.CountBudget == 0 {
		return defaultCountBudget
	}
	return p.CountBudget
}

func (p Plugin) now() time.Time {
	if p.Now == nil {
		return time.Now()
	}
	return p.Now()
}

// Fingerprint implements sdk.Fingerprinter: the version of the server, its
// extensions, the number of tables pg_dump dumps, the size of the database and
// the exact number of rows of the key tables, all in one REPEATABLE READ
// transaction. Of a copy (t.Reference set) the rows of the tables the
// reference counted are counted. A backup takes it in the transaction whose
// snapshot pg_dump dumps (Dump), not through this method.
func (p Plugin) Fingerprint(ctx context.Context, h sdk.Host, cfg sdk.Config, t sdk.FingerprintTarget) (sdk.Fingerprint, error) {
	c, err := parse(cfg)
	if err != nil {
		return sdk.Fingerprint{}, err
	}
	password, tl, err := p.setup(h, c)
	if err != nil {
		return sdk.Fingerprint{}, err
	}
	s, err := p.openSession(ctx, h, tl.psql, c.conninfo(), password)
	if err != nil {
		return sdk.Fingerprint{}, err
	}
	defer s.close()
	return p.measure(ctx, h, s, c, t.Reference)
}

// table is a table pg_dump dumps, with its size on disk.
type table struct {
	Schema string `json:"s"`
	Name   string `json:"t"`
	Bytes  int64  `json:"b"`
}

func (t table) key() string { return t.Schema + "." + t.Name }

// ident is the table as a quoted SQL identifier.
func (t table) ident() string { return quoteIdent(t.Schema) + "." + quoteIdent(t.Name) }

const versionQuery = `SELECT 'R' || current_setting('server_version_num') || ' ' || current_setting('server_version');`

const extensionsQuery = `SELECT 'R' || coalesce(json_agg(json_build_object('n', extname, 'v', extversion) ORDER BY extname)::text, '[]') FROM pg_catalog.pg_extension;`

const sizeQuery = `SELECT 'R' || pg_catalog.pg_database_size(pg_catalog.current_database());`

// tablesQuery lists the tables pg_dump dumps: of the user, not temporary, not
// members of an extension, not excluded (%s).
const tablesQuery = `SELECT 'R' || coalesce(json_agg(json_build_object('s', n.nspname, 't', c.relname, 'b', pg_catalog.pg_total_relation_size(c.oid)) ORDER BY n.nspname, c.relname)::text, '[]') ` +
	`FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace ` +
	`WHERE c.relkind IN ('r', 'p') AND c.relpersistence <> 't' AND n.nspname <> 'information_schema' AND n.nspname !~ '^pg_' ` +
	`AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_depend d WHERE d.classid = 'pg_catalog.pg_class'::pg_catalog.regclass AND d.objid = c.oid AND d.deptype = 'e') ` +
	`AND NOT %s;`

// measure takes the fingerprint in the transaction of the session.
func (p Plugin) measure(ctx context.Context, log logger, s *session, c config, ref *sdk.Fingerprint) (sdk.Fingerprint, error) {
	facts, err := serverFacts(ctx, s)
	if err != nil {
		return sdk.Fingerprint{}, err
	}
	tables, err := listTables(ctx, s, c)
	if err != nil {
		return sdk.Fingerprint{}, err
	}
	facts = append(facts, sdk.Fact{Name: factTables, Rule: sdk.RuleExact, Value: strconv.Itoa(len(tables))})
	keys, how := keyTables(c, tables, ref)
	if ref == nil {
		facts = append(facts, sdk.Fact{Name: factKeyTables, Rule: sdk.RuleInfo, Value: how})
	}
	rows, err := p.countRows(ctx, log, s, keys, tables)
	if err != nil {
		return sdk.Fingerprint{}, err
	}
	return sdk.Fingerprint{Plugin: "postgresql", Format: sdk.FingerprintFormat, Facts: append(facts, rows...)}, nil
}

// ask is a query whose row the fingerprint needs: no row is a failure.
func ask(ctx context.Context, s *session, what, sql string) (string, error) {
	got, ok, err := s.query(ctx, sql)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("fingerprint: the query of the %s failed (see the log of the step)", what)
	}
	return got, nil
}

// serverFacts are the version, the extensions and the size of the database.
func serverFacts(ctx context.Context, s *session) ([]sdk.Fact, error) {
	v, err := ask(ctx, s, "version", versionQuery)
	if err != nil {
		return nil, err
	}
	num, text, _ := strings.Cut(v, " ")
	n, err := strconv.Atoi(num)
	if err != nil {
		return nil, fmt.Errorf("fingerprint: unexpected version %q", v)
	}
	facts := []sdk.Fact{
		{Name: factMajor, Rule: sdk.RuleExact, Value: strconv.Itoa(serverVersion(n).major)},
		{Name: factVersion, Rule: sdk.RuleInfo, Value: text},
	}
	ext, err := extensionFacts(ctx, s)
	if err != nil {
		return nil, err
	}
	size, err := ask(ctx, s, "size of the database", sizeQuery)
	if err != nil {
		return nil, err
	}
	return append(append(facts, ext...), sdk.Fact{Name: factSize, Rule: sdk.RuleInfo, Value: size}), nil
}

func extensionFacts(ctx context.Context, s *session) ([]sdk.Fact, error) {
	got, err := ask(ctx, s, "extensions", extensionsQuery)
	if err != nil {
		return nil, err
	}
	var list []struct {
		Name    string `json:"n"`
		Version string `json:"v"`
	}
	if err := json.Unmarshal([]byte(got), &list); err != nil {
		return nil, fmt.Errorf("fingerprint: unexpected list of extensions: %w", err)
	}
	var facts []sdk.Fact
	for _, e := range list {
		facts = append(facts, sdk.Fact{Name: extensionFact + e.Name, Rule: sdk.RuleExact, Value: present},
			sdk.Fact{Name: extVersionFact + e.Name, Rule: sdk.RuleInfo, Value: e.Version})
	}
	return facts, nil
}

func listTables(ctx context.Context, s *session, c config) ([]table, error) {
	excluded, err := excludedCondition(c)
	if err != nil {
		return nil, err
	}
	got, err := ask(ctx, s, "tables", fmt.Sprintf(tablesQuery, excluded))
	if err != nil {
		return nil, err
	}
	var tables []table
	if err := json.Unmarshal([]byte(got), &tables); err != nil {
		return nil, fmt.Errorf("fingerprint: unexpected list of tables: %w", err)
	}
	return tables, nil
}

// keyTables are the tables whose rows are counted: those of the reference for
// a copy, those of the config, or the largest; and how they were chosen.
func keyTables(c config, tables []table, ref *sdk.Fingerprint) ([]string, string) {
	if ref != nil {
		return countedBy(*ref), "of the reference"
	}
	if len(c.KeyTables) > 0 {
		return c.KeyTables, "configured"
	}
	return largest(tables)
}

// countedBy are the tables a fingerprint counted exactly.
func countedBy(ref sdk.Fingerprint) []string {
	var keys []string
	for _, f := range ref.Facts {
		if name, ok := strings.CutPrefix(f.Name, rowsFact); ok && f.Rule == sdk.RuleExact {
			keys = append(keys, name)
		}
	}
	return keys
}

// largest are the largest tables, at most largestTables of them.
func largest(tables []table) ([]string, string) {
	largest := slices.Clone(tables)
	slices.SortStableFunc(largest, func(a, b table) int { return cmp.Compare(b.Bytes, a.Bytes) })
	keys := make([]string, 0, largestTables)
	for _, t := range largest[:min(largestTables, len(largest))] {
		keys = append(keys, t.key())
	}
	return keys, fmt.Sprintf("the %d largest", len(keys))
}

// countRows counts the rows of each key table exactly, each count within the
// time limit of one and of all; a table the dump does not hold is missing, a
// count over the limit is information, not a failure of the backup.
func (p Plugin) countRows(ctx context.Context, log logger, s *session, keys []string, tables []table) ([]sdk.Fact, error) {
	byKey := make(map[string]table, len(tables))
	for _, t := range tables {
		byKey[t.key()] = t
	}
	deadline := p.now().Add(p.countBudget())
	facts := make([]sdk.Fact, 0, len(keys))
	for _, key := range keys {
		f, err := p.countKey(ctx, log, s, key, byKey, deadline)
		if err != nil {
			return nil, err
		}
		facts = append(facts, f)
	}
	if _, _, err := s.query(ctx, "SET LOCAL statement_timeout = 0;"); err != nil {
		return nil, err
	}
	return facts, nil
}

// countKey counts one key table; one the dump does not hold is missing.
func (p Plugin) countKey(ctx context.Context, log logger, s *session, key string, byKey map[string]table, deadline time.Time) (sdk.Fact, error) {
	t, ok := byKey[key]
	if !ok {
		log.Log(sdk.LevelWarn, fmt.Sprintf("key table %s is not in the dump: recorded as missing", key))
		return sdk.Fact{Name: rowsFact + key, Rule: sdk.RuleExact, Value: missing}, nil
	}
	return p.count(ctx, s, t, deadline.Sub(p.now()))
}

// count is the exact number of rows of a table, or why there is none.
func (p Plugin) count(ctx context.Context, s *session, t table, left time.Duration) (sdk.Fact, error) {
	name := rowsFact + t.key()
	if left <= 0 {
		return sdk.Fact{Name: name, Rule: sdk.RuleInfo, Value: "not counted: the time limit of the counts was spent"}, nil
	}
	limit := min(p.countTimeout(), left)
	got, ok, err := s.query(ctx, fmt.Sprintf("SET LOCAL statement_timeout = %d;\nSELECT 'R' || count(*) FROM %s;", max(limit.Milliseconds(), 1), t.ident()))
	switch {
	case err != nil:
		return sdk.Fact{}, err
	case !ok:
		return sdk.Fact{Name: name, Rule: sdk.RuleInfo, Value: fmt.Sprintf("not counted: the count failed or took longer than %s", limit)}, nil
	}
	return sdk.Fact{Name: name, Rule: sdk.RuleExact, Value: got}, nil
}
