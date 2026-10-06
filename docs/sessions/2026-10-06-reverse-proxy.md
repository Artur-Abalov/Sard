<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright 2026 Artur Abalov -->

# Сессия 2026-10-06: консоль за обратным прокси (OQ-031, OQ-033, OQ-142)

Ветка `ccr-6dac0799-4ff2mq`, база — `main` @ `0860a05`.

## Повод

Владелец развернул сервер в OCI с Cloudflare Tunnel перед консолью. Войти не
получалось: `POST /api/v1/session` отвечал 403 `origin_rejected`. Причина —
`OriginGuardFilter` (ADR 0021) и `request.isSecure == false` за прокси,
завершающим TLS.

Обходной путь `SERVER_FORWARD_HEADERS_STRATEGY=native` в
`docker-compose.override.yml` владелец проверил на своём развёртывании: вход
заработал.

## Решение владельца

Своего фильтра нет. Штатная настройка становится поддерживаемой: переменная
`SARD_FORWARD_HEADERS`, по умолчанию `none`, тест, ADR и документация. Это
изменение конфигурации, поэтому `/ship-feature` не запускался (CLAUDE.md,
правило 4).

## Сделано

- Тест сначала: `server/src/test/kotlin/dev/sard/server/auth/ReverseProxyIntegrationTest.kt`.
  Два класса на реальном Tomcat:
  - с `SARD_FORWARD_HEADERS=native` — 5 тестов;
  - по умолчанию — 3 теста.

  До правки конфига: `8 tests completed, 4 failed`. Упали все проверки
  поведения за прокси, кроме «чужой Origin — 403», который проходил и раньше.
  После правки: 8 из 8.
- `application.yaml`: `server.forward-headers-strategy: ${SARD_FORWARD_HEADERS:none}`.
- `deploy/docker-compose.yml` и `deploy/.env.example`: переменная и предупреждение
  «8080 только на loopback».
- `ClientAddress.kt`: KDoc описывает новое поведение.
- ADR 0045, отметка в ADR 0021, `docs/adr/README.md`.
- `docs/operator/03-configuration.md` и `04-tls-and-names.md`: раздел про
  прокси, настройки для Cloudflare Tunnel, nginx и Caddy, проверка через `curl`.
  README тоже обновлён.
- Реестр: OQ-031, OQ-033, OQ-142 перенесены в «Закрыто».

## Проверено фактами

- Умолчание `internal-proxies` взято из `spring-boot-tomcat-4.1.1.jar`
  (`javap`, `TomcatServerProperties$Remoteip`): `192.168.0.0/16, 172.16.0.0/12,
  169.254.0.0/16, fc00::/7, 10.0.0.0/8, 100.64.0.0/10, 127.0.0.0/8, fe80::/10, ::1/128`.
- Адрес, с которого контейнер видит соединение на порт, опубликованный на
  `127.0.0.1`: `172.17.0.1`. Проверено через `inet_client_addr()` в контейнере
  `postgres:18-alpine`, Docker 29.8.2.

## Не проверено

- Ручной прогон за nginx и Caddy. Их настройки в документации взяты из
  документации самих прокси.
- Сеть compose, отличная от `172.16.0.0/12` (свой `default-address-pools`). Там
  прокси на хосте может оказаться недоверенным.
