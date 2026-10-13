<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright 2026 Artur Abalov -->

# 2026-10-13 — Производительность шлюза: мутации Go и флейки `internal/enroll`

Ветка `ccr-9aafdeb2-nrlltu`. Задача владельца: диагностика медленного мутационного тестирования и
шлюза в целом; затем «делай все P0 по порядку» (защищённые файлы править разрешено).

## Диагностика (замеры)
- CI run 38298083030 на `main`: шаг `gate.sh agent` 4 ч 27 мин из 4 ч 28 мин; сервер 27,5 мин;
  e2e 35,5 мин; остальные джобы ≤ 4 мин.
- go-mutesting `--no-exec ./...` по агенту: 4314 мутантов; `cmd/sard-agent` 954, `hostsetup` 547,
  `repoconnect` 485, `executor` 338, `postgresql` 309.
- Время мутанта (`go test` пакета после правки): `cmd/sard-agent` 15,7 с (таймаут), `postgresql`
  8,7, `executor` 5,3, `transport` 2,4, `config` 0,7.
- Немутированный `cmd/sard-agent` не укладывался в `-timeout 10s` (19,4 с) → все его мутанты
  засчитывались убитыми. В агенте 0 вызовов `t.Parallel()`.
- Отчёты субагентов: дизайн пайплайна и ранжированный список задач P0–P3 (в чате сессии).

## Сделано (P0)
| Коммит | Что |
|---|---|
| `e2dddad` | `scripts/mutate-exec.sh` (`go test -overlay`, таймаут `max(3 × база, 30 с)`), шарды и сводка в `gate.sh`, 12 шардов агента в CI, `timeout-minutes` у всех джоб; ADR-черновик `00XX-draft-go-mutation-exec.md`; OQ-030 и OQ-130 закрыты |
| `4bc57c7` | `lock_test.go`: тест гонки lock-файла останавливается после 200 отказов (2,89 с → 0,05 с) |
| `326b944` | `trust_test.go`: `listenIdleTLS` держит принятые соединения (финализатор GC закрывал их; GOGC=1: 4/50 → 0/200) |
| `665b0bb` | `enroll.DialTOFU` возвращает `*enroll.Conn`, его `Close` закрывает и TLS-соединение; одноразовый dialer (утечка: сервер ждал HTTP/2-преамбулу 120 с) |

Проверено: `tools` — новый exec и встроенный дают один score 0.981043 и тех же 4 выживших;
`sdk`, `cli` — 1.000000; отсутствующий шард роняет `mutation-total`. Пробный шард агента 1/40:
104 мутанта, 14 выжили (0.865385) — раньше часть из них засчитывалась убитыми по таймауту.
Три `/quick-fix` прошли coder → cleaner → architect (все APPROVED); hardener не запускался
(состав `/quick-fix`; для `665b0bb` — решение владельца).

## Не сделано / дальше
- Полный мутационный прогон агента в новом режиме и проверка новых джоб CI — нужен PR.
- P1: часы в конструкторах и `t.Parallel` в `cmd/sard-agent`, `executor`, `postgresql`; JaCoCo в
  мутационном прогоне сервера; процессные тесты `cmd/sard-agent` за тег `integration` (ADR).
- Необязательные замечания архитектора к `665b0bb`: в комментарии `Conn.Close` указать, что второй
  вызов возвращает `ErrClientConnClosing`; заменить встраивание `*grpc.ClientConn` на поле с
  делегированием `Invoke`/`NewStream`.
- Горутина `TestCallEnrollTimeoutAfterTheRequestWasSentMarksTheTokenMaybeSpent`
  (`classify_test.go:242`) остаётся в `select {}` до конца процесса.
