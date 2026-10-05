# QA: уведомления о запусках в Telegram (S9b)

Сценарии: `docs/specs/server/run-notifications.feature` (утверждена
владельцем 2026-10-04, решения Р1–Р10; язык по умолчанию — en). Шаги с
пометкой `(Р…)` проверяют соответствующее решение владельца. Доставка
(очередь, повторы, Bot API) — S9a, черновик ADR `docs/adr/00XX-draft-notifications.md`.

Здесь вручную проходятся сценарии с тегом `@qa` и сквозной путь «кнопка в
консоли → агент → сервер → сообщение в настоящем Telegram». Точные форматы
размеров и длительности, обрезка причины, гонки и управляемые часы
проверяются тестами `@unit`, `@server`, `@startup` с теми же названиями.

Ожидаемый результат указан после «→» в каждом шаге. Любое расхождение — дефект.

## Подготовка

Нужны: Docker, Go, `restic` 0.19.1, `curl`, `jq`; Telegram-клиент; бот от
@BotFather и чат (группа), в который бот добавлен; id этого чата. Команды — из
корня репозитория, **не от root** (шаг с нечитаемыми файлами).

1. Выполнить «Подготовку» из `docs/qa/files-plugin.md` (сервер, агент,
   репозиторий `qa`, переменные `AG`, `H`, `QA`, `DC`, `PSQL`, `API`, `AGENT`,
   `D`, функции `src`, `run`, `rs`; сессия в `$QA/jf`).
2. Включить бота, публичный адрес консоли и русский язык (по умолчанию
   en, Р2; части 1–2 проверяют русские тексты, часть 3 — умолчание):

```bash
TG_TOKEN='<токен бота>'; TG_CHAT='<id чата>'
sed -i '/^#\? *SARD_TELEGRAM_BOT_TOKEN=/d; /^#\? *SARD_TELEGRAM_CHAT_ID=/d; /^#\? *SARD_CONSOLE_PUBLIC_URL=/d; /^#\? *SARD_NOTIFY_LANGUAGE=/d' deploy/.env
printf 'SARD_TELEGRAM_BOT_TOKEN=%s\nSARD_TELEGRAM_CHAT_ID=%s\nSARD_CONSOLE_PUBLIC_URL=http://localhost:5173/\nSARD_NOTIFY_LANGUAGE=ru\n' "$TG_TOKEN" "$TG_CHAT" >> deploy/.env
$DC up -d --wait server
# dlv <run-id> — статус доставки уведомления запуска
dlv() { $PSQL "select status||'|'||attempts||'|'||coalesce(last_error,'') from notification_deliveries where run_id='$1'"; }
# waitdlv <run-id> — ждёт, пока доставка не станет финальной (до 60 с), печатает её
waitdlv() { for i in $(seq 1 60); do s=$(dlv $1); case $s in pending*|'') sleep 1;; *) break;; esac; done; dlv $1; }
```

   → сервер `healthy`; `$DC logs server | grep -c 'Notifications go through \[telegram\]'` → `1`;
   `$DC logs server | grep -c 'no NotificationFormatter bean'` → `0`.

## Часть 1. Успех

3. В консоли (`cd web && npm run dev`, http://localhost:5173) создать
   источник `qa-ok` агента с плагином `files`, путями `[$D]`, репозиторием
   `qa`, и нажать «Запустить бэкап». Запомнить id запуска из адреса страницы
   (`RUN=…`). Закрыть вкладку консоли.
   → в течение 30 с после завершения запуска в чат приходит **одно**
   сообщение.
4. Прочитать сообщение → строки по порядку:
   - `✅ Бэкап выполнен: qa-ok` (жирным);
   - `Агент: <hostname агента>` (hostname моноширинным; совпадает с
     `$PSQL "select hostname from agents where id='$AGENT'"`);
   - `Длительность: …` — совпадает со строкой «Длительность» на странице
     запуска в консоли;
   - `Всего: 1 КиБ, добавлено: …` — совпадает с «Всего» и «Добавлено» на
     странице запуска;
   - `Запуск: $RUN` (моноширинным);
   - `http://localhost:5173/runs/$RUN` — одна косая черта перед `runs`.
5. Нажать ссылку из сообщения → открывается страница этого запуска в консоли.
6. `waitdlv $RUN` → `delivered|0|`. Подождать 60 с, затем
   `$PSQL "select count(*) from notification_deliveries where run_id='$RUN'"` → `1`;
   новых сообщений о запуске в чате нет.
7. Пока запуск шага 3 шёл (или запустить ещё один на большом дереве из
   `files-plugin.md`, шаг 18), в чате не было сообщений о начале и ходе
   запуска → сообщение одно, после завершения.

## Часть 2. Ошибки

8. **Ошибка без снимка.** `R=$(run $(src "{\"paths\":[\"$D\",\"$QA/missing\"]}") >/dev/null; echo $RUN); waitdlv $R`
   → `delivered|…`; в чате сообщение:
   - первая строка `❌ Бэкап завершился ошибкой: qa-…`;
   - строка `Причина: …` — текст причины совпадает символ в символ с
     `curl -sS -b $QA/jf $API/runs/$R | jq -r .message`;
   - строки о снимке и строки «Всего» нет;
   - `Запуск: $R` и ссылка на `/runs/$R`.
9. **Ошибка со снимком** (правило 4). `mkdir -p $D/bad; for i in $(seq 1 11); do echo x > $D/bad/f$i; chmod 0000 $D/bad/f$i; done; R=$(run $(src "{\"paths\":[\"$D\"]}") >/dev/null; echo $RUN); waitdlv $R; chmod 0600 $D/bad/*`
   → первая строка `❌ Бэкап завершился ошибкой: …`; после причины —
   `Снимок создан и пригоден для восстановления, но часть данных в него не попала.`,
   затем `Всего: …, добавлено: …` (Р6) — как на странице запуска.
10. **Длинная причина** (Р3). Сообщение шага 9 длиннее 500 символов?
    `curl -sS -b $QA/jf $API/runs/$R | jq -r '.message | length'`.
    Если больше 500 → причина в сообщении кончается на `…`, следующая строка
    `Полный текст — в консоли.`; в консоли на странице запуска виден полный
    текст. Если 500 или меньше → метки и строки о консоли нет (обрезка тогда
    проверяется только тестами).
11. **Отклонён агентом.** Остановить агента (`kill $AGPID`). Создать
    источник и запуск: `S=$(src "{\"paths\":[\"$D\"]}"); R=$(curl -sS -b $QA/jf -X POST $API/sources/$S/runs | jq -r .id)`;
    подменить плагин шага: `$PSQL "update run_steps set plugin='absent' where run_id='$R'"`;
    запустить агента снова (`"$AG" --config "$H/agent.yaml" >> $QA/agent.out 2>&1 & AGPID=$!`); `waitdlv $R`
    → первая строка `❌ Бэкап отклонён агентом: …`; `Причина: unknown plugin "absent"`;
    строки «Длительность» нет (Р5).
12. **Потерян.** Запустить бэкап большого дерева (`files-plugin.md`, шаг 18)
    и, когда шаг в фазе `uploading`, убить агента: `kill -9 $AGPID`; если окно
    потери S6a требует переподключения агента — запустить его снова. Дождаться,
    пока шаг станет `lost` (`curl -sS -b $QA/jf $API/runs/$R | jq -r '.steps[0].status'`;
    если шаг закончился иначе — повторить, сценарий также покрыт тестом
    `@server`), затем `waitdlv $R`
    → первая строка `❌ Бэкап потерян: …`; `Причина: agent lost the step`;
    следующая строка `Связь с агентом прервалась, пока шаг выполнялся. Можно запустить бэкап снова.`;
    строки о снимке нет. Запустить агента снова и начать запуск того же
    источника → запуск принимается (не 409).

## Часть 3. Без ссылки, другой язык, адрес запроса

13. Убрать адрес консоли и настройку языка — проверить умолчание en (Р2):
    `sed -i '/^SARD_CONSOLE_PUBLIC_URL=/d; /^SARD_NOTIFY_LANGUAGE=/d' deploy/.env; $DC up -d --wait server`.
    Запустить бэкап запросом с чужим хостом:
    `RUN=$(curl -sS -b $QA/jf -X POST $API/sources/<id источника qa-ok>/runs -H 'X-Forwarded-Host: evil.example' -H 'X-Forwarded-Proto: https' | jq -r .id)`;
    дождаться завершения, `waitdlv $RUN`
    → сообщение начинается с `✅ Backup succeeded: qa-ok`; строки
    `Agent:`, `Duration:`, `Total: 1 KiB, added: …`, последняя —
    `Run: $RUN`; ссылки нет, `evil.example` нигде нет.
14. Вернуть русский язык (части 4–5 ждут русских текстов):
    `echo SARD_NOTIFY_LANGUAGE=ru >> deploy/.env`.
    `SARD_NOTIFY_LANGUAGE=de $DC up -d server; sleep 20; $DC logs server | tail -50`
    → сервер не стартует; сообщение называет `sard.notify.language` и `ru`, `en`.
15. `SARD_CONSOLE_PUBLIC_URL=sard.example.com $DC up -d server; sleep 20; $DC logs server | tail -50`
    → сервер не стартует; сообщение называет `sard.console.public-url`.
    Затем `$DC up -d --wait server` → `healthy`.

## Часть 4. Сбой доставки и режим без бота

16. **Неверный чат.** `SARD_TELEGRAM_CHAT_ID=-1 $DC up -d --wait server`;
    запустить бэкап `qa-ok`, дождаться завершения, `waitdlv $RUN`
    → статус `failed`, `last_error` начинается с `HTTP 400` и содержит `chat not found`; запуск в REST — `succeeded`;
    `$DC logs server | grep "Notification of run $RUN through telegram failed"` →
    одна строка, в ней `chat not found`; `$DC logs server | grep -cF "$TG_TOKEN"` → `0`.
17. **Без бота.** `SARD_TELEGRAM_BOT_TOKEN= SARD_TELEGRAM_CHAT_ID= $DC up -d --wait server`;
    запустить три бэкапа `qa-ok` по очереди, дождаться завершения каждого
    → сервер `healthy`, запуски `succeeded`;
    `$DC logs server | grep -c 'Notifications are off: no channel'` → `1`;
    `$DC logs server | grep -E 'WARN|ERROR' | grep -ci 'notif'` → `1`
    (та же строка); в чат ничего не пришло;
    `$PSQL "select count(*) from notification_deliveries where created_at > now() - interval '5 minutes'"` → `0`.
18. Вернуть бота: `$DC up -d --wait server` (значения из `deploy/.env`)
    → в течение 30 с в чат приходят сообщения о трёх запусках шага 17
    (они завершились меньше 24 ч назад — поведение S9a), по одному на запуск,
    у каждого свой `Запуск: …`.

## Часть 5. Секреты и конфигурация

19. Для всех запусков этой процедуры:
    `$PSQL "select config::text from sources" | grep -o "$QA[^\"]*" | sort -u`
    — пути из конфигов. Ни один из этих путей и ни одно имя репозитория
    (`qa`) как отдельное значение не встречается в сообщениях чата, кроме
    случая, когда путь входит в текст причины от агента (шаги 8, 9) →
    подтверждено просмотром чата; в сообщениях нет токена бота.

## Часть 6. Документация и развёртывание

20. `docs/operations/notifications.md` → описывает тексты сообщений (оба
    языка, по итогам), `SARD_CONSOLE_PUBLIC_URL` и `SARD_NOTIFY_LANGUAGE`
    (значения `en` — по умолчанию — и `ru`); говорит, что запуски не вручную
    на этапе 1 не уведомляются;
    строки «До S9b … no NotificationFormatter bean» нет.
21. `grep -E 'SARD_CONSOLE_PUBLIC_URL|SARD_NOTIFY_LANGUAGE' deploy/.env.example`
    → обе переменные есть, закомментированы или пусты, с пояснением.

## Завершение

22. `kill $AGPID`; `chmod -R u+rwX $QA`; удалить бота из чата или отозвать
    токен у @BotFather, если он создавался только для QA.
