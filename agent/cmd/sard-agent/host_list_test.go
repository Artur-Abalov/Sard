// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
)

// extraFragment is D/agent.d/repo-extra.yaml with the repository extra.
func (h *setupHost) extraFragment() string {
	path := h.path("agent.d/repo-extra.yaml")
	h.write(path, string(hostsetup.RepositoryYAML("extra", h.path("extra"), h.path("secrets/restic-extra.pass"))), 0o640)
	h.write(h.path("secrets/restic-extra.pass"), "extra-password\n", 0o600)
	return path
}

type listJSON struct {
	Repositories []struct {
		Name         string  `json:"name"`
		Backend      string  `json:"backend"`
		Status       string  `json:"status"`
		RepositoryID *string `json:"repository_id"`
		DefinedIn    string  `json:"defined_in"`
	} `json:"repositories"`
}

// Правило "repo list показывает репозитории фрагментов и умеет JSON".

// listAsJSON is repo list --json of C with an initialised base and the
// repository extra of a fragment; it returns the fragment's path too.
func listAsJSON(t *testing.T) (*setupHost, listJSON, string) {
	t.Helper()
	h := newSetupHost(t)
	h.base().initialized = true
	fragment := h.extraFragment()
	code, stdout, stderr := h.sudo("repo", "list", "--json", "--config", "C")
	assertCode(t, code, exitOK)
	if stderr != "" {
		t.Fatalf("stderr %q", stderr)
	}
	var got listJSON
	if err := json.Unmarshal([]byte(stdout), &got); err != nil || len(got.Repositories) != 2 {
		t.Fatalf("stdout %q, err %v", stdout, err)
	}
	return h, got, fragment
}

// Список репозиториев в JSON: основной конфиг
func TestRepoListInJSONDescribesTheRepositoryOfTheMainConfig(t *testing.T) {
	h, got, _ := listAsJSON(t)
	base := got.Repositories[0]
	if base.Name != "base" || base.Backend != "local" || base.Status != "initialized" || base.DefinedIn != h.cfgPath {
		t.Fatalf("base: %+v", base)
	}
	if base.RepositoryID == nil || *base.RepositoryID != h.base().id {
		t.Fatalf("base id: %v", base.RepositoryID)
	}
}

// Список репозиториев в JSON: фрагмент
func TestRepoListInJSONNamesTheFragmentOfARepository(t *testing.T) {
	_, got, fragment := listAsJSON(t)
	if extra := got.Repositories[1]; extra.Name != "extra" || extra.DefinedIn != fragment {
		t.Fatalf("extra: %+v", extra)
	}
}

// В JSON неизвестный repository_id — null
func TestUnknownRepositoryIDIsNullInJSON(t *testing.T) {
	h := newSetupHost(t)
	code, stdout, _ := h.sudo("repo", "list", "--json", "--config", "C")
	assertCode(t, code, exitOK)
	var got listJSON
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatal(err)
	}
	if row := got.Repositories[0]; row.RepositoryID != nil || row.Status != "not-initialized" {
		t.Fatalf("row %+v", row)
	}
	if !strings.Contains(stdout, `"repository_id":null`) {
		t.Fatalf("stdout %q", stdout)
	}
}

// Ошибки строк идут текстом в stderr, коды выхода те же
func TestJSONListKeepsProblemMessagesInStderrAndTheExitCode(t *testing.T) {
	h := newSetupHost(t)
	h.base().fatal = "connection refused"
	code, stdout, stderr := h.sudo("repo", "list", "--json", "--config", "C")
	assertCode(t, code, exitTemporary)
	if !strings.Contains(stderr, "base: ") || !strings.Contains(stdout, `"status":"BACKEND_UNAVAILABLE"`) {
		t.Fatalf("stdout %q, stderr %q", stdout, stderr)
	}
}

// Без репозиториев JSON — пустой массив
func TestJSONListWithoutRepositoriesIsAnEmptyArray(t *testing.T) {
	h := newSetupHost(t)
	h.cfg.Repositories = nil
	h.saveConfig()
	code, stdout, _ := h.sudo("repo", "list", "--json", "--config", "C")
	assertCode(t, code, exitOK)
	if strings.TrimSpace(stdout) != `{"repositories":[]}` {
		t.Fatalf("stdout %q", stdout)
	}
}

// Репозитории фрагментов идут после основного конфига в порядке имён файлов
func TestFragmentRepositoriesFollowTheMainConfigInTheList(t *testing.T) {
	h := newSetupHost(t)
	h.write(h.path("agent.d/repo-b.yaml"), string(hostsetup.RepositoryYAML("zeta", h.path("zeta"), h.path("secrets/z.pass"))), 0o640)
	h.write(h.path("agent.d/repo-a.yaml"), string(hostsetup.RepositoryYAML("alpha", h.path("alpha"), h.path("secrets/a.pass"))), 0o640)
	h.write(h.path("secrets/a.pass"), "a\n", 0o600)
	h.write(h.path("secrets/z.pass"), "z\n", 0o600)
	code, stdout, _ := h.sudo("repo", "list", "--config", "C")
	assertCode(t, code, exitOK)
	var names []string
	for _, line := range strings.Split(strings.TrimSpace(stdout), "\n")[1:] {
		names = append(names, strings.Fields(line)[0])
	}
	if strings.Join(names, ",") != "base,alpha,zeta" {
		t.Fatalf("stdout %q", stdout)
	}
}

// Файлы фрагментов не на yaml и невалидный конфиг: ошибки называют файл
func TestAFragmentErrorIsAUsageErrorNamingTheFile(t *testing.T) {
	h := newSetupHost(t)
	path := h.path("agent.d/repo-x.yaml")
	h.write(path, "server: {address: 'x:1'}\n", 0o640)
	code, _, stderr := h.sudo("repo", "list", "--config", "C")
	assertCode(t, code, exitUsage)
	if !strings.Contains(stderr, path) || !strings.Contains(stderr, "server") {
		t.Fatalf("stderr %q", stderr)
	}
}

// Одно имя репозитория в основном конфиге и во фрагменте
func TestDuplicateNameIsAUsageErrorNamingBothFiles(t *testing.T) {
	h := newSetupHost(t)
	path := h.path("agent.d/repo-base.yaml")
	h.write(path, string(hostsetup.RepositoryYAML("base", h.path("other"), h.path("secrets/o.pass"))), 0o640)
	code, _, stderr := h.sudo("repo", "list", "--config", "C")
	assertCode(t, code, exitUsage)
	for _, want := range []string{"DUPLICATE_NAME", "base", h.cfgPath, path} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q: %s", want, stderr)
		}
	}
}
