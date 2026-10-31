// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package config_test

import (
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/config"
)

// docs/specs/agent/retention-forget.feature, rule "Нижний предел задаётся
// только оператором хоста в основном конфиге" (L1a).

const floorBase = restoreBase + "repositories:\n  - {name: R, url: /srv/r, password_file: /p}\n  - {name: Q, url: /srv/q, password_file: /p}\n"

func floorOf(t *testing.T, body, repo string) config.Floor {
	t.Helper()
	c, err := config.Parse([]byte(floorBase + body))
	if err != nil {
		t.Fatal(err)
	}
	return c.FloorOf(repo)
}

func TestБезНастройкиДействуетВстроенныйПредел7Снимкови14Дней(t *testing.T) {
	want := config.Floor{KeepLast: 7, KeepWithinDays: 14, Origin: config.FloorBuiltIn}
	if got := floorOf(t, "", "R"); got != want {
		t.Errorf("floor = %+v, want %+v", got, want)
	}
}

func TestПределПоУмолчаниюИзКонфигаДействуетНаВсеРепозиторииХоста(t *testing.T) {
	body := "retention_floor:\n  default: {keep_last: 10, keep_within_days: 30}\n"
	want := config.Floor{KeepLast: 10, KeepWithinDays: 30, Origin: config.FloorConfigDefault}
	for _, repo := range []string{"R", "Q"} {
		if got := floorOf(t, body, repo); got != want {
			t.Errorf("%s: floor = %+v, want %+v", repo, got, want)
		}
	}
}

func TestПределРепозиторияЗаменяетТолькоНазванныеВНёмПоля(t *testing.T) {
	body := "retention_floor:\n  default: {keep_last: 10, keep_within_days: 30}\n  repositories:\n    R: {keep_within_days: 60}\n"
	want := config.Floor{KeepLast: 10, KeepWithinDays: 60, Origin: config.FloorConfigRepository}
	if got := floorOf(t, body, "R"); got != want {
		t.Errorf("floor = %+v, want %+v", got, want)
	}
	if got := floorOf(t, body, "Q"); got.Origin != config.FloorConfigDefault || got.KeepWithinDays != 30 {
		t.Errorf("Q: floor = %+v", got)
	}
}

func TestПолеБезЗначенияВDefaultБерётсяИзВстроенногоПредела(t *testing.T) {
	want := config.Floor{KeepLast: 3, KeepWithinDays: 14, Origin: config.FloorConfigDefault}
	if got := floorOf(t, "retention_floor:\n  default: {keep_last: 3}\n", "R"); got != want {
		t.Errorf("floor = %+v, want %+v", got, want)
	}
}

func TestПределВыключаетсяТолькоЯвнойНастройкойDisabled(t *testing.T) {
	body := "retention_floor:\n  repositories:\n    R: {disabled: true}\n"
	want := config.Floor{Disabled: true, Origin: config.FloorConfigRepository}
	if got := floorOf(t, body, "R"); got != want {
		t.Errorf("floor = %+v, want %+v", got, want)
	}
	if got := floorOf(t, body, "Q"); got.Disabled {
		t.Errorf("Q: floor = %+v", got)
	}
}

func TestDisabledFalseКакОтсутствие(t *testing.T) {
	body := "retention_floor:\n  repositories:\n    R: {disabled: false, keep_last: 5}\n"
	want := config.Floor{KeepLast: 5, KeepWithinDays: 14, Origin: config.FloorConfigRepository}
	if got := floorOf(t, body, "R"); got != want {
		t.Errorf("floor = %+v, want %+v", got, want)
	}
}

func TestНедопустимыйПределОстанавливаетАгентаСИменемКлюча(t *testing.T) {
	for name, c := range map[string]struct{ setting, key string }{
		"оба нуля":             {"R: {keep_last: 0, keep_within_days: 0}", "retention_floor.repositories.R"},
		"disabled и keep_last": {"R: {disabled: true, keep_last: 5}", "retention_floor.repositories.R"},
		"отрицательное":        {"R: {keep_last: -1}", "retention_floor.repositories.R.keep_last"},
		"дробное":              {"R: {keep_within_days: 1.5}", "retention_floor.repositories.R.keep_within_days"},
		"строка":               {"R: {keep_within_days: 14d}", "retention_floor.repositories.R.keep_within_days"},
		"неизвестный ключ":     {"R: {keep_daily: 7}", "retention_floor.repositories.R.keep_daily"},
		"disabled не булево":   {"R: {disabled: maybe}", "retention_floor.repositories.R.disabled"},
		"репозиторий не карта": {"R: 5", "retention_floor.repositories.R"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := config.Parse([]byte(floorBase + "retention_floor:\n  repositories:\n    " + c.setting + "\n"))
			if err == nil || !strings.Contains(err.Error(), c.key) {
				t.Fatalf("err = %v, want one that names %s", err, c.key)
			}
		})
	}
}

func TestНедопустимыеКлючиВерхнегоУровняРазделаПредела(t *testing.T) {
	for name, c := range map[string]struct{ body, key string }{
		"неизвестный ключ раздела": {"retention_floor:\n  keep_last: 3\n", "retention_floor.keep_last"},
		"раздел не карта":          {"retention_floor: 7\n", "retention_floor"},
		"disabled в default":       {"retention_floor:\n  default: {disabled: true}\n", "retention_floor.default.disabled"},
		"default не карта":         {"retention_floor:\n  default: 7\n", "retention_floor.default"},
		"repositories не карта":    {"retention_floor:\n  repositories: [a]\n", "retention_floor.repositories"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := config.Parse([]byte(floorBase + c.body))
			if err == nil || !strings.Contains(err.Error(), c.key) {
				t.Fatalf("err = %v, want one that names %s", err, c.key)
			}
		})
	}
}

func TestНулевойПределБезDisabledПодсказываетЯвноеВыключение(t *testing.T) {
	_, err := config.Parse([]byte(floorBase + "retention_floor:\n  default: {keep_last: 0, keep_within_days: 0}\n"))
	if err == nil || !strings.Contains(err.Error(), "disabled: true") || !strings.Contains(err.Error(), "retention_floor.default") {
		t.Fatalf("err = %v", err)
	}
}

func TestНулевойKeepLastРепозиторияСОстальнымиДнямиИзDefaultДопустим(t *testing.T) {
	body := "retention_floor:\n  repositories:\n    R: {keep_last: 0}\n"
	want := config.Floor{KeepLast: 0, KeepWithinDays: 14, Origin: config.FloorConfigRepository}
	if got := floorOf(t, body, "R"); got != want {
		t.Errorf("floor = %+v, want %+v", got, want)
	}
}

func TestПределВнутриОпределенияРепозиторияВОсновномКонфигеОстанавливаетАгента(t *testing.T) {
	_, err := config.Parse([]byte(restoreBase + "repositories:\n  - {name: R, url: /srv/r, password_file: /p, retention_floor: {keep_last: 1}}\n"))
	if err == nil || !strings.Contains(err.Error(), "retention_floor") || !strings.Contains(err.Error(), "retention_floor.repositories") {
		t.Fatalf("err = %v", err)
	}
}

func TestПределВоФрагментеAgentDОстанавливаетАгентаСИменемФайла(t *testing.T) {
	h := newOverlayHost(t, floorBase)
	file := h.write("agent.d/repo-R.yaml", "retention_floor:\n  default:\n    keep_last: 1\n")
	_, err := config.Load(h.main)
	if err == nil || !strings.Contains(err.Error(), file) || !strings.Contains(err.Error(), "retention_floor") {
		t.Fatalf("err = %v", err)
	}
}

func TestПределВнутриОпределенияРепозиторияВоФрагментеОстанавливаетАгента(t *testing.T) {
	h := newOverlayHost(t, restoreBase)
	file := h.write("agent.d/repo-R.yaml", "repositories:\n  - {name: R, url: /srv/r, password_file: /p, retention_floor: {keep_last: 1}}\n")
	_, err := config.Load(h.main)
	if err == nil || !strings.Contains(err.Error(), file) || !strings.Contains(err.Error(), "retention_floor") {
		t.Fatalf("err = %v", err)
	}
}

func TestПределДляНеизвестногоРепозиторияНеОшибкаАИмяВидноДляПредупреждения(t *testing.T) {
	c, err := config.Parse([]byte(floorBase + "retention_floor:\n  repositories:\n    old: {keep_last: 30}\n    R: {keep_last: 2}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got := c.UnknownFloorRepositories(); len(got) != 1 || got[0] != "old" {
		t.Errorf("unknown = %v", got)
	}
}
