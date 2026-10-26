// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package postgresql_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/plugins/postgresql"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

// --- the sandbox a copy needs (V5)

func pgFingerprint(facts ...sdk.Fact) *sdk.Fingerprint {
	return &sdk.Fingerprint{Plugin: "postgresql", Format: sdk.FingerprintFormat, Facts: facts}
}

func TestTheSandboxOfACopyIsTheImageOfTheMajorVersionOfTheSource(t *testing.T) {
	spec, err := postgresql.Plugin{}.Sandbox(pgFingerprint(sdk.Fact{Name: "major_version", Rule: sdk.RuleExact, Value: "17"}))

	want := sdk.SandboxSpec{
		Image: "postgres", Major: "17", DataDir: "/var/lib/postgresql/data",
		Env: []string{"PGDATA=/var/lib/postgresql/data/pgdata"}, PasswordEnv: "POSTGRES_PASSWORD",
	}
	if err != nil || spec.Image != want.Image || spec.Major != want.Major || spec.DataDir != want.DataDir ||
		!slices.Equal(spec.Env, want.Env) || spec.PasswordEnv != want.PasswordEnv {
		t.Fatalf("spec %+v, %v", spec, err)
	}
}

func TestASnapshotWithoutFingerprintTakesTheNewestServerAndOneWithoutAVersionNone(t *testing.T) {
	if spec, err := (postgresql.Plugin{}).Sandbox(nil); err != nil || spec.Major != "" || spec.Image != "postgres" {
		t.Fatalf("spec %+v, %v", spec, err)
	}
	if _, err := (postgresql.Plugin{}).Sandbox(pgFingerprint()); err == nil || !strings.Contains(err.Error(), "major_version") {
		t.Fatalf("err = %v", err)
	}
}

func TestTheSizeOfACopyIsTheSizeOfTheDatabase(t *testing.T) {
	p := postgresql.Plugin{}
	if size, ok := p.CopySize(*pgFingerprint(sdk.Fact{Name: "database_size", Rule: sdk.RuleInfo, Value: "7340032"})); size != 7340032 || !ok {
		t.Fatalf("size %d, %v", size, ok)
	}
	for _, f := range []*sdk.Fingerprint{pgFingerprint(), pgFingerprint(sdk.Fact{Name: "database_size", Value: "big"})} {
		if size, ok := p.CopySize(*f); size != 0 || ok {
			t.Errorf("%v: %d, %v", f, size, ok)
		}
	}
}

// --- a sandbox as the plugin sees it

// boxHost is a SandboxHost whose psql answers queries by a script.
type boxHost struct {
	sdk.RestoreHost
	answers map[string][]string // query -> successive answers ("!" fails)
	ran     []sdk.Process
}

func (h *boxHost) SecretFor(name string, _ sdk.Target) (sdk.Secret, error) {
	if name == sdk.SandboxPassword {
		return sdk.Secret{Value: []byte("pw")}, nil
	}
	return sdk.Secret{}, errors.New("no secret")
}

func (h *boxHost) Run(_ context.Context, p sdk.Process) (int, error) {
	h.ran = append(h.ran, p)
	query := p.Args[len(p.Args)-1]
	next := h.answers[query]
	if len(next) == 0 {
		return -1, errors.New("unexpected " + query)
	}
	answer := next[0]
	if len(next) > 1 {
		h.answers[query] = next[1:]
	}
	if answer == "!" {
		p.Stderr("psql: error: connection to server on socket failed")
		return 2, nil
	}
	_, _ = io.WriteString(p.Stdout, answer+"\n")
	return 0, nil
}

const (
	listen     = "SHOW listen_addresses"
	extensions = "SELECT name FROM pg_catalog.pg_available_extensions"
)

func waitingPlugin(slept *int) postgresql.Plugin {
	return postgresql.Plugin{Sleep: func(ctx context.Context, d time.Duration) error {
		*slept++
		if d != 500*time.Millisecond {
			return errors.New("an unexpected poll")
		}
		return ctx.Err()
	}}
}

// The image initialises its cluster with a server without TCP, then starts
// the real one: only the real one listens.
func TestTheSandboxIsReadyOnceTheRealServerListens(t *testing.T) {
	h := &boxHost{answers: map[string][]string{listen: {"!", "", "*"}, extensions: {"plpgsql\npg_trgm\nhstore"}}}
	slept := 0
	f := pgFingerprint(sdk.Fact{Name: "extension:pg_trgm", Rule: sdk.RuleExact, Value: "present"}, sdk.Fact{Name: "tables", Value: "3"})

	err := waitingPlugin(&slept).SandboxReady(t.Context(), h, f)

	if err != nil || slept != 2 {
		t.Fatalf("err %v, slept %d", err, slept)
	}
	first := h.ran[0]
	want := []string{"-X", "-A", "-t", "-q", "-h", "/var/run/postgresql", "-U", "postgres", "-d", "postgres", "-c", listen}
	if first.Path != "psql" || !slices.Equal(first.Args, want) || !slices.Equal(first.Env, []string{"LC_MESSAGES=C"}) {
		t.Fatalf("psql %+v", first)
	}
}

// Check 5: an extension of the source that the image lacks cannot be verified.
func TestAnExtensionTheImageLacksIsARefusalNamingIt(t *testing.T) {
	h := &boxHost{answers: map[string][]string{listen: {"*"}, extensions: {"plpgsql"}}}
	f := pgFingerprint(sdk.Fact{Name: "extension:postgis", Rule: sdk.RuleExact, Value: "present"})

	err := (postgresql.Plugin{}).SandboxReady(t.Context(), h, f)

	var refusal *sdk.SandboxRefusal
	if !errors.As(err, &refusal) || !strings.Contains(refusal.Reason, "no extension postgis") {
		t.Fatalf("err = %v", err)
	}
}

func TestASandboxThatNeverServesEndsWithTheStage(t *testing.T) {
	h := &boxHost{answers: map[string][]string{listen: {"!"}}}
	ctx, cancel := context.WithCancel(t.Context())
	slept := 0
	p := postgresql.Plugin{Sleep: func(context.Context, time.Duration) error {
		slept++
		if slept == 3 {
			cancel()
			return context.Canceled
		}
		return nil
	}}

	err := p.SandboxReady(ctx, h, nil)

	if !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "connection to server on socket failed") {
		t.Fatalf("err = %v", err)
	}
}

func TestASnapshotWithoutFingerprintNeedsNoExtension(t *testing.T) {
	h := &boxHost{answers: map[string][]string{listen: {"*"}}}
	if err := (postgresql.Plugin{}).SandboxReady(t.Context(), h, nil); err != nil {
		t.Fatal(err)
	}
	if len(h.ran) != 1 {
		t.Fatalf("ran %d", len(h.ran))
	}
}

func TestTheListOfExtensionsThatCannotBeReadIsTheFailure(t *testing.T) {
	h := &boxHost{answers: map[string][]string{listen: {"*"}, extensions: {"!"}}}
	f := pgFingerprint(sdk.Fact{Name: "extension:pg_trgm", Rule: sdk.RuleExact, Value: "present"})
	err := (postgresql.Plugin{}).SandboxReady(t.Context(), h, f)

	// A list that could not be read refuses nothing: it is a failure of its own.
	var refusal *sdk.SandboxRefusal
	if err == nil || errors.As(err, &refusal) || !strings.Contains(err.Error(), "psql exited 2") {
		t.Fatalf("err = %v", err)
	}
}

func TestACopyOpensWhenItsDatabaseAnswersFromItsCatalog(t *testing.T) {
	h := &boxHost{answers: map[string][]string{"SELECT count(*) FROM pg_catalog.pg_class": {"412", "!"}}}
	c := sdk.RestoredCopy{Config: sdk.Config(`{"database":"sard_verify"}`)}

	if err := (postgresql.Plugin{}).OpenCopy(t.Context(), h, c); err != nil {
		t.Fatal(err)
	}
	if got := h.ran[0].Args[9]; got != "sard_verify" {
		t.Fatalf("database %q", got)
	}
	if err := (postgresql.Plugin{}).OpenCopy(t.Context(), h, c); err == nil {
		t.Fatal("a copy that does not answer opened")
	}
	ran := len(h.ran)
	if err := (postgresql.Plugin{}).OpenCopy(t.Context(), h, sdk.RestoredCopy{Config: sdk.Config("{")}); err == nil || len(h.ran) != ran {
		t.Fatalf("a broken config: err %v, ran %d more", err, len(h.ran)-ran)
	}
}

// --- the restore and the fingerprint of a copy in the sandbox

// rigBox is the restore rig as a sandbox: its server runs the tools.
type rigBox struct {
	*rpHost
	rig *rpHostRig
	ran []sdk.Process
}

func (b *rigBox) SecretFor(name string, _ sdk.Target) (sdk.Secret, error) {
	if name == sdk.SandboxPassword {
		return sdk.Secret{Value: []byte(rpSecret)}, nil
	}
	return sdk.Secret{}, &sdk.SecretError{Name: name}
}

func (b *rigBox) Run(ctx context.Context, p sdk.Process) (int, error) {
	b.ran = append(b.ran, p)
	return b.rig.Run(ctx, postgresql.Cmd{Path: p.Path, Args: p.Args, Env: p.Env, Stdin: p.Stdin, Stdout: p.Stdout, Stderr: p.Stderr})
}

func TestACopyIsRestoredIntoTheServerOfTheSandboxWithItsTools(t *testing.T) {
	h := newPGHost(t)
	h.host.step = context.Background()
	box := &rigBox{rpHost: h.host, rig: h}
	req := h.request()

	c, err := h.plugin().RestoreInto(t.Context(), box, sdk.Config(`{"host":"db2"}`), sdk.RestoreRequest{
		CommandID: req.CommandID, SnapshotID: req.SnapshotID, Repository: req.Repository, Source: req.Source,
	})

	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	_ = json.Unmarshal(c.Config, &cfg)
	if cfg["host"] != "/var/run/postgresql" || cfg["database"] != "sard_verify" || cfg["user"] != "postgres" || cfg["password_ref"] != sdk.SandboxPassword {
		t.Fatalf("config of the copy %v", cfg)
	}
	requireRanInTheSandbox(t, box.ran)
}

// requireRanInTheSandbox: every tool ran by its name, with nothing of the
// agent's environment but the variables of PostgreSQL and of the locale.
func requireRanInTheSandbox(t *testing.T, ran []sdk.Process) {
	t.Helper()
	if len(ran) == 0 {
		t.Fatal("nothing ran in the sandbox")
	}
	for _, p := range ran {
		if strings.Contains(p.Path, "/") {
			t.Errorf("%s ran by its path", p.Path)
		}
		for _, v := range p.Env {
			if !strings.HasPrefix(v, "PG") && !strings.HasPrefix(v, "LC_") {
				t.Errorf("%s got %s", p.Path, v)
			}
		}
	}
}

func TestTheCopyIsMeasuredInTheSandbox(t *testing.T) {
	h := newPGHost(t)
	box := &refusingBox{rigBox: rigBox{rpHost: h.host, rig: h}}
	cfg := sdk.Config(`{"host":"/var/run/postgresql","port":5432,"database":"sard_verify","user":"postgres","password_ref":"sard.sandbox"}`)

	_, err := h.plugin().Fingerprint(t.Context(), box, cfg, sdk.FingerprintTarget{Reference: pgFingerprint()})

	if err == nil || len(box.ran) == 0 || box.ran[0].Path != "psql" {
		t.Fatalf("err %v, ran %+v", err, box.ran)
	}
	for _, v := range box.ran[0].Env {
		if !strings.HasPrefix(v, "PG") && !strings.HasPrefix(v, "LC_") {
			t.Errorf("the sandbox got %q of the agent's environment", v)
		}
	}
}

// refusingBox runs nothing: every program of the sandbox fails at once.
type refusingBox struct{ rigBox }

func (b *refusingBox) Run(_ context.Context, p sdk.Process) (int, error) {
	b.ran = append(b.ran, p)
	return -1, errors.New("the sandbox is gone")
}

// Without a Sleep of its own the plugin waits for the time of a poll.
func TestTheServerIsPolledEveryHalfSecond(t *testing.T) {
	h := &boxHost{answers: map[string][]string{listen: {"!", "*"}}}
	begun := time.Now()

	err := (postgresql.Plugin{}).SandboxReady(t.Context(), h, nil)

	if err != nil || time.Since(begun) < 500*time.Millisecond || len(h.ran) != 2 {
		t.Fatalf("err %v after %v, ran %d", err, time.Since(begun), len(h.ran))
	}
}

func TestAWaitEndsWithTheCauseOfItsContext(t *testing.T) {
	h := &boxHost{answers: map[string][]string{listen: {"!"}}}
	stop := errors.New("the stage ran out of time")
	ctx, cancel := context.WithCancelCause(t.Context())
	cancel(stop)

	err := (postgresql.Plugin{}).SandboxReady(ctx, h, nil)

	if !errors.Is(err, stop) {
		t.Fatalf("err = %v", err)
	}
}
