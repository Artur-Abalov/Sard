# 8. Переезд и удаление

## Переезд на другую ВМ с сохранением CA

Агенты продолжают работать без повторной регистрации, если на новой машине
тот же CA, восстановленная база и имя, по которому они подключаются.
Неиспользованные токены регистрации старого сервера действуют, если они есть
в восстановленной базе.

CA на новую машину переносится **импортом**: новый сервер при первом старте
берёт CA из каталога `SARD_PKI_IMPORT_DIR`, проверяет его и кладёт в свой
том так же, как сгенерированный. Источник сервер не меняет.

1. Новая машина — по [разделу 2](02-install.md), шаги 1–3, **без запуска**:
   скопируйте туда старый `.env` вместо нового (те же пароли, имена, версия).
2. На старой машине — бэкап по [разделу 6](06-data-and-backup.md) и остановка:

   ```bash
   cd ~/sard && docker compose stop server
   ```

   (бэкап после остановки сервера — чтобы в дампе было всё до последнего
   запуска). Запишите отпечаток CA: окончание любой строки токена после
   последней точки, страница «Токены» консоли или команда из раздела 6.
3. Перенесите на новую машину `sard-db-*.dump`, `sard-pki-*.tgz` и `.env`
   (`scp`), положите в `~/sard`.
4. **Сначала база.** Восстановите её до первого запуска сервера:

   ```bash
   cd ~/sard
   docker compose up -d --wait postgres
   docker compose exec -T postgres psql -U sard -d postgres \
     -c 'DROP DATABASE sard WITH (FORCE)' -c 'CREATE DATABASE sard OWNER sard'
   docker compose exec -T postgres pg_restore -U sard -d sard --no-owner < sard-db-*.dump
   ```

5. **Подготовьте источник CA.** Раскладка — как у каталога CA:
   `pki-import/ca/ca.crt` и `pki-import/ca/ca.key`. Сервер работает от
   пользователя с uid 10001, права — только владельцу (с более широкими
   сервер откажется):

   ```bash
   cd ~/sard
   mkdir pki-import && tar -C pki-import -xzf sard-pki-*.tgz
   sudo chown -R 10001:10001 pki-import
   sudo chmod 700 pki-import pki-import/ca && sudo chmod 600 pki-import/ca/*
   ```

6. **Подключите источник** файлом `~/sard/docker-compose.override.yml`
   (только для чтения; Compose подхватывает его сам):

   ```yaml
   services:
     server:
       environment:
         SARD_PKI_IMPORT_DIR: /var/lib/sard/pki-import
       volumes:
         - ./pki-import:/var/lib/sard/pki-import:ro
   ```

7. Запустите сервер: `docker compose up -d --wait`. В логе должна быть строка
   `CA imported from /var/lib/sard/pki-import: fingerprint=… origin=imported`:

   ```bash
   docker compose logs server | grep -E 'CA imported|fingerprint='
   ```

   Отпечаток в ней равен записанному на старой машине. Если сервер не
   стартует, строка `CA import refused: <ПРИЧИНА>` называет причину и путь;
   причины и что исправить — в [разделе 9](09-troubleshooting.md). После отказа
   в томе CA нет, исправьте источник и запустите снова.
8. Переведите имя на новую машину (DNS-запись или перенос IP) и откройте
   порты ([раздел 1](01-requirements.md), «Сеть»). Агенты переподключатся сами,
   когда имя начнёт указывать на новую машину.
9. Проверка: в консоли агенты «в сети», их запуски проходят, на странице
   «Токены» отпечаток тот же.
10. Когда всё работает, уберите `docker-compose.override.yml` и источник
    `pki-import` (в нём ключ CA — храните его только в сейфе) и
    `docker compose up -d --force-recreate --wait server`. Можно и оставить:
    при следующих стартах сервер видит тот же CA и ничего не импортирует. Если
    в источнике другой CA, сервер не стартует (`CA_ALREADY_PRESENT`).

Сертификат сервера выпускается заново на имена `SARD_PKI_SERVER_NAMES`
**нового** сервера, а не старого. Если имя меняется, добавьте новое в
`SARD_PKI_SERVER_NAMES`, оставив старое, поправьте `SARD_AGENT_ENDPOINT`,
переведите агентов (`server.address` в `/etc/sard/agent.yaml`, перезапуск
`sudo systemctl restart sard-agent`), и только потом уберите старое
([раздел 4](04-tls-and-names.md)).

### Агенты подключились до восстановления базы

Если имя уже указывает на новый сервер, а база ещё не восстановлена, агент
получает отказ `CERT_UNKNOWN` и **останавливается** (код выхода 78, systemd не
перезапускает его). В логе сервера:
`agent call refused: reason=CERT_UNKNOWN serial=… agent=… tenant=… method=…: the certificate was issued by this server's CA, but the database has no record of it: the database was not restored or was restored from a copy older than the agent's enrollment`.
Остановите сервер, восстановите базу (шаг 4), запустите сервер и на хосте
каждого агента выполните:

```bash
sudo systemctl restart sard-agent
```

## Удаление

```bash
cd ~/sard
docker compose down -v          # контейнеры и оба тома: база и ключ CA
docker image rm "$(docker compose config --images | grep sard-server)" postgres:18-alpine
rm -f .env docker-compose.yml
```

`down -v` необратим: без бэкапа ключа CA все агенты придётся регистрировать
заново. Без `-v` тома остаются, и `docker compose up -d` поднимет сервер с
прежними данными. Агенты на хостах удаляются отдельно (`sudo apt remove
sard-agent` или `sudo dpkg -r sard-agent`); репозитории restic и их пароли
остаются на месте.
