// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package hostsetup_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
	"github.com/Artur-Abalov/sard/agent/internal/refusal"
)

func loadPlanConfig(t *testing.T, main string, files map[string][]byte) (config.Config, hostsetup.Layout) {
	t.Helper()
	l := hostsetup.Layout{Config: filepath.Join(t.TempDir(), "agent.yaml")}
	files[l.Config] = []byte("server: {address: 'a:1'}\n" + main)
	writeAll(t, files)
	cfg, err := config.Load(l.Config)
	if err != nil {
		t.Fatal(err)
	}
	return cfg, l
}

func wantReason(t *testing.T, f *refusal.Failure, want refusal.Reason) {
	t.Helper()
	if f == nil || f.Reason != want {
		t.Fatalf("failure = %v, want reason %s", f, want)
	}
}

func TestPlanSecret(t *testing.T) {
	t.Run("new", func(t *testing.T) {
		cfg, l := loadPlanConfig(t, "", map[string][]byte{})
		if p, f := hostsetup.PlanSecret(cfg, l, "db"); f != nil || p.OwnFragment {
			t.Fatalf("plan = %+v, failure = %v", p, f)
		}
	})
	t.Run("own fragment", func(t *testing.T) {
		l := hostsetup.Layout{Config: filepath.Join(t.TempDir(), "agent.yaml")}
		writeAll(t, map[string][]byte{
			l.Config:               []byte("server: {address: 'a:1'}\n"),
			l.SecretFragment("db"): hostsetup.SecretYAML("db", l.SecretFile("db")),
		})
		cfg, err := config.Load(l.Config)
		if err != nil {
			t.Fatal(err)
		}
		if p, f := hostsetup.PlanSecret(cfg, l, "db"); f != nil || !p.OwnFragment {
			t.Fatalf("plan = %+v, failure = %v", p, f)
		}
	})
	t.Run("defined elsewhere", func(t *testing.T) {
		cfg, l := loadPlanConfig(t, "secrets: {db: /other/db}\n", map[string][]byte{})
		_, f := hostsetup.PlanSecret(cfg, l, "db")
		wantReason(t, f, refusal.DefinedInConfig)
	})
	t.Run("value file in use", func(t *testing.T) {
		cfg, l := loadPlanConfig(t, "secrets: {pg: /x}\n", map[string][]byte{})
		cfg.Secrets["pg"] = l.SecretFile("db")
		_, f := hostsetup.PlanSecret(cfg, l, "db")
		wantReason(t, f, refusal.PathInUse)
	})
}

func TestPlanRepository(t *testing.T) {
	t.Run("new", func(t *testing.T) {
		cfg, l := loadPlanConfig(t, "", map[string][]byte{})
		if p, f := hostsetup.PlanRepository(cfg, l, "r", "/srv/r"); f != nil || p != hostsetup.AddNew {
			t.Fatalf("plan = %v, failure = %v", p, f)
		}
	})
	t.Run("same address again", func(t *testing.T) {
		cfg, l := planWithFragment(t, "/srv/r")
		if p, f := hostsetup.PlanRepository(cfg, l, "r", "/srv/r"); f != nil || p != hostsetup.AddUnchanged {
			t.Fatalf("plan = %v, failure = %v", p, f)
		}
	})
	t.Run("another address", func(t *testing.T) {
		cfg, l := planWithFragment(t, "/srv/r")
		_, f := hostsetup.PlanRepository(cfg, l, "r", "/srv/other")
		wantReason(t, f, refusal.RepositoryConflict)
	})
	t.Run("defined elsewhere", func(t *testing.T) {
		cfg, l := loadPlanConfig(t, "repositories:\n  - {name: r, url: /srv/r, password_file: /p/r}\n", map[string][]byte{})
		_, f := hostsetup.PlanRepository(cfg, l, "r", "/srv/r")
		wantReason(t, f, refusal.DefinedInConfig)
	})
	t.Run("password file in use", func(t *testing.T) {
		cfg, l := loadPlanConfig(t, "", map[string][]byte{})
		cfg.Repositories = []config.Repository{{Name: "other", URL: "/o", PasswordFile: l.PasswordFile("r")}}
		_, f := hostsetup.PlanRepository(cfg, l, "r", "/srv/r")
		wantReason(t, f, refusal.PathInUse)
	})
}

func planWithFragment(t *testing.T, url string) (config.Config, hostsetup.Layout) {
	t.Helper()
	l := hostsetup.Layout{Config: filepath.Join(t.TempDir(), "agent.yaml")}
	writeAll(t, map[string][]byte{
		l.Config:                  []byte("server: {address: 'a:1'}\n"),
		l.RepositoryFragment("r"): hostsetup.RepositoryYAML("r", url, l.PasswordFile("r")),
	})
	cfg, err := config.Load(l.Config)
	if err != nil {
		t.Fatal(err)
	}
	return cfg, l
}

func TestPlanRemoval(t *testing.T) {
	if r, f := hostsetup.PlanRemoval("secret", "db", "", "/own"); f != nil || r != hostsetup.NothingToRemove {
		t.Fatalf("undefined: %v, %v", r, f)
	}
	if r, f := hostsetup.PlanRemoval("secret", "db", "/own", "/own"); f != nil || r != hostsetup.RemoveOwn {
		t.Fatalf("own: %v, %v", r, f)
	}
	_, f := hostsetup.PlanRemoval("secret", "db", "/main", "/own")
	wantReason(t, f, refusal.DefinedInConfig)
}

func TestRepositoryNamed(t *testing.T) {
	cfg := config.Config{Repositories: []config.Repository{{Name: "a", URL: "/a"}, {Name: "b", URL: "/b"}}}
	if got := hostsetup.RepositoryNamed(cfg, "b"); got.URL != "/b" {
		t.Errorf("got %+v", got)
	}
	if got := hostsetup.RepositoryNamed(cfg, "zz"); got.Name != "" {
		t.Errorf("unknown name gave %+v", got)
	}
}

// A8b, Р30, Р45: an S3 address is compared as given, not as a path, and the
// env file of the name is not taken from another key.
func TestPlanRepositoryForAnS3Address(t *testing.T) {
	const s3 = "s3:https://s3.example.com/bucket-b/extra"
	t.Run("the same address again is unchanged", func(t *testing.T) {
		cfg, l := planWithEnvFragment(t, s3)
		if p, f := hostsetup.PlanRepository(cfg, l, "r", s3); f != nil || p != hostsetup.AddUnchanged {
			t.Fatalf("plan = %v, failure = %v", p, f)
		}
	})
	t.Run("another bucket is a conflict that shows the current address", func(t *testing.T) {
		cfg, l := planWithEnvFragment(t, s3)
		_, f := hostsetup.PlanRepository(cfg, l, "r", "s3:https://s3.example.com/bucket-b/other")
		wantReason(t, f, refusal.RepositoryConflict)
		if !strings.Contains(f.Detail, s3) {
			t.Fatalf("detail %q", f.Detail)
		}
	})
	t.Run("the env file is used by another key", func(t *testing.T) {
		cfg, l := loadPlanConfig(t, "", map[string][]byte{})
		cfg.Repositories = []config.Repository{{Name: "base", URL: "/b", PasswordFile: "/p", EnvFile: l.EnvFile("r")}}
		_, f := hostsetup.PlanRepository(cfg, l, "r", s3)
		wantReason(t, f, refusal.PathInUse)
		if !strings.Contains(f.Detail, `env_file of repository "base"`) {
			t.Fatalf("detail %q", f.Detail)
		}
	})
	t.Run("a local repository does not care about the env file", func(t *testing.T) {
		cfg, l := loadPlanConfig(t, "", map[string][]byte{})
		cfg.Repositories = []config.Repository{{Name: "base", URL: "/b", PasswordFile: "/p", EnvFile: l.EnvFile("r")}}
		if _, f := hostsetup.PlanRepository(cfg, l, "r", "/srv/r"); f != nil {
			t.Fatalf("failure = %v", f)
		}
	})
}

func planWithEnvFragment(t *testing.T, url string) (config.Config, hostsetup.Layout) {
	t.Helper()
	l := hostsetup.Layout{Config: filepath.Join(t.TempDir(), "agent.yaml")}
	writeAll(t, map[string][]byte{
		l.Config:                  []byte("server: {address: 'a:1'}\n"),
		l.RepositoryFragment("r"): hostsetup.RepositoryYAMLWithEnv("r", url, l.PasswordFile("r"), l.EnvFile("r")),
	})
	cfg, err := config.Load(l.Config)
	if err != nil {
		t.Fatal(err)
	}
	return cfg, l
}
