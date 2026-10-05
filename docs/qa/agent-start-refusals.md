# QA: отказы, после которых агент ждёт оператора (OQ-001, OQ-050)

Сценарии: `docs/specs/agent/agent-start-refusals.feature` (утверждена
владельцем 2026-10-05). Здесь вручную проходятся сценарии с тегом `@qa`;
остальные проверяют тесты с теми же названиями.

Ожидаемый результат указан после «→» в каждом шаге. Любое расхождение — дефект.
Суффикс строки отказа Register (далее `SUF`) — дословно:

```
not reconnecting: fix the cause, then restart the agent
```

Коды выхода старта агента: `0` остановлен, `1` ошибка старта, `2` флаги,
`78` окончательный отказ сервера на Register (systemd не перезапускает).

## Подготовка

Нужны: Docker, Go, `openssl`. Команды выполняются из корня репозитория, **не от
root**.

1. Выполнить части «Подготовка» и 2 из `docs/qa/agent-enroll.md` (сервер поднят,
   агент зарегистрирован, файлы в `$H/tls`, переменные `AG`, `H`, `QA`, `DC`).
2. Подготовить restic, репозитории и вспомогательные функции:

```bash
scripts/fetch-restic.sh
RV=$(sed -n 's/^version=//p' agent/internal/restic/restic-version)
RB=$PWD/.bin/restic/$RV/linux_$(go env GOARCH)/restic
cp "$H/agent.yaml" "$QA/base.yaml"
mkdir -p -m 0700 "$H/sec" "$QA/cache"
printf 'qa-pass\n' > "$H/sec/main.pass"; chmod 0600 "$H/sec/main.pass"
URLM=URL-MARKER-$RANDOM
SUF='not reconnecting: fix the cause, then restart the agent'
# mk <имя первого репозитория> <crypto_provider offsite или пусто> [restic.path] — пишет $H/agent.yaml
mk() { { cat "$QA/base.yaml"; cat <<EOF
restic:
  path: ${3:-$RB}
  cache_dir: $QA/cache
repositories:
  - name: $1
    url: $QA/repo
    password_file: $H/sec/main.pass
  - name: offsite
    url: rest:http://qa:$URLM@127.0.0.1:9/offsite
    password_file: $H/sec/main.pass
EOF
[ -n "$2" ] && printf '    crypto_provider: "%s"\n' "$2"; } > "$H/agent.yaml"; }
# start — запуск агента не дольше 10 с; печатает код выхода, stdout и stderr
start() { timeout 10 "$AG" --config "$H/agent.yaml" >"$QA/sout" 2>"$QA/serr"; echo "exit=$?"; cat "$QA/sout" "$QA/serr"; }
# line — строки stderr с префиксом "sard-agent: "
line() { grep '^sard-agent: ' "$QA/serr"; }
```

## Часть 1. Окончательный отказ Register (OQ-001)

3. `mk main ""; start`
   → агент подключается: в stdout `connecting to localhost:9090`, завершается
   по `timeout` (`exit=124`); `grep -cF "$SUF" "$QA/serr"` → `0`.
4. Имя репозитория, которое сервер отклоняет (`NAME_INVALID`):
   `mk -qa ""; start`
   → завершается за секунды, `exit=78`; `line | wc -l` → `1`; `line` содержит
   `code = InvalidArgument`, `desc = register rejected`,
   `reason NAME_INVALID, field=repositories[0].name` и оканчивается `$SUF`;
   в stdout есть `connecting to` (отказ пришёл от сервера).
5. Агент не повторяет Register: `mk -qa ""; time (timeout 70 "$AG" --config "$H/agent.yaml" >/dev/null 2>&1; echo "exit=$?")`
   → `exit=78` раньше, чем через 5 с (не `124` через 70 с).
6. Недоступный сервер по-прежнему повторяется: `$DC stop sard-server; mk main ""; start; $DC start sard-server`
   → `exit=124` (агент работал до `timeout`); `grep -cF "$SUF" "$QA/serr"` → `0`;
   в stderr есть записи журнала `reconnecting`.

6а. Отозванный агент (`UNAUTHENTICATED`): отозвать агента в консоли (страница
   агента, «Отозвать») или `POST /api/v1/agents/<agent_id>/revoke` с сессией
   администратора (S8b); затем `mk main ""; start`
   → `exit=78`; `line` содержит `code = Unauthenticated`, `reason AGENT_REVOKED`
   и оканчивается `$SUF`. После этого шага агент зарегистрирован заново только
   через `sard-agent enroll --force` с новым токеном.

Отказы `FAILED_PRECONDITION` и `PERMISSION_DENIED` вручную не
воспроизводятся (нужен сервер другой версии); их проверяют тесты `@start`.

## Часть 2. Юнит systemd (OQ-001)

7. `grep -E '^(Restart|RestartPreventExitStatus)=' deploy/agent/sard-agent.service`
   → ровно `Restart=on-failure` и `RestartPreventExitStatus=78`.
8. В чистом контейнере дистрибутива с systemd и установленным пакетом (как
   часть 9 `docs/qa/repo-init.md`), служба не запускалась. Подменить запуск
   программой, которая выходит с кодом 78, не трогая остальной юнит:

```bash
sudo mkdir -p /etc/systemd/system/sard-agent.service.d
printf '[Service]\nExecStart=\nExecStart=/bin/sh -c "exit 78"\n' | sudo tee /etc/systemd/system/sard-agent.service.d/qa.conf
sudo systemctl daemon-reload; sudo systemctl start sard-agent; sleep 25
systemctl show -p NRestarts -p ActiveState -p ExecMainStatus sard-agent
```

   → `NRestarts=0`, `ActiveState=failed`, `ExecMainStatus=78`;
   `systemctl status sard-agent` показывает `status=78/CONFIG`.
9. То же с `exit 1` вместо `exit 78` (`sed -i 's/exit 78/exit 1/' …/qa.conf`,
   `daemon-reload`, `systemctl reset-failed sard-agent`, `start`, `sleep 25`)
   → `NRestarts` не меньше `2` (ошибки старта с кодом 1 systemd перезапускает).
   Удалить `qa.conf`, `daemon-reload`.

## Часть 3. Неподдерживаемый crypto_provider (OQ-050)

10. Для каждого значения `gost`, `RESTIC-AES`, `restic-aes ` (с пробелом в
    конце), `restic`: `mk main "<значение>"; start`
    → сразу `exit=1`; stderr — ровно одна строка (`wc -l < "$QA/serr"` → `1`),
    она начинается с `sard-agent: ` и содержит `CRYPTO_PROVIDER_UNSUPPORTED`,
    `crypto_provider`, `"offsite"`, значение в двойных кавычках и `restic-aes`;
    `grep -cF "$URLM" "$QA/serr"` → `0`; в stdout нет `connecting to`; в
    журнале сервера (`$DC logs sard-server --since 30s`) нет подключения агента.
11. `mk main "restic-aes"; start` → `connecting to localhost:9090`, `exit=124`.
    То же для `mk main ""` (crypto_provider не задан) → то же.
12. Раньше прав секретного файла: `mk main gost; chmod 0644 "$H/sec/main.pass"; start; chmod 0600 "$H/sec/main.pass"`
    → `exit=1`; stderr содержит `CRYPTO_PROVIDER_UNSUPPORTED` и не содержит
    `-rw-r--r--`.
13. Раньше restic: `mk main gost "$QA/nope-restic"; start`
    → `exit=1`; stderr содержит `CRYPTO_PROVIDER_UNSUPPORTED` и не содержит
    `RESTIC_NOT_FOUND`.
14. repo init не изменился: `mk main gost; rm -rf "$QA/repo"; "$AG" repo init --config "$H/agent.yaml" main; echo "exit=$?"`
    → `exit=0` (репозиторий main создан, crypto_provider offsite не мешает);
    `"$AG" repo init --config "$H/agent.yaml" offsite; echo "exit=$?"`
    → `exit=2`, сообщение называет `CRYPTO_PROVIDER_UNSUPPORTED`.
15. В контейнере части 2 (без `qa.conf`): в `/etc/sard/agent.yaml` репозиторий
    с `crypto_provider: gost`; `sudo systemctl start sard-agent; sleep 25`;
    `journalctl -u sard-agent --since -1min | grep -c CRYPTO_PROVIDER_UNSUPPORTED`
    → не меньше `2` (Р11: код 1, systemd перезапускает, каждый раз отказ до сети);
    сообщение называет имя репозитория и `"gost"`.
