<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright 2026 Artur Abalov -->

# 2026-10-21 — V3: ключи restic, срез 1 из 3 (контракт и агент) (coder)

Спецификации: `docs/specs/proto/restic-keys.feature`, `docs/specs/agent/restic-keys.feature`
(утверждены владельцем вместе с К1 и К2); ADR `docs/adr/0066-restic-keys.md`.
Срезы 2 (сервер) и 3 (консоль) не начинались.

## Сделано

- **proto.** `RepositoryInfo.recovery_key = 5` (`RecoveryKeyState`: `UNSPECIFIED`,
  `PRESENT`, `ABSENT`); `make proto`, сгенерированный Go-код обновлён. Тесты
  разбора на сгенерированных типах — `agent/internal/app/recovery_key_wire_test.go`.
- **Register (К21).** `app.Agent.RecoveryKey` и `Logger`; `recoveryKeyReader`
  (`restic key list --json --no-lock`, предел 30 секунд) считает признак заново
  при каждом Register; сбой — `UNSPECIFIED` и строка WARN без секретов.
- **restic.** `CLI.Keys`, `AddKey` (пароль по унаследованному дескриптору),
  `RemoveKey`; `Key` читает время создания в формате самого restic.
- **`internal/restickeys`** — решения: тег в поле пользователя, назначение и
  метка, поиск существующего ключа, предупреждения, id по префиксу, пароль.
- **Команды.** `repo key add|list|remove`; ключ восстановления в `repo add` и
  `repo init` (`--recovery-key-file`, `--no-recovery-key`); `repository.keys` в
  `repo check` и `check`; верификатор (`VERIFIER_READ_ONLY`, обратная проверка
  блокировки, `REPOSITORY_NOT_INITIALIZED`, подсказка про роль);
  `hostsetup.IsTerminal`, `RecordKey`, `RecordRecoveryKeySkipped`;
  `config.StripPassword`; `repoconnect.Connector` (`BeforeCreate`, `Created`,
  `Verifier`, `LockHint`), `repoinit.CreateGated`.
- **Документация.** `docs/operations/agent-host-setup.md` (раздел «Ключи restic»,
  ключ восстановления в `repo add`, коды), `docs/operator/09-troubleshooting.md`
  (`repository.keys`), справка команд, ADR 0066. Новых зависимостей нет.
- **Интеграционные тесты** (`-tags integration`, настоящий restic 0.19.1):
  `keys_integration_test.go` — выпущенный ключ открывает репозиторий, удалённый
  не открывает.

## Находки при работе

- `restic key list --json` печатает `created` как `2026-10-21 12:09:21` (местное
  время хоста без пояса), а не RFC 3339: разбор по поясу хоста
  (`restic.ParseKeyTime`), вывод агента — RFC 3339 UTC.
- Уже существовавшие тесты потребовали правки: новый репозиторий теперь
  выпускает ключ (строка аудита, вывод, шаг `repository.keys` в списках
  проверок); по умолчанию в тестах stdout — терминал (соглашение спецификации).

## Расхождения со спецификацией (для решения владельца)

1. Соглашения называют ключи base по умолчанию «H и R», но сценарии
   `repo key add base --for recovery` (успех) и «Второй ключ восстановления
   без метки отклоняется» (с R) несовместимы с этим умолчанием. Тесты выпуска
   берут репозиторий с одним ключом H.
2. К15: «ключ выпускается до файла пароля» — файл пароля репозитория должен
   существовать до `restic init`, поэтому ключ выпускается сразу после `init`,
   до drop-in и фрагмента (это проверяемое свойство сценария).
3. К15: решение о выводе ключа принимается, когда известно, что репозитория нет
   (иначе повтор `repo add` существующего репозитория без флагов ломал бы
   Ansible); для локального пути без файла `config` — до создания каталога,
   чтобы «хост не изменён» выполнялось.
4. К16: «<n> keys» при n = 1 печатается «1 key».
5. Не сделано: сценарий `@vm` (SELinux, ручная QA), ручные части `@qa`.

## Для следующих срезов

- Сервер и консоль: чтение `recovery_key` (в том числе `UNSPECIFIED`, не равное
  `ABSENT`), баннер, карточка верификатора.
- Команды, которые сервер печатает оператору (`InstallCommands`,
  `RepositoryCommands` самобэкапа, F4b), вызывают `repo add` нового репозитория:
  им нужен терминал либо `--recovery-key-file`/`--no-recovery-key` (В2).

## Проверка

- `./scripts/gate.sh agent fast` — PASSED: покрытие 98.6 %, CRAP <= 6, gocyclo,
  интеграционные тесты с настоящим restic, сборка стенда (тег e2e).
- `./scripts/gate.sh proto fast`, `gen fast`, `sdk fast`, `cli fast` — PASSED;
  `make breaking-proto` и `make license-check` — без замечаний.
- Мутационный прогон агента (`./scripts/gate.sh agent`) не запускался.
