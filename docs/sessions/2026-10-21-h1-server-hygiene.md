<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright 2026 Artur Abalov -->

# 2026-10-21 — H1: гигиена сервера (сроки хранения и метрики Prometheus)

Ветка `ccr-5dea5d74-xsgws1`: её назначила сессия. В задаче указана
`feat/h1-server-hygiene`, переименовать можно по решению владельца.

## Фаза 1 — исследование

«Проверил» — видел в коде или документах (по отчётам двух read-only
агентов). «Полагаю» — не проверено.

### Хранение (проверил)

- `ON DELETE` в миграциях нет, все внешние ключи `NO ACTION`. Удалять
  можно только снизу вверх (`step_logs` → доставки, журнал расписания,
  задания и записи V4 → `run_steps` → `runs`).
- ADR 0013, правило 3, называет `runs`, `run_steps`, `snapshots`,
  `restore_verifications` доказательствами для аудита. H1 это правило
  меняет, нужен ADR.
- `step_logs` секционирована по месяцам (`step_logs_YYYY_MM`), на неё нет
  ссылок, секцию можно удалить `DROP`. Сейчас удаления нет
  (`StepLogPartitions`, KDoc «not done yet»).
- R2 не смотрит на статус запуска: `Snapshots.ofSource` соединяет
  `snapshots` с `run_steps` ради `runId`. Строка в `snapshots` бывает у
  неуспешного бэкапа (частичный снимок) и у потерянного шага (поздний
  результат). У запуска статуса `lost` нет, он только у шага.
- Планировщик уведомлений ищет события за окно `sard.notify.ttl`;
  доставку, удалённую внутри окна, он создаст заново.
- Блокировки в проекте: `FOR UPDATE SKIP LOCKED`; единственный
  advisory-lock — `DumpRole`.
- Читатели «последнего успеха»: свежесть, самобэкап F6, `lastVerifiedAt`,
  `overdue_episodes.closed_by_run_id`, уведомление «восстановился».

### Наблюдаемость (проверил)

- Micrometer есть, Prometheus-реестра нет; `/actuator/*` идёт мимо
  аутентификации, наружу открыты только `health` и `info` (спека Р11).
- `management.server.port` переносит весь actuator, включая `health`.
- `RunFinished` не несёт `kind` и `startedAt`.
- Срока CA публичным методом `CertificateAuthority` не отдаёт.

### Решения владельца (контрольная точка 1)

1. ADR 0013, правило 3, отменяется для `runs`, `run_steps`,
   `restore_verifications`; `snapshots` не трогаем (L1).
2. Запуск, у которого есть строка в `snapshots`, не удаляется никогда;
   последний успешный запуск источника не удаляется.
3. Сроки: всё, что нужно для аудита (запуски всех видов и итогов,
   записи проверок V4, журнал расписания) — 180 дней, не 90; логи шагов
   и доставки уведомлений — 30 дней. Последняя успешная и последняя
   неуспешная проверка источника хранятся всегда.
4. Метрики — на отдельном порту отдельным слушателем; `health` и `info`
   остаются на основном порту.
5. Защита метрик — обязательный токен при открытии наружу.
6. В `RunFinished` добавляются `kind` и `startedAt`; состояние считается
   gauge-запросами по тику с кэшем.
7. Сроки глобальные, не по тенантам.
8. Уборка — собственный `SmartLifecycle`-цикл, пачки, `SKIP LOCKED`, без
   advisory-lock.
9. При старте проверяется, что срок доставок больше `sard.notify.ttl`;
   запись V4, на которую ссылается живой запуск или открытый эпизод,
   ждёт своего срока.
10. Имена метрик в одном классе, `sard.*` сохраняются, метка источника —
    ID.

### Требование к L1

Запуск удаляется (или помечается), когда L1 забыл его снимок.
`snapshots.forgotten_at` пока не ставит никто.

## Фаза 2 — сроки хранения и фоновая уборка

Конвейер `/ship-feature`: specifier → coder → cleaner → architect (три
обзора) → hardener. Ветка `ccr-5dea5d74-xsgws1`, HEAD `f4ddf4c`.

### Что сделано

- Пакет `retention` в сервере: `RetentionLoop` (раз в час, первый проход
  через интервал), `RetentionPass` (пачки по 500, `FOR UPDATE SKIP
  LOCKED`, без advisory-lock, всё подходящее за проход), `StepLogRetention`
  (`DROP` месячных секций `step_logs`, `lock_timeout` 2 с), `RetentionSql`,
  `RetentionSettings` с проверкой при старте, миграция
  `V202610211200__retention_indexes.sql`, блок `sard.retention` в
  `application.yaml`.
- Сроки: логи шагов и доставки 30 дней; запуски, записи V4 и журнал
  расписания 180 дней (решение владельца: всё для аудита — полгода).
- `runs`, `schedule-fires` и `notification-deliveries` строго больше
  `sard.notify.ttl` (проверка при старте).
- Счётчик `sard.retention.deleted{kind}`; одна строка итога на проход.
- Спецификация `docs/specs/server/server-retention.feature`, QA
  `docs/qa/server-retention.md`, черновик
  `docs/adr/00XX-draft-server-retention.md`; правки ADR 0013, 0041, 0042,
  OQ-048, OQ-071.

### Проверено / не проверено

- Проверил: CRAP функций retention ≤ 6.0 (`.bin/crap` по JaCoCo); в
  результатах нет `SURVIVED`; diff только в ожидаемых файлах;
  `build.gradle.kts` не менялся.
- По отчётам агентов (логи, не мой прогон): `gate.sh server fast` и
  полный `gate.sh server` с мутациями — exit 0, покрытие 96.7%, мутантов
  выживших 0.
- Не воспроизведено мной: полный `:server:test` в этой среде (16 ГБ)
  падает по памяти (SIGKILL воркера, `java.io.EOFException`) даже на
  чистой `origin/main`: Testcontainers держит контейнер Postgres на каждый
  кэшированный Spring-контекст. Решение — отдельная задача «один
  контейнер на JVM» (C).
- F2: запись лога во время ожидающего `DROP` ждала 1994 мс при
  `lock_timeout` 2 с и не падала (один прогон); вариант (a)
  `DETACH … CONCURRENTLY` не понадобился.

### Известные ограничения

- R2: `run_steps` удаляются без `SKIP LOCKED` — поздний результат шага
  может отложить уборку тенанта на один проход.
- R3: индексы миграции строятся без `CONCURRENTLY`.
- В9: поздний результат потерянного шага после удаления его запуска
  подтверждается, но снимок не записывается.
- Ветка отстаёт от `origin/main` на 9 коммитов (слияние не делалось).

## Фаза 3 — метрики Prometheus и документация оператора (не закрыта)

Конвейер `/ship-feature`: specifier (спека одобрена владельцем,
ОВ1–ОВ10 по рекомендациям) → coder (части 1 и 2, исправление `StateLoop`,
разделение пакета) → cleaner → architect (два обзора, второй
`STRUCTURE ACCEPTED`) → hardener (остановлен владельцем по токенам).
Ветка `ccr-5dea5d74-xsgws1`, HEAD `a4bf276`, всё запушено.

### Что сделано

- Отдельный слушатель метрик (JDK `HttpServer`) на порту 8081, по
  умолчанию `127.0.0.1`, в compose не публикуется; вне loopback старт
  отвергается без токена `SARD_METRICS_TOKEN` (≥32 символов), 401 без
  тела, сравнение за постоянное время. `/actuator/health` и `/info`
  остаются на 8080, метрик на 8080 нет.
- Пакет `metrics` — чистое ядро (имена в `SardMetricNames`, гистограмма
  длительности, gauge состояния по тику с кэшем), адаптеры во фреймворк —
  в `prometheus` (причина: `scripts/claude/arch-edges.sh`, домен без
  фреймворка). 12-й вызов `system` в ADR 0013.
- `RunFinished` получил `kind` и `startedAt`; `CertificateAuthority.notAfter()`.
- Документация: `docs/operator/14-monitoring.md`, `15-data-retention.md`,
  правки README, `10-security.md`, `03-configuration.md`, `.env.example`;
  ADR-черновики `00XX-draft-metrics-prometheus.md` и
  `00XX-draft-server-retention.md`; тест `MonitoringDocsTest`.
- Спека `docs/specs/server/server-metrics.feature`, QA
  `docs/qa/server-metrics.md`, e2e `MetricsRetentionTest`.

### Подтверждено / не подтверждено

- Проверил сам: `arch-edges.sh` — `domain: clean`, exit 0; CRAP
  затронутых функций ≤ 6.0 (`.bin/crap` по JaCoCo после правок); в
  `build.gradle.kts` только зависимость `micrometer-registry-prometheus`.
- По отчётам агентов (не мой прогон): `gate.sh server fast` — exit 0
  после каждого круга coder'а.
- Не сделано: полный `gate.sh server` с мутациями для фазы 3. Hardener
  успел закоммитить чекпоинты (`0bce6c2` слушатель и `StateQueries`
  под mutflow, `4584f8d` порты тестов, `a4bf276` выжившие в
  `SelfBackupIntegrationTest` и `SelfAgentWiringIntegrationTest`), итоговые
  числа выживших не получены.
- Не прогонялось: e2e `MetricsRetentionTest` (нет образов
  `sard-*:e2e`), `promtool`, `markdownlint-cli2`, `lychee`; модуль
  `jdk.httpserver` в `eclipse-temurin:25-jre` не проверен.
- Среда: полный `:server:test` на 16 ГБ упирается в память (контейнер
  Postgres на каждый кэшированный Spring-контекст), см. фазу 2; решение —
  отдельная задача «один контейнер на JVM» (C). 22 октября ~03:35 UTC
  виртуальная машина сессии была заменена платформой, незавершённый
  прогон мутаций потерян.

### Дальше

1. Довести hardener: выжившие до/после по `metrics` и `prometheus`,
   полный `./scripts/gate.sh server` (≈47 минут по опыту фазы 2).
2. Прогнать e2e (`make e2e-images` + `make e2e-test`), `promtool check
   rules` по примерам из `14-monitoring.md`, markdownlint и lychee.
3. Задача C (общий контейнер Postgres на JVM).
4. Переименовать черновики ADR в `00XX-draft-*` → реальные номера при
   слиянии; ветка отстаёт от `origin/main` на 9 коммитов.
