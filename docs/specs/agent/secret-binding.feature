# language: ru
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright 2026 Artur Abalov
#
# S11. Привязка секретов к плагину и цели; защищённое подключение PostgreSQL.
#
# Пункт 6 внешнего ревью 2026-10-11, дополнение ADR 0008. Решения владельца
# контрольной точки 1 (2026-10-26) — в docs/sessions/2026-10-26-s11-secret-binding.md:
# решение принимает агент (sdk.Host.SecretFor); привязка — отдельный файл
# secret-bindings/<имя>.yaml рядом с главным конфигом; секретов без привязки
# нет; ломать текущие установки можно.
#
# СТАТУС: реализуется без pipeline по указанию владельца («Делай сам»).
# Тесты: agent/plugins/postgresql/binding_test.go, restore_binding_test.go,
# libpq_integration_test.go (-tags libpq); agent/internal/secretbind;
# agent/internal/pluginhost/secrets_test.go;
# agent/cmd/sard-agent/host_secret_binding_test.go.

Функция: Секрет уходит только туда, куда разрешил оператор хоста

  Правило: Секрет применяется только к плагину и цели из его привязки

    Сценарий: Цель вне привязки — шаг падает до подключения
      Дано секрет "pg-app" привязан к "postgresql" и цели "db:5432/app"
      Когда сервер присылает шаг с host "evil.example"
      Тогда шаг FAILED с причиной «secret "pg-app" is not allowed for postgresql at evil.example:5432/app»
      И в причине подсказка «sudo sard-agent secret set pg-app --plugin postgresql --target evil.example:5432/app»
      И ни одна программа не запущена, файл секрета не прочитан

    Сценарий: Секрет без привязки не отдаётся никому
      Дано секрет "pg-app" есть в конфиге, а файла привязки нет
      Когда приходит шаг с этим секретом
      Тогда шаг FAILED: «secret "pg-app" has no binding on this host and is not given to any plugin»

    Сценарий: Секрет, привязанный к другому плагину
      Тогда шаг FAILED: «secret "pg-app" is bound to the plugin files, not postgresql»

    Сценарий: Файл привязки, который может менять не root и не агент
      Дано файл привязки доступен на запись группе или прочим, или его владелец — другой пользователь
      Тогда шаг FAILED с причиной, которая называет секрет, но не путь

    Сценарий: Восстановление и откат прерванного восстановления спрашивают ту же привязку
      Тогда для R2 целью служит новая база на целевом сервере (host:port/имя_новой_базы)
      И отказ привязки — причина SECRET_NOT_ALLOWED (check) до запуска psql

  Правило: Пароль идёт только по SCRAM, к серверу в сети — только по проверенному TLS

    Сценарий: По TCP требуется SCRAM, по TLS — ещё и channel binding
      Тогда в строке подключения require_auth=scram-sha-256
      И при TLS — channel_binding=require
      И на сокете require_auth нет, если привязка не задаёт --auth

    Сценарий: Сервер просит password или md5
      Тогда libpq отказывается до отправки пароля
      И сообщение шага подсказывает перевести роль на SCRAM или разрешить --auth scram-sha-256,md5

    Сценарий: tls_mode слабее verify-full к серверу в сети
      Дано привязка не разрешает --allow-tls
      Когда tls_mode равен disable или require, а хост — не сокет и не localhost/127.0.0.0/8/::1
      Тогда шаг FAILED до запуска программ, причина называет tls_mode, секрет и --allow-tls

    Сценарий: По умолчанию сервер в сети проверяется по сертификату
      Тогда без tls_mode: verify-full с sslrootcert из --ca-file привязки, иначе tls_root_cert, иначе system

    Сценарий: Клиент PostgreSQL старше 16
      Тогда libpq отвергает require_auth до подключения
      И сообщение шага подсказывает поставить клиент 16 или новее

  Правило: Значение секрета нигде не появляется
    Сценарий: Ни одно сообщение, лог шага, вывод secret set/list и файл привязки не содержат значения

  Правило: secret set требует привязку
    Сценарий: Без --plugin или --target секрет не записывается (код 2)
    Сценарий: Неизвестный плагин — PLUGIN_UNKNOWN
    Сценарий: Привязка — файл root:<группа службы> 0640 в каталоге 0750, пишется до фрагмента
    Сценарий: secret list показывает BOUND_TO, --json — объект binding или null
    Сценарий: secret remove удаляет и привязку
