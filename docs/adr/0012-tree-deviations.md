# 0012 — Расхождения с целевым деревом каталогов

- Статус: принято
- Дата: 2026-09-27

Дерево совпадает с целевым из задачи; добавлено следующее:

| Добавлено | Причина |
|---|---|
| `tools/` (Go-модуль вне `go.work`) | Закреплённые версии buf, protoc-плагинов, golangci-lint, go-mutesting и собственная утилита CRAP (ADR 0007). |
| `proto/jvm/` (Gradle-проект `:proto-jvm`) | JVM-привязки контракта вынесены из `server` из-за конфликта protobuf-плагина и mutflow (ADR 0010). |
| `agent/internal/config`, `agent/internal/app` | Разбор YAML-конфига и композиция жизненного цикла агента — отдельные тестируемые пакеты, а не код в `main`. |
| `agent/plugins/builtin.go` | Реестр встроенных плагинов (пакет `plugins`). |
| `deploy/server/`, `deploy/agent/` | Dockerfile сервера и файлы агента (systemd-юнит, пример конфига) разложены по компонентам. |
| `scripts/` | Шлюз качества, проверка лицензий, хуки Claude Code. |
| `docs/adr`, `docs/sessions`, `docs/legal`, `docs/dependencies.md`, `docs/claude-setup.md` | Требования задачи. |
| корневые `settings.gradle.kts`, `build.gradle.kts`, `gradle/`, `gradlew` | Gradle-сборка из корня объединяет `server` и `proto-jvm`, `./gradlew` работает из корня репозитория. |
