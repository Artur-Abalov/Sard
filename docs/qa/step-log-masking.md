# QA: маскирование секретов в логах шагов (A7b)

Сценарии: `docs/specs/agent/step-log-masking.feature` (агент) и
`docs/specs/server/step-log-masking.feature` (сервер, правило 8).
Спецификация — черновик, ждёт утверждения владельцем; шаги, помеченные
«(OQ-NNN)», проверяют рекомендацию specifier к этому вопросу
(`docs/open-questions.md`) и меняются вместе с решением владельца.

Выполнима после реализации A7b. Здесь вручную проходятся сценарии с тегом
`@qa` и сквозной путь «restic напечатал секрет → step_logs → REST»;
остальное проверяют тесты `@unit`, `@restic`, `@start`, `@server`, `@doc`,
`@e2e` с теми же названиями.

Ожидаемый результат указан после «→» в каждом шаге. Любое расхождение — дефект.

Способ заставить шаг напечатать секрет: репозиторий, `url` которого —
несуществующий локальный каталог, в имени которого стоит нужный вид
значения. Шаг `files` с таким репозиторием падает, restic печатает путь в
ошибке (так же устроен `StepLogRedactionTest`, A7c). Агент не знает, откуда
текст, — он маскирует любой вывод restic шага.

## Подготовка

Нужны: Docker, Go, `restic` 0.19.1 (`.bin/restic` после `make tools` или из
пакета агента), `curl`, `jq`, `openssl`, GNU `sed`. Команды — из корня
репозитория.

1. Выполнить «Подготовку» и часть 2 из `docs/qa/agent-enroll.md` (сервер
   поднят, агент зарегистрирован; переменные `AG`, `H`, `QA`, `DC`, `PSQL`).
   Агент, если запущен, остановить.
2. Выполнить «Подготовку» из `docs/qa/admin-login.md` (переменные `API`,
   `PW`, функция `login`); `login $PW $QA/jf >/dev/null`.
3. Секреты, виды значения и конфиг агента:

```bash
S=$H/sec; mkdir -m 0700 $S
# T — только буквы, цифры, "/", "?" и пробел: для них экранирование jq @uri
# совпадает с экранированием сегмента пути URL; длина не кратна 3 — у base64 есть "="
T="Qa/Tok?en $RANDOM$RANDOM x"; while [ $(( ${#T} % 3 )) -eq 0 ]; do T="${T}z"; done
T2="Qa/Rot?ated $RANDOM$RANDOM y"
printf '%s\n' "$T" > $S/tok
openssl genpkey -algorithm ed25519 -out $S/pem          # ключ PEM из трёх строк
printf 'ab7\n' > $S/short
printf 'AWS_SECRET_ACCESS_KEY=ENV-MARKER-%s\n' "$RANDOM$RANDOM" > $S/main.env; ENVV=$(cut -d= -f2 $S/main.env)
printf 'AWS_SECRET_ACCESS_KEY=ENV2-MARKER-%s\n' "$RANDOM$RANDOM" > $S/other.env; ENV2=$(cut -d= -f2 $S/other.env)
head -c 24 /dev/urandom | base64 | tr '+/' 'xy' > $S/qa.pass; PASS=$(cat $S/qa.pass)
chmod 0600 $S/*
B64=$(printf %s "$T" | base64 -w0)                       # стандартный, с "="
B64U=$(printf %s "$B64" | tr '+/' '-_')                  # base64url, с "="
B64R=${B64%%=*}                                          # стандартный без "="
PE=$(jq -rn --arg v "$T" '$v|@uri')                      # %2F %3F %20
PEL=$(printf %s "$PE" | sed 's/%\(..\)/%\L\1/g')         # hex нижним регистром
PEMQ=$(sed -z 's/\n$//; s/\n/\\n/g' $S/pem)              # ключ для строки YAML в кавычках
PEMLINE=$(sed -n 2p $S/pem)                              # строка тела ключа
repo() { printf '  - name: %s\n    url: "%s"\n    password_file: %s\n    env_file: %s\n' "$1" "$2" "$S/qa.pass" "${3:-$S/main.env}"; }
{ echo "secrets:"; for n in tok pem short; do echo "  $n: $S/$n"; done
  echo "repositories:"
  repo leak-raw     "$QA/nope-$T"
  repo leak-b64     "$QA/nope-$B64"
  repo leak-b64url  "$QA/nope-$B64U"
  repo leak-b64raw  "$QA/nope-$B64R-end"
  repo leak-path    "$QA/nope-$PE"
  repo leak-pathlow "$QA/nope-$PEL"
  repo leak-pem     "$QA/nope-$PEMQ-end"
  repo leak-short   "$QA/nope-ab7-x"
  repo leak-env     "$QA/nope-$ENVV"
  repo leak-env2    "$QA/nope-$ENV2"
  repo leak-pass    "$QA/nope-$PASS"
  repo leak-rot     "$QA/nope-$T2"
  repo plain        "$QA/nope-plain"
  repo other        "$QA/other" $S/other.env
  echo "executor:"; echo "  state_dir: $H/state"
} >> "$H/agent.yaml"
"$AG" --config "$H/agent.yaml" > $QA/agent.out 2>&1 &
AGPID=$!
AGENT=$($PSQL "select id from agents order by created_at desc limit 1")
```

   → агент в `$QA/agent.out` печатает `connecting to localhost:9090` и не
   завершается.

Вспомогательные функции:

```bash
# leak <repo> — шаг files с репозиторием <repo>; ждёт окончания; пишет лог шага
# в $QA/log, сообщение шага в $QA/msg; печатает статус шага
leak() {
  SRC=$(curl -sS -b $QA/jf -X POST $API/sources -H 'Content-Type: application/json' \
    -d "{\"name\":\"qa-$RANDOM\",\"agentId\":\"$AGENT\",\"plugin\":\"files\",\"repositoryName\":\"$1\",\"config\":{\"paths\":[\"/etc/hostname\"]}}" | jq -r .id)
  RUN=$(curl -sS -b $QA/jf -X POST $API/sources/$SRC/runs | jq -r .id)
  for i in $(seq 1 120); do st=$(curl -sS -b $QA/jf $API/runs/$RUN | jq -r .status)
    case $st in queued|running) sleep 1;; *) break;; esac; done
  STEP=$(curl -sS -b $QA/jf $API/runs/$RUN | jq -r '.steps[0].id')
  curl -sS -b $QA/jf "$API/runs/$RUN/steps/$STEP/logs?limit=1000" | jq -r '.items[].text' > $QA/log
  curl -sS -b $QA/jf $API/runs/$RUN | jq -r '.steps[0].message // ""' > $QA/msg
  curl -sS -b $QA/jf $API/runs/$RUN | jq -r '.steps[0].status'
}
has() { grep -cF -- "$1" $QA/log $QA/msg | awk -F: '{s+=$2} END {print s}'; }   # has <текст> — число вхождений в лог и сообщение
```

## Часть 1. Старт агента и короткое значение

4. `grep -i warn $QA/agent.out | grep -c short`
   → `1`: ровно одна строка WARN называет секрет `short` и говорит, что
   логи станут менее читаемыми.
5. `grep -c ab7 $QA/agent.out` → `0` (значение не названо).

## Часть 2. Виды значения в логе шага

В каждом шаге этой части, кроме 15 и 16: `leak` печатает `failed`.

6. **Как есть.** `leak leak-raw; has "nope-[REDACTED]"; has "$T"`
   → `failed`; первое число ≥ `1`; второе — `0`.
7. **base64 стандартный.** `leak leak-b64; has "nope-[REDACTED]"; has "$B64"; has "$B64R"`
   → ≥ `1`, `0`, `0`; ни в одной строке `$QA/log` с маркером нет `]=`
   (`grep -cF ']=' $QA/log` → `0`): маркер поглотил выравнивание (OQ-075).
8. **base64url.** `leak leak-b64url; has "nope-[REDACTED]"; has "$B64U"`
   → ≥ `1`, `0`.
9. **base64 без выравнивания** (OQ-075). `leak leak-b64raw; has "nope-[REDACTED]-end"; has "$B64R"`
   → ≥ `1`, `0`.
10. **Экранирование пути URL.** `leak leak-path; has "nope-[REDACTED]"; has "$PE"`
    → ≥ `1`, `0`.
11. **hex нижним регистром.** `leak leak-pathlow; has "nope-[REDACTED]"; has "$PEL"`
    → ≥ `1`, `0`.
12. **Ключ PEM целиком.** `leak leak-pem; has "nope-[REDACTED]-end"; has "$PEMLINE"; has "BEGIN PRIVATE KEY"`
    → ≥ `1`, `0`, `0`: на месте всего ключа один маркер, ни одна строка ключа
    не видна.
13. **Короткое значение.** `leak leak-short; has "nope-[REDACTED]-x"; has "ab7"`
    → ≥ `1`, `0`.
14. **Файл окружения репозитория шага.** `leak leak-env; has "nope-[REDACTED]"; has "$ENVV"`
    → ≥ `1`, `0`.
15. **Файл окружения другого репозитория** (OQ-078). `leak leak-env2; has "$ENV2"`
    → `failed`; `0`.
16. **Пароль репозитория не маскируется** (правило 2, ADR 0008).
    `leak leak-pass; has "nope-$PASS"`
    → `failed`; ≥ `1`: агент не читает `password_file`, текст пароля в
    выводе виден как есть. Документация (шаг 24) говорит об этом.
17. **Строки без секретов не меняются.** `leak plain; has "nope-plain"; grep -cF '[REDACTED]' $QA/log`
    → ≥ `1`, `0`.

## Часть 3. Замена секрета на диске

18. `leak leak-rot; has "$T2"`
    → ≥ `1`: `T2` ещё не секрет.
19. `printf '%s\n' "$T2" > $S/tok; leak leak-rot; has "nope-[REDACTED]"; has "$T2"`
    (агент не перезапускается) → ≥ `1`, `0`: новое значение маскируется со
    следующего шага.
20. `grep -c "$T2" $QA/agent.out` → `0`. Вернуть значение:
    `printf '%s\n' "$T" > $S/tok`.

## Часть 3а. Нечитаемый чужой файл окружения (OQ-088)

Репозиторий `other` — не репозиторий шага `leak-raw`, его `env_file` —
`$S/other.env`. Файл делается нечитаемым переименованием (среда QA — root,
`chmod 000` root не остановит). `leak-raw` падает всегда (каталога
репозитория нет), поэтому проверяется не статус, а то, что сообщение шага не
говорит об `env_file`, а маскирование остальных значений работает.

20a. `W0=$(grep -i warn $QA/agent.out | grep -cw other); mv $S/other.env $S/other.env.off; leak leak-raw; has "nope-[REDACTED]"; grep -cF env_file $QA/msg`
     → `failed`; ≥ `1`; `0`: шаг выполнен, чужой файл его не свалил.
20b. `echo $(( $(grep -i warn $QA/agent.out | grep -cw other) - W0 )); grep -cF "$S/other.env" $QA/agent.out`
     → `1`, `0`: одна строка WARN называет репозиторий `other` и `env_file`,
     пути в журнале нет.
20c. `leak leak-raw >/dev/null; echo $(( $(grep -i warn $QA/agent.out | grep -cw other) - W0 ))`
     → `1`: на втором шаге предупреждение не повторилось.
20d. `mv $S/other.env.off $S/other.env; leak leak-env2; has "$ENV2"`
     → `failed`; `0`: файл снова читается, его значения маскируются со
     следующего шага.
Вручную не проверяется (покрыто тестами): что нечитаемый `env_file` репозитория шага валит шаг,
проверяют тесты `@unit` («Нечитаемый файл окружения репозитория шага —
шаг падает…»); нечитаемый `tls.key_file` — тесты `@unit` правила
«Нечитаемый источник, от которого шаг не зависит…» (переименование ключа
работающего агента ломает его переподключение).

## Часть 4. Сервер хранит то, что прислал агент

21. `$PSQL "select count(*) from step_logs where position('[REDACTED]' in text) > 0"`
    → ≥ `8` (шаги 6–14): маркер хранится буквально.
22. Для последнего шага части 2:
    `$PSQL "select text from step_logs where step_id='$STEP' order by seq" | diff - $QA/log`
    → пусто: REST отдаёт ровно то, что в базе (правило 8).
23. Ни одно значение не попало на сервер и в журнал агента:

```bash
for v in "$T" "$B64" "$B64R" "$B64U" "$PE" "$PEL" "$PEMLINE" "$ENVV"; do
  printf '%s %s %s\n' \
    "$($PSQL "select count(*) from step_logs where position(\$q\$$v\$q\$ in text) > 0")" \
    "$($PSQL "select count(*) from run_steps where position(\$q\$$v\$q\$ in coalesce(message,'')) > 0")" \
    "$(grep -cF -- "$v" $QA/agent.out)"
done
```

    → каждая строка — `0 0 0`. (`T2` здесь не проверяется: до шага 19 он не
    был секретом, и лог шага 18 содержит его по праву.)

## Часть 5. Консоль

24. Открыть в консоли (`cd web && npm run dev`, http://localhost:5173) запуск
    шага 12 и лог шага → строка с `nope-[REDACTED]-end` видна, строк ключа
    PEM нет. (Если страницы лога шага в консоли ещё нет — пропустить,
    отметив это в отчёте QA.)

## Часть 6. Документация

25. `test -f docs/operations/step-log-masking.md && echo ok` → `ok`.
26. Прочитать `docs/operations/step-log-masking.md` → называет `secrets` и
    `env_file`; говорит, что маскируются все секреты агента, а не только
    используемые шагом; перечисляет виды: как есть, JSON, экранирование URL,
    userinfo, hex `%XX` в любом регистре, base64 и base64url; говорит, что
    ключ PEM заменяется одним маркером; называет маркер `[REDACTED]` и то,
    что имя секрета не раскрывается; говорит о предупреждении при старте
    для значений короче 4 знаков.
27. Там же → говорит, что не маскируется секрет, напечатанный в другом
    виде: зашифрованным, хешированным, по частям, base64 с переносами
    строк, целиком в hex, с другими переводами строк (CRLF вместо LF); что
    пароль репозитория из `password_file` агент не читает и не маскирует;
    что сервер логи повторно не маскирует; что новое значение секрета
    маскируется со следующего шага; что значения нечитаемого `env_file`
    другого репозитория и нечитаемого `tls.key_file` не маскируются, пока
    файл не прочитается, а агент пишет предупреждение в свой журнал (OQ-088).

## Завершение

28. `kill $AGPID`; `rm -rf $S`.
