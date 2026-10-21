<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright 2026 Artur Abalov -->

# 2026-10-21 — V3: ключи restic, срез 3 из 3 (консоль) (coder)

Спека: docs/specs/web/restic-keys.feature (КВ1–КВ7).

## Сделано
- Моки: `recoveryKey` у репозиториев агентов, `verifierSetup` (null у источников, два элемента у верификатора),
  `recoveryKeyCommand` в состоянии самобэкапа, `repositories` в сводке агента, новый обработчик
  `GET /api/v1/repositories` (src/mocks/api/repositories.ts; absent, present, unknown, withoutRecoveryKey 1).
- Баннер `RecoveryKeyBanner` на каждой странице: решение — `bannerRepositoryCount` (сравнение с нулём).
- Страница `/repositories`, пункт меню, метка признака в карточке агента (`RecoveryKeyMark`, тон — theme.ts).
- Блок `VerifierSetup` в карточке верификатора; шаг 4 самобэкапа: `recoveryKeyCommand` первой, затем
  `passwordRevealCommand`; карточка восстановления: строка `recoveryCard.requires` (страница и .txt).
- Локали ru и en: 13 ключей из спеки плюс служебные подписи (см. ниже).

## Отклонения и замечания
- Кроме 13 ключей добавлены `repositories.{repositoryId,holders,empty,command,recoveryKey.label}`,
  `verifierSetup.source`, `setup.keys.secondCommand`; удалён неиспользуемый `setup.keys.command`.
  Тексты `setup.keys.intro` и `setup.keys.confirm` переписаны под КВ1.
- Сценарии quoting КС7 по HTTP недостижимы; веб обрабатывает только `keyAddCommand === null`.
