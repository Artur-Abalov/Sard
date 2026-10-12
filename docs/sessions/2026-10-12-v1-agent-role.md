<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright 2026 Artur Abalov -->

# 2026-10-12 — V1: роль агента в токене и сертификате, идентификатор машины

Ветка `ccr-6c6f4c08-945jg7`: её назначила сессия. В задаче указана
`feat/v1-agent-role`, переименовать можно по решению владельца. Это продуктовая
возможность, поэтому после контрольной точки 1 работа идёт по `/ship-feature`.
#61 и #62 уже в `main` (`928507d`).

## Фаза 1 — исследование

«Проверил» — видел в коде. «Полагаю» — не проверено: Docker в окружении
недоступен.

### Чего нет в репозитории

- `sard-restore-verification.md` (V-D4, V-D7, V-D8) и
  `docs/spikes/v0-restore-verification.md`: файлов нет, поиск по `docs/`
  ничего не нашёл. Предложения спайка V0 учесть нельзя.

### Что есть (проверил)

| Тема | Факт | Где |
|---|---|---|
| Токен | `enrollment_tokens`: hash, expires/used/revoked, label, builtin. Роли нет | `persistence/EnrollmentTokenRecord.kt:15-42`, `V202609271200__enrollment.sql` |
| Встроенный токен | `replaceBuiltin()`, в REST не виден. При регистрации `builtin` копируется в агента | `EnrollmentTokens.kt:175,244`; `Enrollment.kt:123` |
| Сертификат агента | BouncyCastle. В нём `CN=<agentId>`, один URI SAN `sard://tenants/<t>/agents/<a>`, EKU clientAuth, срок 365 дней. Своих расширений нет. Из CSR берётся только ключ | `pki/Certificates.kt:58-111`, `pki/AgentCsr.kt:15-29` |
| Перехватчик S3 | Открытый набор: Enrollment и Health. Принципал — `AgentPrincipal(agentId, tenantId, serial)`, сверка серийного номера и SAN с БД | `AgentAuthInterceptor.kt:44-80`, `AgentAuthentication.kt:88-127` |
| Register | Хранится hostname, версия, os, arch, protocol, имена секретов и скриптов, плагины, репозитории. Hello не хранится, в нём только `running_command_ids` | `Registration.kt:49-56`; `agent.proto:89,165` |
| Dispatch | Агент шага — `source.agentId`, выбора агента нет. Возможности проверяются в `AgentOffer.require` при запуске и при POST/PUT источника, в самом dispatch проверки нет | `runs/Runs.kt:179`, `runs/AgentOffer.kt`, `StepDispatcher.kt:132` |
| `ACTION_VERIFY` | Уже есть (`= 3`). Сегодня это проверка плагином (`sdk.Verifier`) снимка, восстановленного во временный каталог на том же агенте. Ни один встроенный плагин её не реализует, только testplugin | `pluginhost/handler.go:66-72,162-185` |
| `ACTION_RUN` | Обработчика нет: есть только allowlist `scripts` и `script_names` в Register | `executor/command.go:142` |
| Отказ шага | `STEP_STATUS_REJECTED` со свободным текстом, машинного кода нет | `executor/command.go:88-123`, `agent.proto:258` |
| Агент и сертификат | `tlsid.Check` проверяет только пару ключ/сертификат. `agent_id` из SAN читает лишь `enroll.InspectIdentity` | `tlsid/tlsid.go:43`, `enroll/identity.go:41,100` |
| `status` | Команды нет ни у `sard-agent`, ни у `sardctl` (там только `version`) | `cmd/sard-agent/main.go:57-66`, `cli/internal/commands/root.go:27` |
| machine-id | В коде нигде не используется | grep |
| sard-self | `read_only: true`, `hostname: sard-self`, состояние на томе `/var/lib/sard-self` | `deploy/docker-compose.yml:87-115` |
| e2e | Testcontainers: каждая команда (enroll, repo, сам агент) — новый контейнер на тех же томах | `test/e2e/.../AgentHost.kt:19-25,260-268` |
| Совместимость | `protocol_version = 1`, `buf breaking` (FILE) в CI. Теста «агент N−1 с сервером N» нет | `app/app.go:17`, `AgentSnapshot.kt:55`, `ci.yml:52` |
| Продление | `RenewCertificate` в proto есть, на сервере возвращает UNIMPLEMENTED | `AgentGrpcService.kt:29` |

Полагаю: в образах `ubuntu:24.04` и `sard-agent` нет `/etc/machine-id` (или он
пустой), а корень sard-self только для чтения. Проверю e2e в фазе 2.

### Предложения к контрольной точке 1

См. ответ владельцу в сессии: способ записи роли, идентификатор машины,
контракт proto и REST, вопросы. Решения внесу сюда после ревью.
