# QA: регистрация агента по токену (S2b)

Сценарии: `docs/specs/server/agent-enrollment.feature`,
`docs/specs/web/agent-enrollment.feature`. Формат токена —
`docs/specs/enrollment-token.md`.

**Статус: черновик, ждёт подтверждения владельца.** Шаги с пометкой
*(решение N)* зависят от открытого вопроса N и уточняются после ответа.

Процедура из двух частей:

- **Часть 1 — gRPC, выполнима сразу после S2b.** API токенов ещё нет (D2), поэтому
  токен создаётся прямо в базе: скрипт генерирует секрет, считает хэш и
  вставляет строку в `enrollment_tokens`. Enroll вызывается `grpcurl` так, как
  его вызовет агент.
- **Часть 2 — API и консоль, заблокирована D2 → W1b и S8a.**

Ожидаемый результат указан после «→» в каждом шаге. Любое расхождение — дефект.

## Подготовка

Нужны: Docker, `grpcurl`, `openssl`, `python3`, `jq`. Команды выполняются из
корня репозитория.

```bash
make up                                     # PostgreSQL + sard-server
DC="docker compose -f deploy/docker-compose.yml"
PSQL="$DC exec -T postgres psql -U sard -d sard -At -c"
DEFAULT=00000000-0000-0000-0000-000000000001
QA=$(mktemp -d)
$DC cp server:/var/lib/sard/pki/ca/ca.crt "$QA/ca.crt"
F=$(openssl x509 -in "$QA/ca.crt" -pubkey -noout | openssl pkey -pubin -outform DER | sha256sum | cut -c1-64)
GRPC="grpcurl -cacert $QA/ca.crt -import-path proto -proto sard/agent/v1/agent.proto -format-error"
EP="localhost:9090 sard.agent.v1.EnrollmentService/Enroll"

# new_token <tenant> <expires_at SQL> [created_at SQL] — печатает строку токена, вставляет хэш в базу
new_token() {
  read -r SECRET HASH < <(python3 -c "import os,base64,hashlib; s=os.urandom(32); \
print(base64.urlsafe_b64encode(s).rstrip(b'=').decode(), hashlib.sha256(s).hexdigest())")
  $PSQL "insert into enrollment_tokens (id, tenant_id, token_hash, expires_at, created_at) \
values (gen_random_uuid(), '$1', decode('$HASH','hex'), $2, ${3:-now()})" >/dev/null
  echo "sard_${SECRET}.${F}"
}
token_row() {  # used_at | agent_id токена по строке
  local s=${1#sard_}; s=${s%%.*}
  local h=$(python3 -c "import base64,hashlib,sys; print(hashlib.sha256(base64.urlsafe_b64decode(sys.argv[1]+'=')).hexdigest())" "$s")
  $PSQL "select coalesce(used_at::text,'-') || '|' || coalesce(agent_id::text,'-') from enrollment_tokens where token_hash = decode('$h','hex')"
}
csr() {  # csr <curve|rsa> — печатает CSR в base64
  if [ "$1" = rsa ]; then openssl genrsa -out "$QA/k" 2048 2>/dev/null
  else openssl ecparam -name "$1" -genkey -noout -out "$QA/k"; fi
  openssl req -new -key "$QA/k" -subj /CN=qa -outform DER | base64 -w0
}
enroll() {  # enroll <token> <csr_b64> <hostname>
  $GRPC -d "{\"enrollment_token\":\"$1\",\"csr_der\":\"$2\",\"hostname\":\"$3\"}" $EP
}
```

→ `make up` завершается, `$F` — 64 символа hex.

## Часть 1. Enroll по gRPC

### Успех

1. `T=$(new_token $DEFAULT "now() + interval '1 hour'"); enroll "$T" "$(csr prime256v1)" qa-host | tee "$QA/ok.json"`
   → ответ с `agentId`, `certificateChainPem`, `caBundlePem`.
2. `jq -r .caBundlePem "$QA/ok.json" | openssl x509 -pubkey -noout | openssl pkey -pubin -outform DER | sha256sum`
   → совпадает с `$F`.
3. `jq -r .certificateChainPem "$QA/ok.json" | openssl x509 -noout -ext subjectAltName`
   → `URI:sard://tenants/00000000-0000-0000-0000-000000000001/agents/<agentId>`.
4. `token_row "$T"` → `used_at` заполнено, `agent_id` = `agentId` из шага 1.
5. `$PSQL "select tenant_id, hostname from agents where id = '$(jq -r .agentId "$QA/ok.json")'"`
   → тенант по умолчанию, `qa-host`.

### Отказы по токену

В каждом шаге этого раздела проверяется: код статуса, деталь
`google.rpc.ErrorInfo` с указанным `reason` и `domain` = `sard.dev`, и что
`$PSQL "select count(*) from agents"` не изменился.

6. `enroll "$T" "$(csr prime256v1)" qa-host` (токен из шага 1 повторно)
   → `UNAUTHENTICATED`, `TOKEN_USED`; `token_row "$T"` не изменился.
7. `enroll "nonsense" "$(csr prime256v1)" qa-host`
   → `INVALID_ARGUMENT`, `TOKEN_MALFORMED`.
8. `enroll "" "$(csr prime256v1)" qa-host`
   → `INVALID_ARGUMENT`, `TOKEN_MALFORMED`.
9. `T2=$(new_token $DEFAULT "now() + interval '1 hour'"); enroll "${T2%.*}.$(echo "${T2##*.}" | tr a-f A-F)" "$(csr prime256v1)" qa-host`
   (отпечаток в верхнем регистре)
   → `INVALID_ARGUMENT`, `TOKEN_MALFORMED`; `token_row "$T2"` → `-|-` (токен не тронут).
10. `python3 -c "import os,base64; print('sard_'+base64.urlsafe_b64encode(os.urandom(32)).rstrip(b'=').decode()+'.$F')"` → строка `U`; `enroll "$U" "$(csr prime256v1)" qa-host`
    → `UNAUTHENTICATED`, `TOKEN_UNKNOWN`.
11. `T3=$(new_token $DEFAULT "now() - interval '1 second'" "now() - interval '1 hour'"); enroll "$T3" "$(csr prime256v1)" qa-host`
    → `UNAUTHENTICATED`, `TOKEN_EXPIRED`; `token_row "$T3"` → `-|-`.
12. *(решение 3)* Создать токен `T4`, пометить его отозванным способом, выбранным
    в решении 3 (или через API из части 2), `enroll "$T4" …`
    → `UNAUTHENTICATED`, `TOKEN_REVOKED`.
13. *(решение 4)* `enroll "${T2%.*}.8544e2352a80a3d403eed68f8bb4ff271d0ce02c09c1d0faf6f090cea423be9f" "$(csr prime256v1)" qa-host`
    (секрет настоящего токена, отпечаток тестового вектора)
    → `UNAUTHENTICATED`, `TOKEN_FOREIGN_CA`; `token_row "$T2"` → `-|-`.

### Неудача не расходует токен

14. `enroll "$T2" "$(printf garbage | base64 -w0)" qa-host`
    → `INVALID_ARGUMENT`, `CSR_INVALID`; `token_row "$T2"` → `-|-`.
15. `enroll "$T2" "$(csr rsa)" qa-host` → `INVALID_ARGUMENT`, `CSR_INVALID`; `token_row "$T2"` → `-|-`.
16. `enroll "$T2" "$(csr secp521r1)" qa-host` → `INVALID_ARGUMENT`, `CSR_INVALID`; `token_row "$T2"` → `-|-`.
17. `enroll "$T2" "$(csr secp384r1)" qa-host` → успех; `token_row "$T2"` → заполнено.
18. `T5=$(new_token $DEFAULT "now() + interval '1 hour'"); $DC stop postgres; enroll "$T5" "$(csr prime256v1)" qa-host`
    → `UNAVAILABLE`, `INTERNAL_RETRYABLE`; текст статуса без подробностей сбоя.
19. `$DC start postgres`, дождаться `healthy`; `token_row "$T5"` → `-|-`;
    `enroll "$T5" "$(csr prime256v1)" qa-host` → успех.

### Тенант берётся из токена

20. `$PSQL "insert into tenants (id, name) values ('00000000-0000-0000-0000-0000000000b0', 'qa-b')"`;
    `TB=$(new_token 00000000-0000-0000-0000-0000000000b0 "now() + interval '1 hour'"); enroll "$TB" "$(csr prime256v1)" qa-b-host | tee "$QA/b.json"`
    → успех; `$PSQL "select tenant_id from agents where id = '$(jq -r .agentId "$QA/b.json")'"` → `…0000b0`;
    SAN сертификата называет тенант `…0000b0`.

### Повторная регистрация хоста

21. `T6=$(new_token $DEFAULT "now() + interval '1 hour'"); enroll "$T6" "$(csr prime256v1)" qa-host`
    → успех, `agentId` отличается от шага 1;
    `$PSQL "select count(*) from agents where hostname = 'qa-host'"` → на 1 больше, чем до шага.

### Гонка

22. `T7=$(new_token $DEFAULT "now() + interval '1 hour'"); for i in $(seq 10); do (enroll "$T7" "$(csr prime256v1)" race > "$QA/race$i" 2>&1; echo $? >> "$QA/race.rc") & done; wait`
    → в `$QA/race.rc` ровно один `0`; в остальных девяти файлах `UNAUTHENTICATED` и `TOKEN_USED`;
    `$PSQL "select count(*) from agents where hostname = 'race'"` → `1`.

### Секрет не утекает

23. Для каждой строки токена, использованной выше (`$T`, `$T2` … `$T7`, `$TB`), и её
    секрета: `$DC logs server | grep -cF "<строка или секрет>"` → `0`.
24. В выводе шагов 6–18 (текст статуса и детали) ни строки токена, ни секрета
    → `grep -F` по сохранённому выводу даёт 0 совпадений.

## Часть 2. API и консоль (заблокировано D2 → W1b, S8a)

Выполняется после появления входа администратора и API токенов. Пути и коды
HTTP — из контракта S8a.

1. Без входа запросить список токенов → отказ в аутентификации, данных токенов нет.
2. Войти администратором тенанта, создать токен без срока *(решение 1)*
   → срок = сейчас + 24 ч; показана команда `sard-agent enroll --server <адрес> --token <строка>` *(решение 5)*
   и предупреждение «показывается один раз».
3. Нажать «Копировать» → буфер обмена содержит ровно показанную команду.
4. Закрыть диалог, открыть карточку токена → ни строки, ни команды на странице;
   в ответе API карточки и списка (DevTools → Network) строки токена нет.
5. Создать токен со сроком 4 мин 59 с и 7 дней 1 с *(решение 1)*
   → ошибка у поля срока, команда не показана, токен в списке не появился.
6. Создать токен с подписью из 201 символа *(решение 2)* → ошибка у поля подписи.
7. Выполнить на хосте `sard-agent enroll` (A2b) командой из шага 2 → агент
   зарегистрирован; в списке токен в состоянии «использован», кнопки отзыва нет.
8. Создать токен, нажать «Отозвать» → состояние «отозван»; регистрация этим
   токеном → `TOKEN_REVOKED`.
9. Создать токен со сроком 5 мин, подождать 5 мин → состояние «истёк», кнопки
   отзыва нет.
10. Войти администратором другого тенанта → токены и агенты первого тенанта
    не видны; прямой запрос карточки по идентификатору → «не найден».
11. `docker compose logs server | grep -F` по строкам и секретам токенов из этой
    части → 0 совпадений.
