# 9. Диагностика

## Где смотреть

```bash
cd ~/sard
docker compose ps                                  # состояние и health контейнеров
docker compose logs --tail=200 server              # лог сервера
docker compose logs --tail=50 postgres
curl -fsS http://localhost:8080/actuator/health    # {"groups":[...],"status":"UP"}
curl -fsS http://localhost:8080/api/v1/status      # версия
```

На хосте агента:

```bash
systemctl status sard-agent
journalctl -u sard-agent --since '-1h'
```

Секреты (пароли, токены, ключи) в логи не пишутся ни сервером, ни агентом.

## Сервер не стартует

Причина — первая строка исключения в `docker compose logs server`.

| В логе | Что сделать |
|---|---|
| `SARD_ADMIN_PASSWORD is not set` / `must be at least 12 characters` | задать пароль в `.env` ([раздел 3](03-configuration.md)) |
| `SARD_AGENT_ENDPOINT host '…' is not covered by any of …` | добавить хост в `SARD_PKI_SERVER_NAMES` или поправить `SARD_AGENT_ENDPOINT` |
| `SARD_AGENT_ENDPOINT is not a valid host or host:port` | без схемы и пути: `sard.example.com:9090` |
| `… SARD_TELEGRAM_CHAT_ID is not set` (или `BOT_TOKEN`) | задать обе переменные Telegram или очистить обе |
| `SARD_TELEGRAM_BOT_TOKEN is not a bot token (digits:secret)` | токен из @BotFather целиком |
| `SARD_AGENT_DOWNLOADS_URL must be an absolute http or https address …` | `https://имя` без пути-запроса-фрагмента, или пусто |
| `CA import refused: IMPORT_SOURCE_MISSING` | `SARD_PKI_IMPORT_DIR` указывает на несуществующий путь или файл: проверьте монтирование в `docker-compose.override.yml` |
| `CA import refused: IMPORT_FILE_MISSING` | в источнике нет `ca/ca.crt` или `ca/ca.key`; сообщение называет ожидаемый путь (файлы лежат в подкаталоге `ca`) |
| `CA import refused: IMPORT_FILE_UNREADABLE` | сервер (uid 10001) не может прочитать файл или каталог: `chown -R 10001:10001` |
| `CA import refused: IMPORT_PERMISSIONS_TOO_OPEN` | права шире владельца: `chmod 700` на источник и `ca`, `chmod 600` на оба файла |
| `CA import refused: CA_CERT_INVALID` | `ca.crt` — не ровно один сертификат X.509 в PEM (пустой файл, два сертификата, ключ вместо сертификата) |
| `CA import refused: CA_KEY_INVALID` | `ca.key` — не незашифрованный PKCS#8 PEM (`BEGIN PRIVATE KEY`); `openssl pkcs8 -topk8 -nocrypt` |
| `CA import refused: CA_KEY_UNSUPPORTED` | ключ не ECDSA P-256; CA Sard другого типа не бывает, проверьте, тот ли это архив |
| `CA import refused: CA_KEY_MISMATCH` | ключ от другого сертификата: файлы из разных архивов |
| `CA import refused: CA_NOT_SELF_SIGNED` | сертификат подписан другим CA (промежуточный); нужен корневой CA Sard |
| `CA import refused: CA_NOT_A_CA` | у сертификата нет `basicConstraints CA:TRUE` |
| `CA import refused: CA_KEY_USAGE` | `keyUsage` сертификата без `keyCertSign` |
| `CA import refused: CA_NOT_YET_VALID` | сертификат ещё не действует по часам сервера: проверьте время на машине |
| `CA import refused: CA_EXPIRED` | срок CA истёк, импортировать нечего: агентов регистрируют заново с новым CA |
| `CA import refused: IMPORT_WRITE_FAILED` | не удалось записать в `SARD_PKI_DIR` (диск полон, том только для чтения); в каталоге CA ничего не осталось |
| `CA import refused: CA_ALREADY_PRESENT` | в `SARD_PKI_DIR` уже другой CA, чем в источнике (сообщение называет оба отпечатка): уберите `SARD_PKI_IMPORT_DIR` или очистите том, если это ошибочный старт |
| права каталога PKI шире владельца | восстановление ключа CA с неверными правами: повторить команду `chown`/`chmod` из [раздела 6](06-data-and-backup.md#восстановление) |
| ошибка подключения к базе, `password authentication failed` | `SARD_DB_PASSWORD` в `.env` не совпадает с паролем, с которым том базы создан впервые: том помнит первый пароль |
| `set SARD_… in .env` от `docker compose` | обязательная переменная пустая: `SARD_VERSION`, `SARD_DB_PASSWORD`, `SARD_ADMIN_PASSWORD` |

## Агент не подключается

Агент остановился с `reason CERT_UNKNOWN` (в логе сервера — тенант и пояснение
«в базе записи нет»): сервер с вашим CA не нашёл запись о сертификате агента,
то есть база не восстановлена или старше его регистрации. Восстановите базу и
выполните на хосте `sudo systemctl restart sard-agent`
([раздел 8](08-migrate-and-remove.md)).

Проверяйте по порядку на хосте агента:

1. **Порт.** `nc -vz <sard.example.com> 9090` (или
   `timeout 3 bash -c '</dev/tcp/<sard.example.com>/9090' && echo open`). Закрыт —
   облачный фаервол и фаервол хоста сервера ([раздел 1](01-requirements.md)).
2. **Имя.** `server.address` в `/etc/sard/agent.yaml` должен быть одним из
   `SARD_PKI_SERVER_NAMES`; проверка имён — [раздел 4](04-tls-and-names.md).
   Ошибка TLS про имя хоста в `journalctl -u sard-agent` — именно это.
3. **Время.** `timedatectl` на обоих хостах: «System clock synchronized: yes».
   Сдвиг больше 30 с сервер пишет в лог (`clock skew`); большой сдвиг ломает
   проверку сертификатов.
4. **Отзыв.** Отозванный в консоли агент получает отказ при подключении; он
   регистрируется заново новым токеном (`enroll --force`).
5. **Прокси.** Между агентом и портом 9090 нет ничего, что завершает TLS
   ([раздел 4](04-tls-and-names.md)).
6. **Регистрация.** Коды выхода `sard-agent enroll` и что с каждым делать —
   [регистрация агента](../operations/agent-enroll.md).

## Консоль

| Симптом | Причина |
|---|---|
| после перезапуска сервера снова страница входа | сессии в памяти сервера, перезапуск их сбрасывает — так задумано |
| вход отвечает 403 `origin_rejected` | консоль открыта через прокси с TLS или с переписанным `Host` — не поддерживается на этапе 1; SSH-туннель ([раздел 4](04-tls-and-names.md)) |
| вход заблокирован после нескольких неверных паролей | блокировка перебора; ждать или перезапустить сервер |
| команды установки агента не работают с другого хоста | порт 8080 на `127.0.0.1` (OQ-144), [раздел 4](04-tls-and-names.md), «Раздача пакетов агента» |

## Запуск бэкапа

- **Шаг FAILED, «permission denied».** Агент работает от `sard-agent` и видит
  только то, что этому пользователю можно читать ([плагин files](../plugins/files.md)).
- **Шаг «потерян».** Связь с агентом пропала во время шага: агент перезапущен,
  хост выключен, сеть.
- **Агент завис при старте с локальным репозиторием.** Каталог репозитория не
  добавлен в `ReadWritePaths` службы ([репозиторий агента](../operations/repo-init.md)).
- **Нет уведомления в Telegram.** В логе сервера при старте —
  `Notifications are off`, если переменные не заданы; бот должен быть
  добавлен в чат и иметь право писать.
