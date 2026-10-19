// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package config_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/config"
)

const restoreBase = "server:\n  address: s:1\n"

// Scenario: Без настройки каталог восстановления — каталог по умолчанию в каталоге состояния службы.
func TestWithoutRestoreDirTheRestoreDirectoryIsInTheStateDirectoryOfTheService(t *testing.T) {
	c, err := config.Parse([]byte(restoreBase))
	if err != nil {
		t.Fatal(err)
	}
	if got := c.RestoreDir(); got != "/var/lib/sard-agent/restore" {
		t.Errorf("RestoreDir() = %q", got)
	}
}

func TestRestoreDirOfTheConfigIsTheRestoreDirectory(t *testing.T) {
	c, err := config.Parse([]byte(restoreBase + "restore:\n  dir: /srv/restore\n"))
	if err != nil || c.RestoreDir() != "/srv/restore" {
		t.Fatalf("RestoreDir() = %q, %v", c.RestoreDir(), err)
	}
}

// Scenario: Недопустимый каталог восстановления останавливает старт агента.
func TestAnInvalidRestoreDirectoryStopsTheAgent(t *testing.T) {
	tests := map[string]string{
		"relative":                        "restore:\n  dir: relative/dir\n",
		"root":                            "restore:\n  dir: /\n",
		"not clean":                       "restore:\n  dir: /srv/a/../b\n",
		"the state directory":             "restore:\n  dir: /srv/state\nexecutor:\n  state_dir: /srv/state\n",
		"the default state directory":     "restore:\n  dir: /var/lib/sard-agent/executor\n",
		"inside the state directory":      "restore:\n  dir: /srv/state/restore\nexecutor:\n  state_dir: /srv/state\n",
		"holding the state directory":     "restore:\n  dir: /srv\nexecutor:\n  state_dir: /srv/state\n",
		"inside the cache of restic":      "restore:\n  dir: /srv/cache/x\nrestic:\n  cache_dir: /srv/cache\n",
		"inside the default cache":        "restore:\n  dir: /var/cache/sard/restic/x\n",
		"holding the cache of restic":     "restore:\n  dir: /srv\nrestic:\n  cache_dir: /srv/cache\n",
		"inside a local repository":       "restore:\n  dir: /srv/repo/restore\nrepositories:\n  - {name: r, url: /srv/repo, password_file: /p}\n",
		"inside a repository with local:": "restore:\n  dir: /srv/repo/restore\nrepositories:\n  - {name: r, url: 'local:/srv/repo', password_file: /p}\n",
		"holding a local repository":      "restore:\n  dir: /srv\nrepositories:\n  - {name: r, url: /srv/repo, password_file: /p}\n",
		"equal to a local repository":     "restore:\n  dir: /srv/repo\nrepositories:\n  - {name: r, url: /srv/repo, password_file: /p}\n",
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := config.Parse([]byte(restoreBase + body))
			if !errors.Is(err, config.ErrInvalidRestore) || !strings.Contains(err.Error(), "restore.dir") {
				t.Fatalf("err = %v, want ErrInvalidRestore naming restore.dir", err)
			}
		})
	}
}

func TestARestoreDirectoryBesideTheOtherDirectoriesIsValid(t *testing.T) {
	body := restoreBase + "restore:\n  dir: /srv/restore\nexecutor:\n  state_dir: /srv/state\nrestic:\n  cache_dir: /srv/cache\n" +
		"repositories:\n  - {name: r, url: /srv/repo, password_file: /p}\n  - {name: s, url: 's3:https://h/b', password_file: /p}\n" +
		"  - {name: t, url: '/srv/restore-repo', password_file: /p}\n"
	if _, err := config.Parse([]byte(body)); err != nil {
		t.Fatal(err)
	}
}

func TestARestoreSectionWithAnotherKeyIsRefused(t *testing.T) {
	if _, err := config.Parse([]byte(restoreBase + "restore:\n  overwrite: true\n")); err == nil {
		t.Fatal("an unknown key of restore passed")
	}
}

// Scenario: restore.dir в agent.d.
func TestAFragmentMaySetTheRestoreDirectory(t *testing.T) {
	h := newOverlayHost(t, mainYAML)
	h.write("agent.d/restore.yaml", "restore:\n  dir: /srv/restore\n")
	cfg, err := config.Load(h.main)
	if err != nil || cfg.RestoreDir() != "/srv/restore" {
		t.Fatalf("RestoreDir() = %q, %v", cfg.RestoreDir(), err)
	}
}

func TestAnInvalidRestoreDirectoryOfAFragmentNamesTheSettingAndTheFile(t *testing.T) {
	h := newOverlayHost(t, mainYAML)
	path := h.write("agent.d/restore.yaml", "restore:\n  dir: relative\n")
	_, err := config.Load(h.main)
	if !errors.Is(err, config.ErrInvalidRestore) || !strings.Contains(err.Error(), "restore.dir") || !strings.Contains(err.Error(), path) {
		t.Fatalf("err = %v", err)
	}
}

func TestTheRestoreDirectoryCannotBeSetTwice(t *testing.T) {
	h := newOverlayHost(t, mainYAML+"restore:\n  dir: /srv/a\n")
	path := h.write("agent.d/restore.yaml", "restore:\n  dir: /srv/b\n")
	_, err := config.Load(h.main)
	var dup *config.DuplicateNameError
	if !errors.As(err, &dup) || dup.Name != "restore.dir" || dup.First != h.main || dup.Second != path {
		t.Fatalf("err = %v, want a *DuplicateNameError for restore.dir", err)
	}
}

func TestTwoFragmentsCannotSetTheRestoreDirectoryBoth(t *testing.T) {
	h := newOverlayHost(t, mainYAML)
	h.write("agent.d/a.yaml", "restore:\n  dir: /srv/a\n")
	h.write("agent.d/b.yaml", "restore:\n  dir: /srv/b\n")
	_, err := config.Load(h.main)
	var dup *config.DuplicateNameError
	if !errors.As(err, &dup) || dup.Name != "restore.dir" {
		t.Fatalf("err = %v", err)
	}
}

func TestAFragmentWithAnEmptyRestoreSectionSetsNothing(t *testing.T) {
	h := newOverlayHost(t, mainYAML)
	h.write("agent.d/a.yaml", "restore: {}\n")
	cfg, err := config.Load(h.main)
	if err != nil || cfg.RestoreDir() != "/var/lib/sard-agent/restore" {
		t.Fatalf("RestoreDir() = %q, %v", cfg.RestoreDir(), err)
	}
}

// A restore.dir of the main config is not blamed on a file of agent.d, even when a fragment is what makes it
// invalid (a repository of the fragment inside it).
func TestAnInvalidRestoreDirectoryOfTheMainConfigIsNotAttributedToAFragment(t *testing.T) {
	h := newOverlayHost(t, mainYAML+"restore:\n  dir: /srv/restore\n")
	h.write("agent.d/repo.yaml", "repositories:\n  - {name: inside, url: /srv/restore/repo, password_file: /p}\n")
	_, err := config.Load(h.main)
	if !errors.Is(err, config.ErrInvalidRestore) || !strings.HasPrefix(err.Error(), config.ErrInvalidRestore.Error()) {
		t.Fatalf("err = %v", err)
	}
}

func TestTheRootIsNotARestoreDirectoryWhateverElseIsConfigured(t *testing.T) {
	_, err := config.Parse([]byte(restoreBase + "restore:\n  dir: /\n"))
	if !errors.Is(err, config.ErrInvalidRestore) || !strings.Contains(err.Error(), "want an absolute path") {
		t.Fatalf("err = %v", err)
	}
}

func TestOnlyARepositoryOnThisDisksHasALocalPath(t *testing.T) {
	tests := []struct {
		url   string
		path  string
		local bool
	}{
		{"/srv/repo", "/srv/repo", true},
		{"local:/srv/repo/", "/srv/repo", true},
		{"s3:https://h/b", "", false},
		{"sftp:u@h:/srv/repo", "", false},
		{"rest:https://h/", "", false},
	}
	for _, tt := range tests {
		path, local := config.Repository{URL: tt.url}.LocalPath()
		if path != tt.path || local != tt.local {
			t.Errorf("LocalPath(%q) = %q, %v, want %q, %v", tt.url, path, local, tt.path, tt.local)
		}
	}
}

// Scenario: Без настройки доля таблицы блокировок — 0.5.
func TestWithoutALockShareItIsAHalf(t *testing.T) {
	c, err := config.Parse([]byte(restoreBase))
	if err != nil || c.PostgresqlLockShare() != 0.5 {
		t.Fatalf("PostgresqlLockShare() = %v, %v", c.PostgresqlLockShare(), err)
	}
}

func TestTheLockShareOfTheConfigIsKept(t *testing.T) {
	for body, want := range map[string]float64{"0": 0, "1": 1, "0.25": 0.25, "1.0": 1} {
		c, err := config.Parse([]byte(restoreBase + "restore:\n  postgresql_lock_share: " + body + "\n"))
		if err != nil || c.PostgresqlLockShare() != want {
			t.Errorf("%s: PostgresqlLockShare() = %v, %v, want %v", body, c.PostgresqlLockShare(), err, want)
		}
	}
}

// Scenario: Недопустимая доля таблицы блокировок останавливает старт агента.
func TestAnInvalidLockShareStopsTheAgent(t *testing.T) {
	for _, value := range []string{"-0.1", "1.5", `"half"`, ".nan", ".inf"} {
		t.Run(value, func(t *testing.T) {
			_, err := config.Parse([]byte(restoreBase + "restore:\n  postgresql_lock_share: " + value + "\n"))
			if err == nil || !strings.Contains(err.Error(), "restore.postgresql_lock_share") {
				t.Fatalf("err = %v, want one naming restore.postgresql_lock_share", err)
			}
		})
	}
}

func TestAFragmentMaySetTheLockShare(t *testing.T) {
	h := newOverlayHost(t, mainYAML)
	h.write("agent.d/restore.yaml", "restore:\n  postgresql_lock_share: 0.2\n")
	cfg, err := config.Load(h.main)
	if err != nil || cfg.PostgresqlLockShare() != 0.2 {
		t.Fatalf("PostgresqlLockShare() = %v, %v", cfg.PostgresqlLockShare(), err)
	}
}

func TestAFragmentThatSetsNoLockShareKeepsThatOfTheMainConfig(t *testing.T) {
	h := newOverlayHost(t, mainYAML+"restore:\n  postgresql_lock_share: 0.3\n")
	h.write("agent.d/restore.yaml", "restore:\n  dir: /srv/restore\n")
	cfg, err := config.Load(h.main)
	if err != nil || cfg.PostgresqlLockShare() != 0.3 {
		t.Fatalf("PostgresqlLockShare() = %v, %v", cfg.PostgresqlLockShare(), err)
	}
}

func TestTheLockShareCannotBeSetTwice(t *testing.T) {
	h := newOverlayHost(t, mainYAML+"restore:\n  postgresql_lock_share: 0.3\n")
	h.write("agent.d/restore.yaml", "restore:\n  postgresql_lock_share: 0.4\n")
	_, err := config.Load(h.main)
	var dup *config.DuplicateNameError
	if !errors.As(err, &dup) || dup.Name != "restore.postgresql_lock_share" {
		t.Fatalf("err = %v", err)
	}
}

func TestAnInvalidLockShareOfAFragmentNamesTheSettingAndTheFile(t *testing.T) {
	h := newOverlayHost(t, mainYAML)
	path := h.write("agent.d/restore.yaml", "restore:\n  postgresql_lock_share: 2\n")
	_, err := config.Load(h.main)
	if err == nil || !strings.Contains(err.Error(), "restore.postgresql_lock_share") || !strings.Contains(err.Error(), path) {
		t.Fatalf("err = %v", err)
	}
}
