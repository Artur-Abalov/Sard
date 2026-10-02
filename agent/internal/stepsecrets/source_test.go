// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package stepsecrets_test

import (
	"errors"
	"io/fs"
	"slices"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/executor"
	"github.com/Artur-Abalov/sard/agent/internal/stepsecrets"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

var _ executor.Secrets = (*stepsecrets.Source)(nil)

// files is a fake file system; a missing file is fs.ErrNotExist with its path.
type files map[string]string

func (f files) read(name string) ([]byte, error) {
	data, ok := f[name]
	if !ok {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	return []byte(data), nil
}

func cfg() config.Config {
	return config.Config{
		Secrets: map[string]string{"pg": "/etc/sard/pg", "api": "/etc/sard/api", "blank": "/etc/sard/blank"},
		Repositories: []config.Repository{
			{Name: "main", URL: "rest:http://u:url-pass@host/x", PasswordFile: "/etc/sard/main.pass", EnvFile: "/etc/sard/main.env"},
			{Name: "plain", URL: "/srv/restic", PasswordFile: "/etc/sard/plain.pass"},
			{Name: "other", URL: "/srv/other", PasswordFile: "/etc/sard/other.pass", EnvFile: "/etc/sard/other.env"},
		},
	}
}

func allFiles() files {
	return files{
		"/etc/sard/pg":         "pg-secret\n",
		"/etc/sard/api":        "api-token\r\n",
		"/etc/sard/blank":      "\n",
		"/etc/sard/main.env":   "# creds\nAWS_ACCESS_KEY_ID=AKIAEXAMPLE\nAWS_SECRET_ACCESS_KEY=aws-secret\nEMPTY=\n",
		"/etc/sard/other.env":  "OTHER=other-secret\n",
		"/etc/sard/main.pass":  "never-read",
		"/etc/sard/plain.pass": "never-read",
	}
}

func step(repo string) *agentv1.RunStep {
	return &agentv1.RunStep{CommandId: "c1", RepositoryName: repo}
}

func values(t *testing.T, got []executor.Secret) []string {
	t.Helper()
	var out []string
	for _, s := range got {
		out = append(out, s.Name+"="+string(s.Value))
	}
	return out
}

func TestEverySecretAndTheStepRepositorysEnvFileAndURLPassword(t *testing.T) {
	got, err := stepsecrets.New(cfg(), allFiles().read).For(step("main"))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"secret api=api-token",
		"secret blank=",
		"secret pg=pg-secret",
		`env_file of repository "main": AWS_ACCESS_KEY_ID=AKIAEXAMPLE`,
		`env_file of repository "main": AWS_SECRET_ACCESS_KEY=aws-secret`,
		`env_file of repository "main": EMPTY=`,
		`url of repository "main"=url-pass`,
	}
	if got := values(t, got); !slices.Equal(got, want) {
		t.Fatalf("values =\n%q\nwant\n%q", got, want)
	}
}

func TestARepositoryWithoutEnvFileOrPasswordAddsNothing(t *testing.T) {
	got, err := stepsecrets.New(cfg(), allFiles().read).For(step("plain"))
	if err != nil || len(got) != 3 {
		t.Fatalf("values = %q, err = %v", values(t, got), err)
	}
}

func TestAStepWithoutRepositoryGetsTheSecretsOnly(t *testing.T) {
	got, err := stepsecrets.New(cfg(), allFiles().read).For(step(""))
	if err != nil || len(got) != 3 {
		t.Fatalf("values = %q, err = %v", values(t, got), err)
	}
}

func TestTheRepositoryPasswordFileIsNeverRead(t *testing.T) {
	read := allFiles()
	var opened []string
	_, err := stepsecrets.New(cfg(), func(name string) ([]byte, error) {
		opened = append(opened, name)
		return read.read(name)
	}).For(step("main"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range opened {
		if strings.HasSuffix(name, ".pass") {
			t.Fatalf("read %s (ADR 0008)", name)
		}
	}
}

func TestAnUnreadableSecretIsNamedWithoutItsPath(t *testing.T) {
	f := allFiles()
	delete(f, "/etc/sard/pg")
	_, err := stepsecrets.New(cfg(), f.read).For(step("main"))
	if err == nil || err.Error() != `cannot read secret "pg": file does not exist` || !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("err = %v", err)
	}
}

func TestAnUnreadableEnvFileIsNamedByItsRepository(t *testing.T) {
	f := allFiles()
	delete(f, "/etc/sard/main.env")
	_, err := stepsecrets.New(cfg(), f.read).For(step("main"))
	if err == nil || err.Error() != `cannot read env_file of repository "main": file does not exist` {
		t.Fatalf("err = %v", err)
	}
}

func TestAnInvalidEnvFileNamesTheLineNeverAValue(t *testing.T) {
	f := allFiles()
	f["/etc/sard/main.env"] = "TOKEN=fine\nnot an assignment s3cr3t\n"
	_, err := stepsecrets.New(cfg(), f.read).For(step("main"))
	if err == nil || strings.Contains(err.Error(), "s3cr3t") || !strings.Contains(err.Error(), `env_file of repository "main"`) || !strings.Contains(err.Error(), "line 2") {
		t.Fatalf("err = %v", err)
	}
}

func TestAReadErrorWithoutAPathKeepsItsText(t *testing.T) {
	_, err := stepsecrets.New(cfg(), func(string) ([]byte, error) { return nil, errors.New("boom") }).For(step(""))
	if err == nil || err.Error() != `cannot read secret "api": boom` {
		t.Fatalf("err = %v", err)
	}
}

func TestAnUnknownRepositoryAddsNothing(t *testing.T) {
	got, err := stepsecrets.New(cfg(), allFiles().read).For(step("nas"))
	if err != nil || len(got) != 3 {
		t.Fatalf("values = %q, err = %v", values(t, got), err)
	}
}
