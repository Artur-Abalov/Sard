# Сессия 2026-09-28: A2a — механизм регистрации агента + A1 (права секретных файлов)

Ветка `claude/work-report-pb9coa`. Спецификация: `docs/specs/agent/agent-enroll.feature`
(владелец утвердил A2b 2026-09-28; A2a реализуется «следующим, по этой же
спецификации» — п. 2 «Порядок работ»). Формат токена —
`docs/specs/enrollment-token.md`. Модель ошибок сервера — ADR 00XX. Доверие
по отпечатку и цепочка «лист+корень» — ADR 0014. A3 (транспорт, TLS-хелперы
тестов) уже в `main` — `docs/sessions/2026-09-27-a3-transport.md`.

CLI-команда `sard-agent enroll` (A2b) в этой сессии не реализуется —
только механизм, которым она будет пользоваться, и A1.

## Пакет `agent/internal/enroll`

Публичный API (для A2b):

- `ParseToken(s string) (Token, error)` — строгий разбор `sard_<секрет>.<отпечаток>`
  (docs/specs/enrollment-token.md); любое нарушение — единый `*TokenError{Reason: "TOKEN_MALFORMED"}`,
  текст ошибки никогда не содержит входную строку. Проверен тестовым вектором
  из спецификации.
- `NormalizeTokenFile(data []byte) string` — отбрасывает ровно один
  завершающий LF/CRLF (В11); остальное (пустой файл, лишние пробелы) —
  забота A2b.
- `NewIdentityKey() (*ecdsa.PrivateKey, error)`, `BuildCSR(key, hostname) ([]byte, error)` —
  свежий ключ ECDSA P-256 и PKCS#10 CSR с CN=hostname (сервер игнорирует
  subject и сам решает содержимое сертификата, ADR 0014).
- `DialTOFU(ctx, address, fingerprint string) (*grpc.ClientConn, error)` —
  doверие по первому использованию (D4.3): TLS-рукопожатие вручную (не
  через `grpc.NewClient`), отпечаток корня из цепочки сверяется с токеном
  **до** передачи готового соединения куда-либо, что могло бы отправить
  токен или CSR. Возвращает `*Error` с `Class` — `trust` (несовпадение
  отпечатка, имени хоста, любой другой сбой TLS) или `temporary`
  (недоступность, таймаут, отмена контекста).
- `CallEnroll(ctx, conn, req) (*EnrollResult, error)` — вызывает
  `EnrollmentService.Enroll` и классифицирует отказ по `reason` из
  `google.rpc.ErrorInfo` (домен `sard.dev`, ADR 00XX) в ту же `*Error`:
  `token-refused` (TOKEN_UNKNOWN/USED/EXPIRED/REVOKED), `trust`
  (TOKEN_FOREIGN_CA), `usage` (TOKEN_MALFORMED — защитно, сам агент такой
  токен уже отсеял), `agent-error` (CSR_INVALID, HOSTNAME_INVALID,
  неизвестная причина, непредусмотренный код/ответ), `temporary`
  (INTERNAL_RETRYABLE — токен цел; таймаут/обрыв после отправки запроса —
  `TokenMaybeSpent: true`, В14). Не повторяет вызов.
- `InspectIdentity(files config.TLS) (IdentityStatus, error)` — В19:
  существует, если есть `tls.cert_file` или `tls.key_file`; `agent_id`
  берётся из URI SAN `sard://tenants/<t>/agents/<a>` (авторитетный
  источник — `server/pki/CertificateAuthority.kt`, не CN); неразбираемый
  сертификат — `Unreadable: true`, identity всё равно существует; один
  `tls.ca_file` не считается.
- `CheckWritable(files Files) error` — каталоги не создаются (В12); первая
  непригодная директория — `*Error{Class: write}`.
- `WriteIdentity(files Files, key, cert, ca []byte) error` — атомарная
  запись всех трёх файлов: сперва все три пишутся во временные файлы рядом
  (fsync), и только если все три успешно застейджены — все три
  переименовываются (rename + fsync каталога). Ключ — 0600, сертификат и
  бандл — 0644, независимо от umask (`Chmod` после создания, не полагаемся
  на маску процесса). Сбой на любом этапе стадирования не создаёт ни
  одного rename — предыдущие файлы гарантированно не тронуты.
- `Lock(certFile string) (unlock func(), error)` / `LockPath(certFile string) string` —
  файл блокировки рядом с `tls.cert_file` (В15), `O_EXCL`; второй
  параллельный вызов — `*Error{Class: temporary}`.
- `Files{KeyFile, CertFile, CAFile string}`, `Class` (`agent-error`,
  `usage`, `token-refused`, `trust`, `temporary`, `write`), `*Error`
  (`Class`, `Reason`, `Address`, `Names`, `TokenMaybeSpent`, `Code`) —
  единая точка классификации; A2b сводит `Class` → код выхода в одном
  месте (`switch` по `Class`), `Reason`/`Address`/`Names` — в тексты
  сообщений.

`agent/internal/config`: `AddressEqual(a, b string) bool` — В10 (хост без
учёта регистра, IPv6 по значению через `net.ParseIP`, порты равны;
непарсящийся адрес — не равны).

## Решения и почему

1. **`DialTOFU` делает TLS-рукопожатие вручную** (`net.Dialer.DialContext`
   + `tls.Client(...).HandshakeContext`), а не через `grpc.NewClient` с
   `VerifyPeerCertificate`. Причина: `grpc.NewClient` ленив (не
   подключается, пока не вызван RPC), и нет способа гарантировать, что
   проверка отпечатка завершилась до того, как что-то отправит токен —
   разве что форсировать реальное соединение вручную. Уже установленное и
   провалидированное TLS-соединение (`*tls.Conn`) передаётся в
   `grpc.NewClient` через `grpc.WithContextDialer` + собственный
   `credentials.TransportCredentials` (`tofuCredentials`), который просто
   возвращает уже готовый `*tls.Conn` в `ClientHandshake` — второго
   TLS-рукопожатия не происходит. Тест `TestDialTOFURejectsAMismatchedFingerprintBeforeAnyRequest`
   и симметричные ему проверяют это через `@fake`-сервер (как в A3):
   TLS-серверу, который сразу отдаёт цепочку, но никогда не видит вызов
   `Enroll`, потому что рукопожатие обрывается раньше.
2. **Отвергнуто: проверка отпечатка после установления `grpc.ClientConn`
   через `VerifyPeerCertificate` в его `credentials.NewTLS`.** Такой
   `ClientConn` ленив; чтобы форсировать проверку, пришлось бы вызывать
   `conn.Connect()` и опрашивать `GetState()` — тогда для сценария
   «сервер без TLS» (не отвечает TLS-рукопожатием вовсе) неразличимы
   «недоступен» и «не тот протокол», потому что `grpc.ClientConn` не
   даёт публичного доступа к тексту последней ошибки транспорта (только
   состояние `TransientFailure`). Ручное рукопожатие даёт точный `error`
   от `crypto/tls` и различает классы `trust`/`temporary` по факту, где
   именно оборвалось: TCP-соединение не установилось → `temporary`;
   установилось, но TLS не сложился (в т.ч. сервер без TLS вовсе) →
   `trust`.
3. **`grpc.ClientConn` после успешного `DialTOFU` форсированно
   подключается** (`conn.Connect()`), иначе уже готовое TLS-соединение
   осталось бы неиспользуемым, а тестовый gRPC-сервер завис бы в
   ожидании HTTP/2-преамбулы при остановке (`grpc.Server.Stop` ждёт
   закрытия соединений) — обнаружено по зависшему тесту, воспроизведено
   и исправлено.
4. **Единый `*enroll.Error` на все типизированные исходы**, а не отдельные
   типы ошибок по разделу спецификации. У A2b одна точка «класс → код
   выхода» (`switch e.Class`), как и просит задание; поля `Reason`,
   `Address`, `Names`, `TokenMaybeSpent`, `Code` — ровно то, что нужно
   текстам сообщений по В2/В9/В14/В17/В18, без отдельного парсинга.
5. **`ClassUsage` добавлен в `enroll.Class`**, хотя по декомпозиции задания
   класс «использование» — целиком A2b (локальные проверки флагов/токена
   до сети). Причина: `classifyReason` обязан защитно обработать
   `TOKEN_MALFORMED` от сервера (таблица «Отказ сервера по причине...» в
   фиче явно указывает класс «использование» для этой причины), хотя сам
   агент такой токен уже отсеивает `ParseToken` до отправки. Без этого
   класса `CallEnroll` пришлось бы либо считать такой ответ
   «непредусмотренным» (agent-error — неверно по спецификации), либо
   падать с паникой на неизвестном классе.
6. **`agentIDFromCertFile` берёт `agent_id` из URI SAN, не из CN.**
   Проверено по `server/src/main/kotlin/dev/sard/server/pki/CertificateAuthority.kt`
   (`AgentIdentity.of`) — сервер считает авторитетным именно URI SAN, CN
   в сертификате дублирует то же значение, но не гарантированно (В19
   упоминает оба варианта расплывчато).
7. **Атомарность записи трёх файлов: стадирование всех трёх, потом все
   renames.** Настоящая atomic-запись сразу трёх независимых файлов в
   разных каталогах не бывает (rename атомарен только для одного файла).
   Выбранный порядок гарантирует: если сбой происходит на любом из трёх
   `stage()` (запись во временный файл + fsync) — ни одного `rename` ещё
   не было, старые файлы гарантированно не тронуты. Единственное окно,
   которое не закрыть — сбой ровно между двумя `rename` (после
   переименования файла N, но до N+1); оно зафиксировано в комментарии
   `WriteIdentity`, как и просило задание («документируй стратегию
   отката»), и не имеет практического способа устранения без файловой
   системы с транзакциями поверх нескольких файлов (вне выбора стека).
   Тесты фиксируют сбой строго на этапе стадирования (директория
   заменяется обычным файлом — тот же приём `store_test.go`, «даже для
   root», без chmod, который root обходит).
8. **Проверка владельца/прав секретных файлов (A1) реализована в
   отдельном пакете `agent/internal/secrets`**, а не внутри `enroll`: A1
   касается запуска агента (`sard-agent --config`, без `enroll`), а
   `enroll` — отдельная команда (A2b). Разделение совпадает с границей
   из задания («плюс A1 secret-file permission check at agent start»).
9. **`secrets.CheckAll` пропускает отсутствующий файл** (`stat` вернул
   `fs.ErrNotExist`), не считая это нарушением В20. Причина: спецификация
   В20 говорит о правах и владельце *существующих* файлов; отсутствие
   файла — отдельная проблема, которую и так сообщает код, которому файл
   нужен (транспорт для `tls.key_file`, executor/restic для остальных).
   Проверено существующим тестом `TestValidConfigDialsTheServerUntilStopped`
   (`agent/cmd/sard-agent/main_test.go`) — до этой правки любое включение
   A1-проверки «в лоб» валило бы этот тест, потому что там `tls.key_file`
   и `tls.cert_file` намеренно не существуют (транспорт читает их лениво
   при рукопожатии, которого в тесте не происходит).
10. **`secrets.StatFunc` инъецируется, `RealStat` — единственная
    продакшен-реализация** (`os.Stat` + `syscall.Stat_t.Uid`). Тесты не
    зависят от uid процесса — вся матрица сценариев `@a1` подставляет свой
    uid через фейковую карту `path → Info`.
11. **Тесты `@a1` названы точно по тексту сценариев спецификации**
    (`TestSecretFileOpenToGroupOrOthersRefusesStart`,
    `TestSecretFileOwnedByAnotherUserRefusesStart`,
    `TestSecretFileClosedToAllButOwnerIsAccepted`,
    `TestOpenCertAndCABundleDoNotBlockStart`,
    `TestRepositoryWithoutEnvFileNeedsNoEnvFile`), как S2b/A3 именуют тесты
    по сценариям Gherkin.
12. **Зависимость `google.golang.org/genproto/googleapis/rpc` (пакет
    `errdetails`) объявлена явно** в `agent/go.mod` (была transitive-only
    через grpc) и добавлена в `docs/dependencies.md` — `CallEnroll`
    напрямую использует `errdetails.ErrorInfo` для разбора причины отказа.
    Apache-2.0, совместима.

## Как проверено

- `cd agent && GOWORK='' go test ./...` — все пакеты зелёные (включая
  новые `internal/enroll`, `internal/secrets`, обновлённые
  `internal/config`, `cmd/sard-agent`).
- `./scripts/gate.sh agent fast` — PASSED: `gofmt`/`vet`/`golangci-lint` —
  0 issues; тесты + покрытие — 95.5% (порог 80%); CRAP — все функции
  ≤ 6.0 (порог 6); интеграционные тесты restic — зелёные (не тронуты).
- `make license-check` — `268 files OK`.
- Зависание при первом варианте `DialTOFU` (через `grpc.NewClient` без
  форсированного `Connect()`) воспроизведено `go test -timeout 5s` с
  дампом горутин — `grpc.Server.Stop()` висел в `sync.WaitGroup.Wait`,
  ожидая закрытия соединения, которое никогда не использовалось для
  HTTP/2. Исправлено явным `conn.Connect()` в `DialTOFU` после успешной
  проверки; тест перепроверен `go test -run TestDialTOFU -timeout 20s`.

## Открытые вопросы / потенциальная неоднозначность (для A2b и владельца)

- Спецификация A2b (сценарий «Сервер без TLS не проходит проверку»)
  требует класс «доверие» для сервера, который вообще не говорит по TLS.
  `DialTOFU` классифицирует любой сбой TLS-рукопожатия (после успешного
  TCP-подключения), кроме истечения контекста, как `trust` — это
  включает и «не то TLS», и «сброс соединения в момент рукопожатия»,
  которые неразличимы средствами `crypto/tls` без более глубокого разбора
  сетевых ошибок. Реализовано так осознанно (см. решение 2 выше), но это
  единственное место, где граница `trust`/`temporary` определяется
  эвристикой «TCP дошло → значит недоверие, а не недоступность», а не
  точным анализом причины. Если в `@fake`-тестах A2b обнаружится сценарий,
  где эта эвристика даёт не тот класс — придётся вернуться к этому месту.
- Задание просило рассмотреть `tls.ca_file` проверку «отпечаток корня в
  bundle равен F» как часть A2a — это тривиально (тот же `spkiFingerprint`
  из `trust.go`, который уже экспортируется как приватная функция), но
  явного публичного хелпера для этого A2a пока не даёт: он понадобится
  только `@fake`-тестам A2b, которые проверяют записанный `tls.ca_file`
  постфактум, и его проще добавить вместе с A2b, чем угадывать сигнатуру
  сейчас.
