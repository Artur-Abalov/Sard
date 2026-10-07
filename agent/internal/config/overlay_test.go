// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/config"
)

const mainYAML = `
server:
  address: sard.example.com:9090
repositories:
  - {name: base, url: /srv/base, password_file: /etc/sard/secrets/base.pass}
secrets:
  pg: /etc/sard/secrets/pg
`

// overlayHost is D: the main config C and, optionally, D/agent.d.
type overlayHost struct {
	t    *testing.T
	dir  string
	main string
}

func newOverlayHost(t *testing.T, mainBody string) *overlayHost {
	t.Helper()
	h := &overlayHost{t: t, dir: t.TempDir()}
	h.main = filepath.Join(h.dir, "agent.yaml")
	h.write("agent.yaml", mainBody)
	return h
}

func (h *overlayHost) write(rel, body string) string {
	h.t.Helper()
	path := filepath.Join(h.dir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		h.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		h.t.Fatal(err)
	}
	return path
}

func repoFragment(name string) string {
	return "repositories:\n  - {name: " + name + ", url: /srv/" + name + ", password_file: /etc/sard/secrets/restic-" + name + ".pass}\n"
}

func TestFragmentRepositoriesFollowTheMainConfigInFileNameOrder(t *testing.T) {
	h := newOverlayHost(t, mainYAML)
	h.write("agent.d/repo-b.yaml", repoFragment("zeta"))
	h.write("agent.d/repo-a.yaml", repoFragment("alpha"))
	cfg, err := config.Load(h.main)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, r := range cfg.Repositories {
		names = append(names, r.Name)
	}
	if got := strings.Join(names, ","); got != "base,alpha,zeta" {
		t.Fatalf("repositories = %s, want base,alpha,zeta", got)
	}
}

func TestFragmentSecretsJoinTheMainOnes(t *testing.T) {
	h := newOverlayHost(t, mainYAML)
	h.write("agent.d/secret-db.yaml", "secrets:\n  db: /etc/sard/secrets/db\n")
	cfg, err := config.Load(h.main)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(cfg.SecretNames(), ","); got != "db,pg" {
		t.Fatalf("secret names = %s, want db,pg", got)
	}
}

func TestEveryDefinitionKnowsTheFileItCameFrom(t *testing.T) {
	h := newOverlayHost(t, mainYAML)
	frag := h.write("agent.d/repo-extra.yaml", repoFragment("extra"))
	sfrag := h.write("agent.d/secret-db.yaml", "secrets:\n  db: /etc/sard/secrets/db\n")
	cfg, err := config.Load(h.main)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"base": h.main, "extra": frag}
	for _, r := range cfg.Repositories {
		if got := cfg.RepositorySource(r.Name); got != want[r.Name] {
			t.Errorf("repository %s defined in %q, want %q", r.Name, got, want[r.Name])
		}
	}
	if got := cfg.SecretSource("pg"); got != h.main {
		t.Errorf("secret pg defined in %q, want %q", got, h.main)
	}
	if got := cfg.SecretSource("db"); got != sfrag {
		t.Errorf("secret db defined in %q, want %q", got, sfrag)
	}
	if got := cfg.SecretSource("nope"); got != "" {
		t.Errorf("unknown secret defined in %q, want none", got)
	}
}

func TestOnlyYamlFilesOfTheFragmentDirectoryAreRead(t *testing.T) {
	for _, name := range []string{".repo-x.yaml.tmp-1234", "repo-x.yaml.rpmsave", "README", "repo-x.yaml~"} {
		t.Run(name, func(t *testing.T) {
			h := newOverlayHost(t, mainYAML)
			h.write("agent.d/"+name, "this: [is not\n  yaml")
			if _, err := config.Load(h.main); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestNoFragmentDirectoryMeansTheMainFileAlone(t *testing.T) {
	h := newOverlayHost(t, mainYAML)
	cfg, err := config.Load(h.main)
	if err != nil || len(cfg.Repositories) != 1 {
		t.Fatalf("cfg = %+v, err = %v", cfg, err)
	}
}

type duplicateCase struct {
	name  string
	files map[string]string
	kind  string
	dup   string
	first string // path relative to D
	later string
}

var duplicateCases = []duplicateCase{
	{"repository in main and a fragment", map[string]string{"agent.d/repo-base.yaml": repoFragment("base")}, "repository", "base", "agent.yaml", "agent.d/repo-base.yaml"},
	{"secret in two fragments", map[string]string{
		"agent.d/secret-a.yaml": "secrets:\n  db: /x/a\n",
		"agent.d/secret-b.yaml": "secrets:\n  db: /x/b\n",
	}, "secret", "db", "agent.d/secret-a.yaml", "agent.d/secret-b.yaml"},
	{"secret in main and a fragment", map[string]string{"agent.d/secret-pg.yaml": "secrets:\n  pg: /x/pg\n"}, "secret", "pg", "agent.yaml", "agent.d/secret-pg.yaml"},
	{"repository twice in one fragment", map[string]string{"agent.d/repo-x.yaml": repoFragment("x") + "  - {name: x, url: /y, password_file: /z}\n"}, "repository", "x", "agent.d/repo-x.yaml", "agent.d/repo-x.yaml"},
}

func TestDuplicateNameNamesBothFiles(t *testing.T) {
	for _, c := range duplicateCases {
		t.Run(c.name, func(t *testing.T) {
			h := newOverlayHost(t, mainYAML)
			for rel, body := range c.files {
				h.write(rel, body)
			}
			_, err := config.Load(h.main)
			var dup *config.DuplicateNameError
			if !errors.As(err, &dup) {
				t.Fatalf("err = %v, want a *DuplicateNameError", err)
			}
			want := config.DuplicateNameError{Kind: c.kind, Name: c.dup, First: filepath.Join(h.dir, c.first), Second: filepath.Join(h.dir, c.later)}
			if *dup != want {
				t.Fatalf("duplicate = %+v, want %+v", *dup, want)
			}
			for _, part := range []string{"DUPLICATE_NAME", c.dup, dup.First, dup.Second} {
				if !strings.Contains(err.Error(), part) {
					t.Errorf("message %q lacks %q", err, part)
				}
			}
		})
	}
}

func TestFragmentWithAnyKeyButRepositoriesAndSecretsIsRefused(t *testing.T) {
	for _, key := range []string{"server", "tls", "scripts", "restic", "executor", "service", "bogus"} {
		t.Run(key, func(t *testing.T) {
			h := newOverlayHost(t, mainYAML)
			path := h.write("agent.d/repo-x.yaml", key+": {a: b}\n")
			_, err := config.Load(h.main)
			var fe *config.FragmentError
			if !errors.As(err, &fe) {
				t.Fatalf("err = %v, want a *FragmentError", err)
			}
			if fe.File != path || fe.Key != key {
				t.Fatalf("fragment error = %+v", fe)
			}
			if !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), key) {
				t.Fatalf("message %q lacks the file and the key", err)
			}
		})
	}
}

func TestFragmentThatIsNotYamlNamesTheFile(t *testing.T) {
	h := newOverlayHost(t, mainYAML)
	path := h.write("agent.d/repo-x.yaml", "repositories: [unclosed\n")
	_, err := config.Load(h.main)
	if err == nil || !strings.Contains(err.Error(), path) {
		t.Fatalf("err = %v, want one naming %s", err, path)
	}
}

func TestFragmentThatCannotBeReadNamesTheFile(t *testing.T) {
	h := newOverlayHost(t, mainYAML)
	// A directory where a file should be: reading it fails even for root.
	path := filepath.Join(h.dir, "agent.d", "repo-x.yaml")
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := config.Load(h.main)
	if err == nil || !strings.Contains(err.Error(), path) {
		t.Fatalf("err = %v, want one naming %s", err, path)
	}
}

func TestFragmentDirectoryThatCannotBeListedNamesIt(t *testing.T) {
	h := newOverlayHost(t, mainYAML)
	dir := filepath.Join(h.dir, "agent.d")
	h.write("agent.d", "a file where the directory should be")
	_, err := config.Load(h.main)
	if err == nil || !strings.Contains(err.Error(), dir) {
		t.Fatalf("err = %v, want one naming %s", err, dir)
	}
}

func TestFragmentRepositoryIsValidatedLikeAMainOne(t *testing.T) {
	h := newOverlayHost(t, mainYAML)
	path := h.write("agent.d/repo-x.yaml", "repositories:\n  - {name: x, url: /srv/x}\n")
	_, err := config.Load(h.main)
	if !errors.Is(err, config.ErrInvalidRepository) || !strings.Contains(err.Error(), path) {
		t.Fatalf("err = %v, want ErrInvalidRepository naming %s", err, path)
	}
}

func TestFragmentUnknownNestedKeyIsRefused(t *testing.T) {
	h := newOverlayHost(t, mainYAML)
	path := h.write("agent.d/repo-x.yaml", "repositories:\n  - {name: x, url: /srv/x, password_file: /p, extra: 1}\n")
	_, err := config.Load(h.main)
	if err == nil || !strings.Contains(err.Error(), path) {
		t.Fatalf("err = %v, want one naming %s", err, path)
	}
}

func TestEmptyFragmentIsAllowed(t *testing.T) {
	h := newOverlayHost(t, mainYAML)
	h.write("agent.d/repo-x.yaml", "")
	if _, err := config.Load(h.main); err != nil {
		t.Fatal(err)
	}
}

func TestOtherFilesAlongsideFragmentsAreSkippedAndFragmentsStillRead(t *testing.T) {
	h := newOverlayHost(t, mainYAML)
	h.write("agent.d/README", "")
	h.write("agent.d/a.yaml", repoFragment("a"))
	cfg, err := config.Load(h.main)
	if err != nil || len(cfg.Repositories) != 2 {
		t.Fatalf("cfg = %+v, err = %v", cfg, err)
	}
}

func TestServiceUserDefaultsAndComesFromTheMainConfigOnly(t *testing.T) {
	h := newOverlayHost(t, mainYAML+"service:\n  user: backup\n")
	cfg, err := config.Load(h.main)
	if err != nil || cfg.ServiceUser() != "backup" {
		t.Fatalf("service user = %q, err = %v", cfg.ServiceUser(), err)
	}
	plain := newOverlayHost(t, mainYAML)
	cfg, err = config.Load(plain.main)
	if err != nil || cfg.ServiceUser() != config.DefaultServiceUser || config.DefaultServiceUser != "sard-agent" {
		t.Fatalf("default service user = %q, err = %v", cfg.ServiceUser(), err)
	}
}

func TestPeekServiceUserIgnoresEveryProblemOfTheFile(t *testing.T) {
	h := newOverlayHost(t, "service:\n  user: backup\n")
	if got := config.PeekServiceUser(h.main); got != "backup" {
		t.Fatalf("peeked %q, want backup", got)
	}
	for name, body := range map[string]string{"invalid yaml": "service: [", "no key": "server: {address: a:1}\n"} {
		bad := newOverlayHost(t, body)
		if got := config.PeekServiceUser(bad.main); got != config.DefaultServiceUser {
			t.Errorf("%s: peeked %q, want the default", name, got)
		}
	}
	if got := config.PeekServiceUser(filepath.Join(h.dir, "missing.yaml")); got != config.DefaultServiceUser {
		t.Fatalf("missing file: peeked %q, want the default", got)
	}
}
