# Сессия 2026-09-30: плагин `files` агента (A6b), реализация (coder)

Ветка `claude/gifted-shannon-786uix`. Спецификация —
`docs/specs/agent/files-plugin.feature` (решения Ф1–Ф18), QA —
`docs/qa/files-plugin.md`, вопросы OQ-038, OQ-040…OQ-046.

## Что сделано
- **proto.** `StepProgress.files_processed = 6`, `files_total = 7`
  (аддитивно); `make proto`, сгенерированный код закоммичен, `buf breaking`
  проходит.
- **executor / pluginhost.** У `Reporter` появился `ProgressFiles`; счётчики
  файлов restic идут в UPLOADING вместе с байтами и под тем же ограничением
  частоты.
- **restic.** `--retry-lock 5m` у каждого `backup`; `--one-file-system`
  (`BackupRequest.OneFileSystem`, `sdk.Dump.OneFileSystem`); итог не теряется,
  если контекст отменён после выхода restic с кодом 0 (Ф12);
  `PartialError` считает различные пути и называет первые десять в кавычках
  Go (Ф1, Ф11); строка restic «`<путь> does not exist, skipping`» становится
  записью об ошибке (Ф15; на 0.19.1 restic завершается с кодом 3 без записи
  `error`, путь есть только в этой строке); фатальное сообщение restic —
  одной строкой и с путями, которых нет (Ф17: у restic в нём перевод строки);
  `ErrLocked` — «repository is locked by another process» (Ф10).
- **pluginhost.** Сбой restic или репозитория называет `repository_name`
  (Ф11); необязательный интерфейс `RestoreDeferred` — RESTORE с непустым
  `snapshot_id` завершается FAILED без restic и без каталога восстановления,
  пустой `snapshot_id` по-прежнему REJECTED (Ф5).
- **files.** `Prepare` (нормализация, повторы и вложенность — `ConfigError`;
  проверка только указанных путей, ссылка отклоняется, отмена во время
  ожидания ФС), `Dump` (нормализованные пути, `exclude`, `one_file_system`),
  схема с пределами, заголовками, описаниями, примерами, ссылкой на exclude
  и переводами `x-sard-i18n.ru`, ФС подставляется через `Plugin.FS`.
- **Документация.** `docs/plugins/files.md`, ADR 0028 (`x-sard-i18n`).
- **Тесты.** `agent/plugins/files/*_test.go` — сценарии `@unit`, `@register`,
  `@doc` через настоящий исполнитель, адаптер, обёртку restic и фейковые
  `restic.Executor` и ФС; `agent/internal/pluginhost/files_integration_test.go`
  — `@restic` (тег `integration`, restic 0.19.1).

## Решения кодера (не в спецификации)
- Как плагин сообщает «restore не реализован»: необязательный интерфейс
  `pluginhost.RestoreDeferred` (внутренний, не в SDK); третья сторона его
  не получает.
- `sdk.Dump.OneFileSystem bool` — аддитивное поле Apache-2.0 SDK.
- Сообщения: `paths that cannot be backed up (N)[, first 10]: "p": reason; …`,
  restic: `at least one source file could not be read: unreadable paths (N)[,
  first 10]: "p", …`.
- Формат нарушает и общий `PartialError` для всех плагинов, не только files.
- Специальные файлы (сокет, устройство) при проверке не открываются: `open`
  на FIFO блокирует.

## Не покрыто, к сведению
- `@restic`: нечитаемый файл (среда работает от root, права 0000 не мешают),
  ожидание блокировки другим процессом, отмена и таймаут на большом дереве
  (загрузка > 5 с) — нет интеграционных тестов; поведение покрыто `@unit`
  на выводе restic 0.19.1 (`testdata/`).
- Сообщение об ошибке шаблона exclude и о недоступном хранилище получает
  приставку `repository "R": …`, так как код выхода 1 не различает причины.

## Правки по ревью архитектора
- ADR 0029 фиксирует решения A6b: `RestoreDeferred`, `Dump.OneFileSystem`,
  `--retry-lock 5m`, Ф12, разбор строки «does not exist, skipping».
- `OneFileSystem` вместе с `Filename` теперь падает (`ErrInvalidRequest`);
  такая ошибка не получает приставку `repository "R":`.
- `agent/plugins/imports_test.go` проверяет, что встроенные плагины зависят
  только от SDK, а реестр плагинов подключает только `cmd/sard-agent`.
