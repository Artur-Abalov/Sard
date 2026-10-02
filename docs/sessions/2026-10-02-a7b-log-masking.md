# Сессия 2026-10-02: маскирование секретов в логах шагов (A7b)

Ветка `claude/setup-readme-english-wjqujq`. Спецификация утверждена владельцем:
`docs/specs/agent/step-log-masking.feature`, `docs/specs/server/step-log-masking.feature`,
решения OQ-072…OQ-086 (приняты все рекомендации), OQ-087 — дыра, которую A7b
обязана закрыть.

## Роль: coder

### Что сделано (дельта Д1–Д5 и OQ-085)

| Дельта | Что | Где |
|---|---|---|
| Д1 base64 | стандартный и URL-алфавит; три сдвига; образцы «только знаки значения», «до конца текста без `=` и с `=`»; образцы короче 4 знаков не берутся | `agent/internal/redact/base64.go`, подключено в `patternsOf` (`redact.go`) |
| Д2 порог | `redact.IsShort` (меньше 4 кодовых точек; невалидный UTF-8 — байты); `ShortValueLen = 4`; `OnShortValue` удалён | `agent/internal/redact/redact.go` |
| Д2 предупреждение | при старте `Source.Audit` читает все файлы и пишет WARN на короткие значения и нечитаемые файлы; на шагах `Source.For` предупреждает только при переходе «не короткое → короткое»; исполнитель больше не предупреждает | `agent/internal/stepsecrets/source.go`, вызов в `serve` (`agent/cmd/sard-agent/main.go`) |
| Д3 паника (OQ-087) | значение паники и стек проходят `Set.Mask` до `Logger.Error` | `agent/internal/executor/command.go` |
| Д4 документ | `docs/operations/step-log-masking.md` (что маскируется и что нет) | тест `agent/internal/stepsecrets/doc_test.go` |
| Д5 состав | `env_file` всех репозиториев, `tls.key_file`, пароль URL репозитория шага | `stepsecrets.Source.gather` |
| OQ-085 | `Host.Secret` внутри шага отдаёт содержимое из того же чтения, что и маскировщик | `executor.Secret.Ref/Content`, `Reporter.Secret`, `pluginhost.host.Secret` |

ADR 0033 дополнен разделом «Изменения A7b» (новый ADR не нужен: решения
продолжают A7c). `docs/open-questions.md`: OQ-087 перенесён в закрытые.

### Решения coder, о которых стоит знать
- Нечитаемый `env_file` **любого** репозитория (не только репозитория шага)
  и нечитаемый `tls.key_file` валят шаг (как нечитаемый секрет): молча не
  маскировать нельзя. Спецификация явно называет только репозиторий шага.
- Образцы base64 короче 4 знаков не используются (для значения из 1–2 байт
  это знаки, совпадающие со случайным текстом).
- Предупреждение держит `Source`, а не исполнитель: `Audit` и `For` делят
  одно состояние «значение сейчас короткое». Интерфейс `executor.Secrets`
  не менялся; `executor.Secret` и `Reporter` получили аддитивные поля и метод
  для OQ-085.

### Сценарии и тесты

| Сценарии спецификации | Тесты |
|---|---|
| маскирование в выводе restic, строках плагина, результате, detail; разрыв между порциями; пустой секрет; строки без секретов; порядок; параллельные шаги; PEM; длинный секрет; одна строка PEM не маскируется; маркер | `agent/internal/executor/redaction_test.go` (A7c и новые), `agent/internal/redact/*_test.go` |
| виды base64: с выравниванием и без, маркер поглощает `=`, любой сдвиг, любой разрыв, PEM в base64 | `agent/internal/redact/base64_test.go`; во всех путях текста — `TestBase64OfASecretIsMaskedOnEveryPathOfAStepsText` |
| @restic base64 в ошибке настоящего restic | `TestBase64OfASecretInResticStderrIsMasked` (`agent/internal/pluginhost/redaction_integration_test.go`, тег integration) |
| набор значений: все `secrets:`, env_file всех репозиториев, tls.key_file, пароль URL, пароль репозитория не читается, замена файла видна со следующего шага | `agent/internal/stepsecrets/source_test.go` |
| OQ-085 | `TestTheHandlerGetsTheContentTheMaskerWasBuiltFrom` (executor), `TestHostGivesTheValueTheStepStartedWithEvenIfTheFileChanged` (pluginhost), `TestAConfiguredSecretCarriesItsNameAndTheFileContentForThePlugin` |
| короткие значения: маскируются; порог 4 знака (abc/abcd/пар/паро); имя без значения; env_file — репозиторий и переменная; не повторяется на шагах; ставшее коротким — один раз; нечитаемый файл при старте | `stepsecrets/source_test.go`; @start — `agent/cmd/sard-agent/masking_start_test.go` (run с недоступным сервером, как `TestValidConfigDialsTheServerUntilStopped`; фейкового сервера по TLS не строил) |
| паника в журнале агента, значения не в журнале на всех путях, сохранённый результат | `TestAPanicValueIsMaskedInTheAgentLogToo`, `TestAStepThatPrintsSecretsOnEveryPathLeavesNoValueInTheAgentLog`, `TestTheResultMessageIsMaskedBeforeItIsSentOrStored` |
| @doc | `agent/internal/stepsecrets/doc_test.go` |
| @e2e | `test/e2e/.../StepLogRedactionTest.kt` расширен (путь репозитория содержит секрет как есть и в base64); **не запускался**: в среде нет Docker |
| сервер (три сценария) | `RunsGrpcApiIntegrationTest.kt`: `Строка с маркером хранится и отдаётся без изменений`, `Строка, похожая на секрет, хранится без изменений`, `Сообщение результата шага хранится без изменений`; компилируются, `spotlessCheck` и `detekt` проходят, **не запускались** (на момент написания Docker не было; прогнано ниже) |

Сценарии «Агент сообщает серверу имена секретов, но не значения» и «Неверный
файл окружения» проверены существующими тестами A1/A7c, новых тестов не потребовали
(RegisterRequest не менялся).

### Результат ворот (как напечатали инструменты)
- `LC_ALL=C.UTF-8 ./scripts/gate.sh agent fast`: `gate: PASSED (agent, fast)`;
  `coverage: 98.1%`; `0 issues.` (golangci-lint); максимум CRAP в таблице —
  `6.0` (`Executor.compile` CC 6, покрытие 100.0%); интеграционные тесты с restic 0.19.1
  `ok` (pluginhost, restic, testplugin, cmd/sard-agent).
- `./scripts/gate.sh sdk fast`: `gate: PASSED (sdk, fast)` (менялись только комментарии `Host`).
- `make license-check`: `license-check: 593 files OK`.
- `./gradlew :server:spotlessCheck :server:compileTestKotlin :server:detekt --offline`: без ошибок.
  `./scripts/gate.sh server fast` не запускался: тесты сервера требуют Docker.
- Мутационное тестирование — не в `fast`; следующий шаг — cleaner/hardener.

### Прогон после появления Docker
- `LC_ALL=C.UTF-8 ./scripts/gate.sh server fast`: `coverage: 96.2% (instructions)`,
  максимум CRAP в таблице `6.0`, `gate: PASSED (server, fast)`. Три новых серверных теста вошли в прогон.
- `StepLogRedactionTest` (`./gradlew :e2e:test --tests '*StepLogRedactionTest*'`):
  `a secret restic prints reaches step_logs as REDACTED() PASSED`, `BUILD SUCCESSFUL`.
  Образы собраны вручную по шагам `make e2e-images`: сборка server-образа внутри Docker
  упирается в 429 Maven Central (ограничение среды), поэтому jar собран на хосте
  (`./gradlew :server:bootJar --offline`) и положен в runtime-стадию того же Dockerfile;
  агент — `scripts/package-agent.sh` и `test/e2e/agent/Dockerfile` как в Makefile.

### Осталось
- Мутационный прогон (`./scripts/gate.sh agent`) — hardener.

## Роль: specifier (уточнение после coder, OQ-088)

Владелец по решению coder о нечитаемых чужих файлах: «Делаем именно так и
фиксируем это — некритичные чужие файлы надо просто отметить
предупреждением». Спецификация дополнена: С9, Д6, правило «Нечитаемый
источник, от которого шаг не зависит, отмечается предупреждением и шаг не
валит» (12 сценариев `@oq-088`), строка в `@doc`-сценарии, явное «плагин шага
не ссылается на секрет tok» в сценарии нечитаемого секрета (поведение не
менялось). QA — часть 3а, шаг 27. Реестр — OQ-088. Решение coder «нечитаемый
`env_file` любого репозитория и `tls.key_file` валят шаг» отменяется для
чужих источников; следующий шаг — coder.

## Роль: coder (OQ-088)

`stepsecrets.Source` разделяет сбои источников: нечитаемый файл `secrets:` и
`env_file` репозитория шага валят шаг, как раньше; нечитаемый или неверный
`env_file` другого репозитория и нечитаемый `tls.key_file` — одно предупреждение
(`Source.warnForeign`, состояние «сейчас сломан» под мьютексом, общее для
`Audit` и `For`), значения такого источника в набор шага не входят. Сломавшийся
снова источник предупреждается заново. Документ оператора и ADR 0033 обновлены.

| Сценарии @oq-088 | Тесты (`agent/internal/stepsecrets/source_test.go`) |
|---|---|
| нечитаемый env_file другого репозитория не валит шаг, значения не маскируются; предупреждение без пути | `TestAnUnreadableEnvFileOfAnotherRepositoryWarnsOnceWithoutThePathAndFailsNothing` |
| неверный env_file другого репозитория | `TestAnInvalidEnvFileOfAnotherRepositoryWarnsWithoutItsContent` |
| нечитаемый env_file репозитория шага (offsite) валит шаг | `TestAnUnreadableEnvFileOfTheStepRepositoryFailsTheStepWhateverItsName` |
| нечитаемый tls.key_file: не валит, одно предупреждение без пути | `TestAnUnreadableAgentKeyWarnsOnceWithoutThePathAndFailsNothing` |
| не повторяется на шагах | `TestTheWarningAboutAForeignFileIsNotRepeatedOnLaterSteps` |
| нечитаемый при старте | `TestAForeignFileUnreadableAtStartIsNotWarnedAboutAgainOnSteps` |
| параллельные шаги | `TestParallelStepsGiveOneWarningAboutAForeignFile` |
| снова читаемый — маскируется молча | `TestAForeignFileThatIsReadableAgainIsMaskedSilentlyFromTheNextStep` |
| снова нечитаемый — новое предупреждение | `TestAForeignFileThatBreaksAgainIsWarnedAboutAgain` |
| @doc «маскируется не всё» | `doc_test.go: TestTheDocumentationSaysWhatIsNotMasked` |

«Лог шага содержит маркер на месте T» и «плагин вызывался» следуют из набора
значений: исполнитель не менялся, интерфейс `executor.Secrets` тоже.

### Ворота после OQ-088 (как напечатали инструменты)
- `LC_ALL=C.UTF-8 ./scripts/gate.sh agent fast`: `coverage: 98.1%`, `0 issues.`,
  `gate: PASSED (agent, fast)`; первый прогон показал CRAP 8.0 у `Source.gather`
  и 7.0 у `Source.For`, исправлено выделением `secretFiles` и `urlPassword`.
  Один из ранних прогонов упал на `TestDialTOFUClassifiesAContextDeadlineAsTemporary`
  (пакет `enroll`, окно 50 мс) под нагрузкой; отдельно и в повторном прогоне ворот зелёный.
- `LC_ALL=C.UTF-8 ./scripts/gate.sh server fast`: `gate: PASSED (server, fast)`
  (`coverage: 96.2% (instructions)`; `RunsGrpcApiIntegrationTest`: tests="17" failures="0").
  Первый прогон упал на `NoSuchFileException ... in-progress-results-generic.bin`
  (сбой Gradle, параллельно шла другая сборка); повтор зелёный.
- sdk не менялся.
- `StepLogRedactionTest` с агентом, собранным после OQ-088 (образ агента пересобран,
  образ сервера — из предыдущего прогона): `tests="1" failures="0" errors="0"`, `BUILD SUCCESSFUL`.
- `make license-check`: `license-check: 593 files OK`.

Следующий шаг: cleaner.

## cleaner

Рефакторинг не нужен: изменений кода нет. Метрики (`LC_ALL=C.UTF-8 ./scripts/crap.sh agent`),
до и после идентичны, так как код не менялся:

| Функция | CRAP до | CRAP после |
|---|---|---|
| максимум по модулю agent (`resolveEnrollLocals`, `Config.validate`, `Executor.compile`, `Executor.check`, ...) | 6.0 (CC 6, 100.0%) | 6.0 (CC 6, 100.0%) |
| функций с CRAP > 6 | 0 | 0 |

Файлы A7b: `Executor.compile` (redaction.go) 6.0, остальные функции `redact`, `stepsecrets`
(`secretFiles`, `urlPassword` уже выделены в coder-этапе) ниже порога и в верхние строки списка не попали.
Размеры: `stepsecrets/source.go` 209 строк, `redact/*.go` без тестов 476 строк суммарно, дублирования,
требующего выделения, нет.

Ворота: `LC_ALL=C.UTF-8 ./scripts/gate.sh agent fast`: `coverage: 98.1%`, `0 issues.`,
`gate: PASSED (agent, fast)`. Server не менялся.

Следующий шаг: architect.

## hardener

Инструмент: `go-mutesting` (agent, sdk), mutflow (server). `LC_ALL=C.UTF-8`.

Первый прогон `./scripts/gate.sh agent` упёрся в лимит времени оболочки (мутационный этап идёт
больше часа), поэтому `go-mutesting ./...` запускался напрямую в `agent/` и в `agent/plugins/sdk`.

| Модуль | Выживших до | Выживших после | Счёт после (печатает ворота) |
|---|---|---|---|
| sdk | `The mutation score is 1.000000 (39 passed, 0 failed, 1 duplicated, 0 skipped, total is 39)`: 0 | 0 | `mutation score: 1.000000` |
| server (mutflow) | 0 (702 discovered, 702 killed, 0 survived, сумма по `build/test-results/test/*.xml`) | 0 | ворота прошли, код не менялся |
| agent | `The mutation score is 0.937023 (1964 passed, 132 failed, 127 duplicated, 0 skipped, total is 2096)` | 151 строка `FAIL` в выводе ворот | `mutation score: 0.927958` |

Рост числа `FAIL` в agent (132 -> 151) не регрессия: 23 новых строки относятся к `cmd/sard-agent`,
которым тесты (около 5.7 с) в первом прогоне, шедшем параллельно с gradle-воротами server,
не укладывались в `-timeout 10s` у go-mutesting, и все мутанты были "убиты" таймаутом. Повторный
прогон только `./cmd/...` без нагрузки: `The mutation score is 0.938503 (351 passed, 23 failed,
13 duplicated, 0 skipped, total is 374)`. Эти выжившие в коде до A7b (enroll, repo, main,
`shutdownTimeout`, `ref.Executor = exec`), мутанты `enroll/identity.go.9` и `enroll/trust.go.32`
в втором прогоне не выжили без изменений кода (зависят от нагрузки). Совет: гонять мутационный
этап без параллельной нагрузки.

Выжившие в файлах A7b (до): `redact/base64.go.11, .15, .16, .24`, `redact/redact.go.40-42`,
`stepsecrets/source.go.27, .41, .46, .49`. Убиты тестами (поведение, не мутант):

- `TestABase64TextShorterThanFourCharactersIsNotSearchedFor` (`redact/base64_test.go`): значение из
  двух байт, его 3-символьные base64-формы без `=` не маскируются, форма с `=` маскируется
  (убивает `minBase64 = 3`).
- `TestAOneCharacterSecretIsWarnedAbout` (убивает `len(v.Value) > 1`).
- `TestTheCheckAtStartWarnsAboutAnUnreadableEnvFileWithoutThePath`,
  `TestTheCheckAtStartWarnsAboutAnUnreadableAgentKeyWithoutThePath`,
  `TestTheCheckAtStartWarnsAboutAnUnreadableSecretAndAForeignFileAlike`
  (`stepsecrets/source_test.go`): `Audit` сам предупреждает о нечитаемых чужих файлах и ключе
  агента, без пути (убивают `warnForeign` как no-op, пропуск `append` и `break` вместо `continue`).

Эквивалентные мутанты (остались, исключений в конфигурации нет):

- `redact/base64.go.15, .16, .24`: `from := (8*shift + 5) / 6` при `shift` из {0, 1, 2} даёт 0, 2, 3;
  замены `7*`, `+ 4`, `9*` дают те же значения. Предложение coder: таблица `[]int{0, 2, 3}[shift]`
  или константы вместо формулы.
- `redact/redact.go.40` (`offset >= w.base` в `keep`), `.41` (`from <= w.curAt`), `.42` (`to < w.curAt`
  в `appendStream`): на границе добавляется ноль байт, результат тот же. `.42` это ранний возврат
  как оптимизация. Предложение coder: убрать избыточность или оставить как есть.

Выжившие вне A7b (executor.go, store.go, pluginhost, testplugin, transport, restic, enroll,
repoinit, tlsid, redact/automaton.go и др., полный список в выводе ворот) не трогались: ворота
требуют только 0.80. Строк, затронутых A7b, в `executor`, `pluginhost/source.go`, `main.go` (кроме
перечисленных выше) среди выживших нет. Выживший `main.go.51` (`ref.Executor = exec`) вне диффа A7b.

Ворота (после): `gate: PASSED (agent, full)` (`coverage: 98.1%`, `mutation score: 0.927958`),
`gate: PASSED (sdk, full)` (`coverage: 100.0%`, `mutation score: 1.000000`),
`gate: PASSED (server, full)` (`coverage: 96.2% (instructions)`). Ошибок 429 при загрузке не было.

## Итог и дефекты

Конвейер пройден полностью: specifier → coder → cleaner → architect (APPROVED) → hardener.
Найденные по ходу дефекты и задачи, не вошедшие в A7b, записаны в `docs/open-questions.md`:
OQ-089 (путь хоста в ошибке `pluginhost.Secrets.Secret`, ADR 0008), OQ-090 (нет теста списка
зависимостей агента, ADR 0027), OQ-091 (эквивалентные мутанты в `redact`), OQ-092 (нестабильный
сетевой тест `enroll`), OQ-093 (мутационный этап агента хрупок под нагрузкой), OQ-094 (выжившие
мутанты в коде до A7b), OQ-095 (`make e2e` не работает в облачной среде из-за 429).
