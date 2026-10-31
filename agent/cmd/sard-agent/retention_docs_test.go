// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"os"
	"strings"
	"testing"
)

// docs/specs/agent/retention-prune.feature, rule "Документация оператора
// описывает предел хоста и ручную очистку" (КПр8).

const (
	operatorDir   = "../../../docs/operator/"
	retentionDoc  = "18-snapshot-retention.md"
	manualPruneBy = "## Ручная очистка"
)

func operatorFile(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(operatorDir + name)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Join(strings.Fields(string(data)), " ") // Markdown wraps lines inside a sentence
}

func wantAll(t *testing.T, text, where string, phrases ...string) {
	t.Helper()
	for _, p := range phrases {
		if !strings.Contains(strings.ToLower(text), strings.ToLower(p)) { // a phrase may open a sentence
			t.Errorf("%s lacks %q", where, p)
		}
	}
}

func TestРазделОХраненииСнимковЕстьИСвязанСОглавлениемИСправочником(t *testing.T) {
	for _, name := range []string{"README.md", "03-configuration.md"} {
		wantAll(t, operatorFile(t, name), name, retentionDoc)
	}
	wantAll(t, operatorFile(t, "03-configuration.md"), "03-configuration.md", "## Агент: retention_floor")
}

func TestДокументацияНазываетВсеКлючиПределаИВстроенныеЗначения(t *testing.T) {
	doc := operatorFile(t, retentionDoc)
	wantAll(t, doc, retentionDoc,
		"retention_floor:", "default:", "repositories:",
		"keep_last", "keep_within_days", "disabled",
		"7 снимков", "14 дней",
		"`disabled` допустим только в `retention_floor.repositories.<имя>`",
		"ключ `retention_floor` во фрагменте `agent.d` и внутри определения репозитория — ошибка конфигурации",
	)
}

func TestДокументацияГоворитЧтоПределДействуетНаХостеКоторыйУдаляет(t *testing.T) {
	wantAll(t, operatorFile(t, retentionDoc), retentionDoc,
		"задайте нижний предел на каждом хосте, учётка которого может удалять в репозитории",
		"предел хоста, сделавшего бэкап, на хосте, который удаляет, не действует",
	)
}

func TestДокументацияНазываетСтрокуЖурналаИМестоПределаВКонсоли(t *testing.T) {
	wantAll(t, operatorFile(t, retentionDoc), retentionDoc,
		"retention floor repository=", "keep_last=", "keep_within_days=", "origin=",
		"консоль показывает предел хоста в блоке хранения карточки источника",
	)
}

func TestДокументацияНазываетОграничениеВыпускаОдинАгентНаРепозиторий(t *testing.T) {
	wantAll(t, operatorFile(t, retentionDoc), retentionDoc,
		"забывание и очистка не выполняются, если репозиторий видят несколько агентов роли source",
		"назначение владельца обслуживания появится в следующем выпуске",
	)
}

func TestДокументацияРучнойОчисткиПредупреждаетОБлокировкеБэкапов(t *testing.T) {
	doc := operatorFile(t, retentionDoc)
	_, section, found := strings.Cut(doc, manualPruneBy)
	if !found {
		t.Fatalf("%s has no section %q", retentionDoc, manualPruneBy)
	}
	wantAll(t, section, "the manual prune section",
		"prune держит исключительную блокировку репозитория",
		"бэкап этого репозитория ждёт её до 5 минут и затем завершается ошибкой",
		"prune ничего не забывает и удаляет только данные без ссылок",
		"восстановление и проверка восстановления из этого хранилища, идущие во время prune, могут завершиться ошибкой, поэтому запускать prune стоит, когда их нет",
		"repository is locked", "storage account has no delete permission", "обновите агента",
	)
}

func TestДокументацияСамобэкапаЧестноНазываетСлабостьПределаНаХостеСервера(t *testing.T) {
	wantAll(t, operatorFile(t, retentionDoc), retentionDoc,
		"предел встроенного агента `sard-self` задаётся в его `agent.yaml` на машине сервера",
		"тот, кто захватил машину сервера, может этот предел изменить",
	)
}
