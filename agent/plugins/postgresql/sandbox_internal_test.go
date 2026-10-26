// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package postgresql

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

// recorder is a SandboxHost that records the programs it runs.
type recorder struct {
	sdk.RestoreHost
	ran []sdk.Process
}

func (r *recorder) Run(_ context.Context, p sdk.Process) (int, error) {
	r.ran = append(r.ran, p)
	return 0, nil
}

// A program of the sandbox runs by its name, with the variables of
// PostgreSQL and of the locale only: nothing else of the agent's environment.
func TestTheSandboxRunsAToolByNameWithThePostgresqlAndLocaleVariablesOnly(t *testing.T) {
	r := &recorder{}
	_, _ = sandboxRunner{r}.Run(t.Context(), Cmd{
		Path: "/sandbox/pg_restore", Args: []string{"-l"},
		Env: []string{"HOME=/var/lib/sard-agent", "PGPASSWORD=pw", "AWS_SECRET_ACCESS_KEY=k", "LC_MESSAGES=C", "PGAPPNAME=sard"},
	})

	got := r.ran[0]
	if got.Path != "pg_restore" || !slices.Equal(got.Args, []string{"-l"}) {
		t.Fatalf("ran %+v", got)
	}
	if want := []string{"PGPASSWORD=pw", "LC_MESSAGES=C", "PGAPPNAME=sard"}; !slices.Equal(got.Env, want) {
		t.Fatalf("env %q, want %q", got.Env, want)
	}
}

// The tools of the image are there, executable files of no size: the plugin
// finds them on the PATH of the sandbox without looking at the agent's disk.
func TestTheToolsOfTheSandboxAreAlwaysThere(t *testing.T) {
	info, err := sandboxTools{}.Stat("/sandbox/psql")
	if err != nil {
		t.Fatal(err)
	}
	if info.Name() != "psql" || info.Size() != 0 || info.Mode() != 0o755 || info.IsDir() || !info.ModTime().Equal(time.Time{}) || info.Sys() != nil {
		t.Fatalf("info %v %d %v %v %v %v", info.Name(), info.Size(), info.Mode(), info.IsDir(), info.ModTime(), info.Sys())
	}
}

// The plugin in a sandbox sees no variable of the agent: a PGHOST of the
// agent's service would otherwise reach the tools of the sandbox.
func TestThePluginInASandboxHasNoEnvironmentOfTheAgent(t *testing.T) {
	t.Setenv("PGHOST", "/agent")
	p := Plugin{}.inSandbox(&recorder{})

	if env := p.environ(); !slices.Equal(env, []string{"PATH=/sandbox"}) {
		t.Fatalf("env %q", env)
	}
}

// A size beyond int64 is still a size.
func TestTheLargestSizeOfADatabaseIsASize(t *testing.T) {
	f := sdk.Fingerprint{Facts: []sdk.Fact{{Name: factSize, Rule: sdk.RuleInfo, Value: "18446744073709551615"}}}
	if size, ok := (Plugin{}).CopySize(f); size != 18446744073709551615 || !ok {
		t.Fatalf("size %d, %v", size, ok)
	}
}

// gone is a sandbox whose programs do not run.
type gone struct{ recorder }

var errGone = errors.New("the container is gone")

func (*gone) Run(context.Context, sdk.Process) (int, error) { return -1, errGone }

func TestAQueryThatDidNotRunCarriesWhy(t *testing.T) {
	out, err := sandboxQuery(t.Context(), &gone{}, "postgres", "SELECT 1")
	if out != "" || !errors.Is(err, errGone) {
		t.Fatalf("out %q, err %v", out, err)
	}
}
