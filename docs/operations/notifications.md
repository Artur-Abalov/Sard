<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright 2026 Artur Abalov -->

# Уведомления в Telegram

Сервер сообщает в Telegram о завершении запусков (S9a; что и каким текстом — S9b, ADR 0024). Решения — черновик ADR `docs/adr/00XX-draft-notifications.md`.

## Включить
1. Создайте бота у @BotFather и получите токен вида `123456789:AA…`.
2. Добавьте бота в чат или канал и узнайте его id (у групп и каналов он отрицательный, например `-1001234567890`).
3. В `deploy/.env` задайте оба значения и перезапустите сервер (`make down && make up`):

   ```text
   SARD_TELEGRAM_BOT_TOKEN=123456789:AA…
   SARD_TELEGRAM_CHAT_ID=-1001234567890
   ```

4. В журнале сервера при старте — `Notifications go through [telegram]`.

Одно из двух значений без другого — сервер не стартует и называет недостающую переменную. Оба пустые — сервер стартует, уведомления выключены, в журнале `Notifications are off: no channel …`. На этапе 1 один чат получает уведомления всех тенантов.

До S9b в журнале будет `Notifications are off: no NotificationFormatter bean (S9b)`: тексты уведомлений ещё не написаны.

## Как это работает
- Раз в `sard.notify.tick-interval` (10 с) сервер находит запуски, завершённые за последние `sard.notify.ttl` (24 ч), и ставит каждому строку в `notification_deliveries`, затем отправляет. Завершение запуска будит цикл сразу.
- Сервер, упавший после завершения запуска, отправит уведомление после старта, если с завершения прошло меньше `ttl`. Уведомление может прийти дважды (ответ Telegram потерян) — но не пропасть.
- Повторы: 429 — по `retry_after`; 5xx и сеть — 10 с, 20 с, … до 10 мин, не больше 8 попыток; прочие ошибки 4xx (неверный токен, бот не в чате) — без повторов.

## Наблюдать
- Метрики: `sard.notify.sent`, `sard.notify.retries`, `sard.notify.undelivered` (теги `channel`, `reason=failed|expired`), `sard.notify.pending`.
- Почему не доставлено:

  ```sql
  select run_id, channel, status, attempts, last_error, created_at
  from notification_deliveries where status in ('failed', 'expired') order by created_at desc;
  ```

  `last_error` — HTTP-статус и описание Telegram; токена в нём нет.

## Безопасность
- Токен не пишется в журнал сервера и в базу. Не включайте `-Djdk.httpclient.HttpClient.log` и `-Djdk.internal.httpclient.debug`: с ними HTTP-клиент JDK пишет в журнал адрес запроса, а в нём токен.
- Сервер ходит к `api.telegram.org` по HTTPS; прокси берётся из системных свойств JVM (`https.proxyHost`, `https.proxyPort`).
