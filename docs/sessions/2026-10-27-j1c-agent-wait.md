<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright 2026 Artur Abalov -->

# J1c — ожидание офлайн-агента (coder)

Дата: 2026-10-27. Спецификация — `docs/specs/server/run-cancel-deadlines.feature` (сценарии `# J1c`), ADR —
черновик `docs/adr/00XX-draft-run-cancel-deadlines.md`, раздел «J1c». proto и агент не менялись.

## Что сделано
- Миграция `V202610271400__agent_wait.sql`: статус `agent_unavailable` у запуска и шага, `runs.agent_wait_until`,
  `runs.agent_unavailable_since`, частичный индекс queued-запусков со сроком.
- Срок ожидания считается при создании запуска и хранится: `Runs` (ручной — создание + `sard.run.agent-wait.max`; по
  расписанию и догоняющий — следующее срабатывание того же расписания, не дальше максимума, `Firing`), `Restores`
  (создание + максимум, закрывает OQ-233). Настройка `sard.run.agent-wait.max` (`SARD_RUN_AGENT_WAIT_MAX`, 24 ч), не
  длительность или `<= 0` — сервер не стартует (`AgentWaitProperties`).
- `AgentWaits.expire`: queued-шаг запуска с наступившим сроком и офлайн-агентом закрывается условным UPDATE шага и запуска
  (`agent_unavailable`, «the agent was not connected until the wait deadline», `agent_unavailable_since` — последняя связь из
  `agents.last_seen_at`), затем `RunFinished`. Отсрочка после старта для агента, не виденного с момента старта (ДJ9).
  Вызывается тиком диспетчера и, раньше выбора срабатываний, тиком планировщика (Р-J12).
- `RunOutcome.waiting_for_agent` (queued при офлайн-агенте, присутствие агента спрашивают только у queued-запуска) и
  `agent_unavailable`; `Run.agentWaitUntil`, `Run.agentUnavailableSince`.
- Уведомления: заголовок «не выполнен — агент был недоступен» (бэкап, восстановление; ru, en), строка о последней связи или
  «ещё ни разу не подключался»; запуск по расписанию уведомляет только первым в серии; «ошибок подряд» пропускает
  `agent_unavailable`.
- OpenAPI (`make openapi`), моки MSW (`agentWaitUntil`, `agentUnavailableSince`), словари `enum.RunStatus`, `RunOutcome`,
  `StepStatus` (ru, en), значок `pause` для `agent_unavailable` в `theme.ts` и `StatusIcon.tsx`. Экраны — J1e.
- e2e `AgentWaitTest` (агент остановлен, `SARD_RUN_AGENT_WAIT_MAX=1m`, расписание каждую минуту).
- `docs/operator/03-configuration.md`, `application.yaml`, `docs/open-questions.md` (OQ-233 закрыт, OQ-263 дополнен).

## Решения по ходу
1. Срок хранится в `runs`, а не в шаге: его показывает запуск, а истечение читает оба и закрывает оба в одной транзакции.
2. «Последняя связь» — `agents.last_seen_at` (пишется не чаще раза в heartbeat-интервал): отдельной таблицы сессий нет.
3. Отсрочка после старта — время создания бина `AgentWaits`; «агент не виден с момента старта» — `last_seen_at` пуст или раньше
   старта.
4. Серия «первая ошибка» по-прежнему смотрит на непосредственно предыдущий запуск; `agent_unavailable` пропускает только счёт
   «ошибок подряд» (так читается сценарий «Недоступный агент не входит в число ошибок подряд»).
5. Тесты планировщика, державшие активный запуск ожиданием офлайн-агента (`2 - an active run skips ...`, журнал срабатываний),
   переведены на `running` (`ScheduleTables.startAll`): после J1c такой запуск истекает на следующем срабатывании — это и есть
   сценарий Р-J12.
6. Значок `pause` для `agent_unavailable`: тест `theme.test.ts` требует свой значок у каждого значения `RunStatus` и `StepStatus`.

## Расхождения со спецификацией
- ДJ2 не говорит, что считать серией для `failed`, идущего после `agent_unavailable`: уведомляет как первый провал (предыдущий
  запуск не `failed`). Если владелец хочет, чтобы `agent_unavailable` был прозрачен и здесь, меняется одна строка `Telling.turn`.
- Сценарий «Отказ базы на тике ожидания»: «одна строка доставки» обеспечена тем, что доставки планируются по завершённым запускам
  (одна на запуск и канал), а не отдельным счётом; тест проверяет один `RunFinished` и повторный тик без закрытия.
- Сценарий «Агент подключился до срока — шаг уходит как обычно» отдельного теста не получил: код отправки не менялся, шаг
  остаётся `queued` до отправки.
- Запрос QA `docs/qa/run-cancel-deadlines.md` не дополнялся.

## Проверки
- `./scripts/gate.sh server fast`: `coverage: 96.8% (instructions)`, CRAP — ни одной функции выше 6, `gate: PASSED (server, fast)`.
- `./scripts/gate.sh web fast`: `gate: PASSED (web, fast)`.
- `make license-check`: `license-check: 1931 files OK`; `./gradlew :e2e:compileTestKotlin` — собрано.
- По дороге гейт ловил: detekt (длина строки, магическое число, `TooManyFunctions`, `LargeClass`), CRAP > 6 (`RunState.following`,
  `RunOutcome.of`, `Telling.turn`, два `outcome` форматтеров), `ArchitectureTest` (`runs` не знает `agents`: присутствие агента —
  `AgentPresence` в `runs`, реализация в `agents/dispatch`; новый вызов `sessions.system` — ADR 0013: чтение истёкших ожиданий
  лежит в `StepDeadlines.waitsOver`), `ApiContractIntegrationTest` (новые значения перечислений), тесты расписания, державшие
  активный запуск ожиданием офлайн-агента.

## Открытое
- e2e `AgentWaitTest` не запускался (образы не собраны, OQ-263); компилируется (`:e2e:compileTestKotlin`).
- Мутационное тестирование не запускалось (`gate.sh server fast` его не включает).
