// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package hostsetup_test

import (
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
)

func TestLayoutNamesFilesNextToTheMainConfig(t *testing.T) {
	l := hostsetup.Layout{Config: "/etc/sard/agent.yaml"}
	for got, want := range map[string]string{
		l.FragmentDir():           "/etc/sard/agent.d",
		l.RepositoryFragment("x"): "/etc/sard/agent.d/repo-x.yaml",
		l.SecretFragment("db"):    "/etc/sard/agent.d/secret-db.yaml",
		l.LockFile():              "/etc/sard/agent.d/.sard-config.lock",
		l.SecretsDir():            "/etc/sard/secrets",
		l.SecretFile("db"):        "/etc/sard/secrets/db",
		l.PasswordFile("main"):    "/etc/sard/secrets/restic-main.pass",
		l.EnvFile("main"):         "/etc/sard/secrets/restic-main.env",
	} {
		if got != want {
			t.Errorf("got %s, want %s", got, want)
		}
	}
}

func TestFragmentYAMLReadsBackThroughTheConfigLoader(t *testing.T) {
	dir := t.TempDir()
	l := hostsetup.Layout{Config: dir + "/agent.yaml"}
	files := map[string][]byte{
		l.Config:                  []byte("server: {address: 'a:1'}\n"),
		l.RepositoryFragment("x"): hostsetup.RepositoryYAML("x", "/srv/x", "/p/x.pass"),
		l.SecretFragment("db"):    hostsetup.SecretYAML("db", "/s/db"),
	}
	writeAll(t, files)
	cfg, err := config.Load(l.Config)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Repositories) != 1 || cfg.Repositories[0] != (config.Repository{Name: "x", URL: "/srv/x", PasswordFile: "/p/x.pass", Fragment: l.RepositoryFragment("x")}) {
		t.Fatalf("repositories = %+v", cfg.Repositories)
	}
	if cfg.Secrets["db"] != "/s/db" {
		t.Fatalf("secrets = %v", cfg.Secrets)
	}
	if !strings.HasPrefix(string(files[l.RepositoryFragment("x")]), "#") {
		t.Fatal("the fragment does not say who wrote it")
	}
}

func TestFragmentYAMLQuotesWhatNeedsIt(t *testing.T) {
	dir := t.TempDir()
	l := hostsetup.Layout{Config: dir + "/agent.yaml"}
	odd := "/srv/with: colon #and hash"
	writeAll(t, map[string][]byte{
		l.Config:                  []byte("server: {address: 'a:1'}\n"),
		l.RepositoryFragment("x"): hostsetup.RepositoryYAML("x", odd, "/p"),
	})
	cfg, err := config.Load(l.Config)
	if err != nil || cfg.Repositories[0].URL != odd {
		t.Fatalf("url = %q, err = %v", cfg.Repositories[0].URL, err)
	}
}

func TestReferencesToAPathAreFoundInEveryKey(t *testing.T) {
	cfg := config.Config{
		TLS:          config.TLS{CAFile: "/t/ca", CertFile: "/t/cert", KeyFile: "/t/key"},
		Repositories: []config.Repository{{Name: "base", URL: "/b", PasswordFile: "/p/pass", EnvFile: "/p/env"}},
		Secrets:      map[string]string{"pg": "/s/pg", "db": "/s/db"},
		Scripts:      map[string]string{"maint": "/bin/maint"},
	}
	cases := map[string]string{
		"/t/key":     "tls.key_file",
		"/t/cert":    "tls.cert_file",
		"/t/ca":      "tls.ca_file",
		"/p/pass":    `password_file of repository "base"`,
		"/p/env":     `env_file of repository "base"`,
		"/s/pg":      `secrets.pg`,
		"/bin/maint": `scripts.maint`,
		"/free":      "",
	}
	for path, want := range cases {
		if got := hostsetup.ReferencedBy(cfg, path, ""); got != want {
			t.Errorf("%s: referenced by %q, want %q", path, got, want)
		}
	}
	if got := hostsetup.ReferencedBy(cfg, "/s/db", "secrets.db"); got != "" {
		t.Errorf("a key's own reference counted: %q", got)
	}
	if got := hostsetup.ReferencedBy(cfg, "/p/pass", `password_file of repository "base"`); got != "" {
		t.Errorf("a key's own reference counted: %q", got)
	}
}

func TestAnEnvFileIsNamedInTheFragmentOfARepository(t *testing.T) {
	dir := t.TempDir()
	l := hostsetup.Layout{Config: dir + "/agent.yaml"}
	writeAll(t, map[string][]byte{
		l.Config:                  []byte("server: {address: 'a:1'}\n"),
		l.RepositoryFragment("x"): hostsetup.RepositoryYAMLWithEnv("x", "s3:https://h/b", l.PasswordFile("x"), l.EnvFile("x")),
	})
	cfg, err := config.Load(l.Config)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Repositories[0].EnvFile; got != dir+"/secrets/restic-x.env" {
		t.Fatalf("env_file = %q", got)
	}
	if strings.Contains(string(hostsetup.RepositoryYAML("x", "/srv/x", "/p")), "env_file") {
		t.Fatal("a repository without an env file names one")
	}
}
