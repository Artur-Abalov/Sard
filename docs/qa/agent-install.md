<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright 2026 Artur Abalov -->

# QA: установка агента с сервера Sard (U1b)

Сценарии: `docs/specs/server/agent-install.feature`,
`docs/specs/web/agent-install.feature`, `docs/specs/agent/agent-install.feature`.
Пометка `[ВN]` у шага называет решение владельца 2026-10-05 из заголовка
серверной спецификации, которое шаг проверяет.

Ожидаемый результат — после «→». Любое расхождение — дефект.

## Подготовка

Нужны: Docker, `curl`, `jq`, браузер, контейнер Ubuntu 24.04 с systemd без
выхода в интернет (сеть только до сервера), `minisign` на машине проверяющего.

```bash
make down; make up                       # образ сервера несёт пакеты своей версии (U1a)
API=http://localhost:8080/api/v1
PW=$(sed -n 's/^SARD_ADMIN_PASSWORD=//p' deploy/.env)
QA=$(mktemp -d)
curl -sS -X POST "$API/session" -H 'Content-Type: application/json' \
  -d "{\"password\":\"$PW\"}" -c "$QA/jar" -o /dev/null -w '%{http_code}\n'
api() { curl -sS -b "$QA/jar" "$@"; }
V=$(curl -sS http://localhost:8080/downloads/agent/manifest.json | jq -r .version)
```

→ вход — `204`; `$V` — версия сервера (`curl -sS $API/status` без сессии
отвечает той же `version`).

## Часть 1. REST

1. `curl -sS -o /dev/null -w '%{http_code}\n' "$API/agent-install"` → `401`.
2. `api "$API/agent-install" | jq '{format, arch, agentVersion, resticVersion, downloadsEnabled}'`
   → `deb`, `amd64`, `$V`, версия из `agent/internal/restic/restic-version`, `true`.
3. `api "$API/agent-install" | jq -r '.steps[].kind'` → по строке:
   `download checksum signature install configure enroll repo-init start`
   (в образе `make up` подписи нет → без `signature`, см. шаг 9).
4. `api "$API/agent-install" | jq '[.steps[] | select(.optional)] | map(.kind)'`
   → `[]` в неподписанном образе; `["signature"]` в релизном.
5. `api "$API/agent-install?arch=arm64&format=tar" | jq -r '.steps[0].commands[]'`
   → скачивается `sard-agent_${V}_linux_arm64.tar.gz`.
6. `api -o /dev/null -w '%{http_code}\n' "$API/agent-install?arch=riscv64"` → `422`,
   ошибка у поля `arch`; то же `format=rpm` → `422`, поле `format`.
7. `api "$API/agent-install" | jq -r '.steps[] | select(.kind=="enroll") | .commands[]'`
   → `sudo -u sard-agent sard-agent enroll --server localhost:9090 --token <...>`
   с заполнителем; адрес совпадает с `enrollCommand` из
   `api -X POST -H 'Content-Type: application/json' -d '{}' "$API/enrollment-tokens" | jq -r .enrollCommand`
   до `--token`.
8. `api "$API/agent-install" | grep -c 'sard_'` → `0` (строки токена нет),
   при том что активный токен из шага 7 существует.
9. `api "$API/agent-install" | jq '{signed, releaseKey}'` → `signed: false` в
   образе `make up`; `releaseKey.id` = `DF5D5B6DB257DBFA`, `publicKey` совпадает
   со строкой в `README.md` `[В5]`.
10. `api -H 'Host: evil.example' -H 'X-Forwarded-Host: evil.example' "$API/agent-install" | grep -c evil`
    → `0`.
11. `[В1]` Адрес раздачи: `api "$API/agent-install" | jq -r '.steps[0].commands[]'`
    → ссылки `http://localhost:8080/downloads/agent/...`. Перезапуск с
    `SARD_AGENT_DOWNLOADS_URL=https://sard.corp.example` → ссылки
    `https://sard.corp.example/downloads/agent/...`; с `ftp://x` → сервер не
    стартует, в логе `SARD_AGENT_DOWNLOADS_URL`.
12. Перезапуск с `SARD_AGENT_DOWNLOADS=false`:
    `api "$API/agent-install" | jq '{downloadsEnabled, steps, resticVersion, manualInstallDoc}'`
    → `false`, `[]`, `null`, ссылка на документацию ручной установки. Вернуть `true`.

## Часть 2. Хост: установка командами из ответа

13. В контейнере-хосте выполнить шаги `download` и `checksum` из шага 3
    (адрес раздачи доступен из контейнера) → коды `0`, `sha256sum` печатает
    `<файл>: OK`.
14. Изменить байт в пакете (`printf x | dd of=<пакет> bs=1 seek=100 conv=notrunc`)
    и повторить `checksum` → код не `0`, в выводе имя пакета и `FAILED`.
    Скачать пакет заново.
15. Шаг `install` → код `0`; в выводе установки — команда `enroll` от имени
    `sard-agent` и указание взять токен в консоли Sard.
16. `systemctl is-enabled sard-agent; systemctl is-active sard-agent` →
    `disabled`, `inactive`. Через 60 с
    `systemctl show -p NRestarts sard-agent` → `NRestarts=0`.
17. `[В2]` Шаг `configure` → код `0`; `/etc/sard/agent.yaml` есть,
    `server.address` равен адресу из команды enroll. Повторить шаг → файл не
    изменился (`sha256sum` до и после совпадает). Отредактировать репозиторий
    `main` на локальный каталог хоста.
18. В консоли создать токен; шаг `enroll` из диалога → код `0`.
19. Шаг `repo-init` → код `0`, напоминание сохранить файл пароля.
20. Шаг `start` → код `0`; в консоли агент онлайн с версией `$V`.
21. Хост без выхода в интернет: `curl -m 5 https://github.com` в контейнере
    → ошибка; шаги 13–20 всё равно прошли.
22. `[В3]` Повторить 13–20 на чистом Debian 12 с `format=tar` → тот же
    результат; `stat -c '%a %U' /etc/sard/tls /etc/sard/secrets /var/cache/sard/restic`
    → `700 sard-agent` у каждого.

## Часть 3. Подпись (релизный образ)

23. Собрать релизный каталог с тестовым ключом (`scripts/test-release-signing.sh`
    выводит, как) или взять образ `ghcr.io/artur-abalov/sard-server:<версия>`.
    `api "$API/agent-install" | jq '.signed, ([.steps[].kind] | index("signature"))'`
    → `true`, номер шага после `checksum`.
24. Шаг `signature` с установленным `minisign` → код `0`, доверенный
    комментарий `sard-agent <версия>`.
25. Изменить строку в `SHA256SUMS` и повторить шаг 24 → код не `0`.

## Часть 4. Устаревшие агенты и обновление

26. Собрать пакеты и образ версии `v0.0.1`, поставить агента по части 2,
    затем `make up` с версией `v0.0.2`.
    `api "$API/agents" | jq '.items[] | {hostname, agentVersion, outdated}'` →
    `v0.0.1`, `outdated: true` `[В4]`.
27. `api "$API/agents/<id>/upgrade?format=deb" | jq -r '.steps[].kind'` →
    `download checksum upgrade` (и `signature` в подписанном образе); пакет —
    архитектуры агента.
28. Выполнить шаги на хосте → агент онлайн с тем же `id`, версия `v0.0.2`;
    `outdated: false`; `sha256sum /etc/sard/agent.yaml /etc/sard/tls/* /etc/sard/secrets/*`
    до и после совпадает; в выводе установки нет команды `enroll` `[В8]`.
29. Агент, приславший `agent_version` `v0.0.3` (сборка новее) → `outdated: false`;
    `v0.0.1-3-gabc1234` → `false`.
30. `api -o /dev/null -w '%{http_code}\n' "$API/agents/00000000-0000-7000-8000-000000000000/upgrade"` → `404`.

## Часть 5. Консоль (дважды: на моках `VITE_API_MOCKS=1` и против `make up`)

31. Пустой тенант → `/agents`: блок установки раскрыт, выбраны deb и amd64,
    у deb подпись «Debian, Ubuntu, Astra», версии агента и restic показаны.
32. Выбрать arm64 → в Network запрос `agent-install?arch=arm64...`, команды на
    экране совпадают с ответом символ в символ.
33. Нажать «копировать» у шага checksum, вставить в текстовое поле → ровно
    команды шага из ответа.
34. Шаг подписи: помечен необязательным, показаны ID и строка ключа, текст о
    сверке с README и ссылка на README; нужен `minisign`. В неподписанном
    образе вместо шага — текст о неподписанной сборке.
35. Создать токен → диалог: все шаги от скачивания до запуска, в шаге enroll —
    `enrollCommand` ответа со строкой токена. Закрыть диалог → в блоке на
    `/agents` у enroll снова заполнитель.
36. `[В7]` С агентами в тенанте → блок на `/agents` свёрнут и раскрывается.
37. Устаревший агент из части 4 → в списке пометка «доступно обновление» с
    версией, цвет notice, статус «онлайн». Карточка — шаги обновления, напоминание
    об активных запусках, для deb — пояснение о конфиге и ключах. Неустаревший —
    блока нет.
38. `SARD_AGENT_DOWNLOADS=false` → блок объясняет, что раздача выключена, есть
    ссылка на документацию, команд и выбора нет.
39. На моках сделать ответ `agent-install` 503 → в блоке ошибка, список агентов
    показан.
40. Язык RU, шаги 31–38 → нет ключей i18n и английского текста, кроме команд,
    имён файлов и версий. То же для EN — нет русского.
