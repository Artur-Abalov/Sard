// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { describe, expect, test } from 'vitest'
import {
  levelWords,
  restoreTimeText,
  stagesSpan,
  mismatchText,
  outcomeView,
  stageHint,
  stageLabel,
  verificationMetric,
  verifierReasonText,
} from './verification'

const NOW = Date.parse('2026-10-26T12:00:00Z')

function ago(ms: number): string {
  return new Date(NOW - ms).toISOString()
}

const HOUR = 3_600_000
const DAY = 24 * HOUR

describe('levelWords', () => {
  test.each([
    [1, 'ru', 'целостность'],
    [2, 'ru', 'извлечение'],
    [3, 'ru', 'запуск'],
    [4, 'ru', 'данные совпали'],
    [5, 'ru', 'время восстановления измерено'],
    [4, 'en', 'data matches'],
    [5, 'en', 'restore time measured'],
  ])('level %s in %s is %s', (level, language, text) => {
    expect(levelWords(level, language)).toBe(text)
  })

  test('a level the console does not know is shown as its number', () => {
    expect(levelWords(0, 'ru')).toBe('0')
    expect(levelWords(6, 'en')).toBe('6')
  })
})

describe('verificationMetric', () => {
  test.each([
    [true, 5 * HOUR, 5, 'проверено 5 часов назад — время восстановления измерено'],
    [true, null, null, 'ещё не проверялось'],
    [false, null, null, 'проверка выключена'],
    [false, 2 * DAY, 4, 'проверка выключена, последняя — 2 дня назад'],
  ])('enabled %s, passed %s ms ago, level %s: %s', (enabled, passedAgo, level, text) => {
    const lastVerifiedAt = passedAgo === null ? null : ago(passedAgo)

    expect(
      verificationMetric({ enabled, lastVerifiedAt, lastVerifiedLevel: level }, NOW, 'ru'),
    ).toBe(text)
  })

  test.each([
    [HOUR, 'проверено час назад — данные совпали'],
    [3 * HOUR, 'проверено 3 часа назад — данные совпали'],
    [21 * HOUR, 'проверено 21 час назад — данные совпали'],
    [20 * 60_000, 'проверено 20 минут назад — данные совпали'],
    [30_000, 'проверено только что — данные совпали'],
    [3 * DAY, 'проверено 3 дня назад — данные совпали'],
  ])('a pass %s ms ago is told in the words of the language', (passedAgo, text) => {
    const fields = { enabled: true, lastVerifiedAt: ago(passedAgo), lastVerifiedLevel: 4 }

    expect(verificationMetric(fields, NOW, 'ru')).toBe(text)
  })

  test('in English', () => {
    const fields = { enabled: true, lastVerifiedAt: ago(5 * HOUR), lastVerifiedLevel: 5 }

    expect(verificationMetric(fields, NOW, 'en')).toBe(
      'verified 5 hours ago — restore time measured',
    )
    expect(
      verificationMetric(
        { enabled: true, lastVerifiedAt: null, lastVerifiedLevel: null },
        NOW,
        'en',
      ),
    ).toBe('not verified yet')
    expect(
      verificationMetric(
        { enabled: false, lastVerifiedAt: null, lastVerifiedLevel: null },
        NOW,
        'en',
      ),
    ).toBe('verification is off')
  })

  test('a pass without a level says only when', () => {
    const fields = { enabled: true, lastVerifiedAt: ago(2 * HOUR), lastVerifiedLevel: null }

    expect(verificationMetric(fields, NOW, 'ru')).toBe('проверено 2 часа назад')
  })
})

describe('outcomeView', () => {
  test.each([
    ['passed', 'пройдено', 'success'],
    ['failed', 'провал', 'error'],
    ['cannot_verify', 'не можем проверить', 'warning'],
    ['infrastructure_failure', 'сбой инфраструктуры', 'warning'],
    ['no_verifier', 'нет подходящего верификатора', 'warning'],
  ] as const)('%s is «%s» in the role %s', (status, label, role) => {
    expect(outcomeView(status, 'ru')).toEqual({ label, role })
  })

  test('no outcome that did not take place is an error', () => {
    for (const status of ['cannot_verify', 'infrastructure_failure', 'no_verifier'] as const) {
      expect(outcomeView(status, 'ru').role, status).not.toBe('error')
    }
  })

  test('in English', () => {
    expect(outcomeView('infrastructure_failure', 'en').label).toBe('infrastructure failure')
  })
})

describe('verifierReasonText', () => {
  test.each([
    ['offline', 'не в сети'],
    ['outdated', 'агент старше V4'],
    ['no_key', 'нет ключа к репозиторию'],
    ['no_plugin', 'нет плагина для проверки'],
    ['same_host', 'та же машина, что у источника'],
    ['low_space', 'мало места'],
    ['busy', 'занят другой проверкой'],
    ['space_unknown', 'свободное место неизвестно'],
  ] as const)('%s is «%s»', (code, text) => {
    expect(verifierReasonText(code, 'ru')).toBe(text)
  })
})

describe('mismatchText', () => {
  test.each([
    [
      'rows',
      'public.orders',
      '1204311',
      '1204310',
      'таблица public.orders: в источнике 1 204 311 строк, в копии 1 204 310',
    ],
    ['control_path', '/srv/data', 'nonempty', 'empty', 'контрольный путь /srv/data пуст'],
    ['control_path', '/srv/data', 'nonempty', 'missing', 'контрольного пути /srv/data нет в копии'],
    ['tables', '', '42', '41', 'таблиц: в источнике 42, в копии 41'],
    ['other', 'new_fact', '1', '2', 'new_fact: в источнике 1, в копии 2'],
  ])('%s %s %s→%s', (kind, subject, expected, actual, text) => {
    const name = kind === 'other' ? subject : `${kind}:${subject}`

    expect(mismatchText({ name, kind, subject, expected, actual }, 'ru')).toBe(text)
  })

  test('in English, with the commas of the language', () => {
    const rows = {
      name: 'rows:public.orders',
      kind: 'rows',
      subject: 'public.orders',
      expected: '1204311',
      actual: '1204310',
    }

    expect(mismatchText(rows, 'en')).toBe(
      'table public.orders: 1,204,311 rows in the source, 1,204,310 in the copy',
    )
  })

  test('the other facts are said with their raw name and the values as they came', () => {
    const fact = {
      name: 'extension:postgis',
      kind: 'extension',
      subject: 'postgis',
      expected: 'present',
      actual: 'absent',
    }

    expect(mismatchText(fact, 'ru')).toBe('extension:postgis: в источнике present, в копии absent')
  })

  test('files and bytes are counts and sizes', () => {
    const files = { name: 'files', kind: 'files', subject: '', expected: '1000', actual: '998' }
    const bytes = { name: 'bytes', kind: 'bytes', subject: '', expected: '2048', actual: '1024' }

    expect(mismatchText(files, 'ru')).toBe('файлов: в источнике 1 000, в копии 998')
    expect(mismatchText(bytes, 'ru')).toBe('объём: в источнике 2 КиБ, в копии 1 КиБ')
    expect(mismatchText(bytes, 'en')).toBe('size: 2 KiB in the source, 1 KiB in the copy')
  })
})

describe('stageLabel and stageHint', () => {
  test.each([
    ['snapshot', 'снимок'],
    ['fingerprint_tag', 'отпечаток в теге снимка'],
    ['work_volume', 'рабочий том'],
    ['restic_check', 'целостность хранилища'],
    ['sandbox', 'песочница'],
    ['restore', 'извлечение'],
    ['start', 'запуск приложения'],
    ['fingerprint', 'сравнение отпечатков'],
    ['cleanup', 'уборка'],
  ])('%s is «%s»', (name, label) => {
    expect(stageLabel(name, 'ru')).toBe(label)
  })

  test('a stage the console does not know is named as it is', () => {
    expect(stageLabel('future_stage', 'ru')).toBe('future_stage')
  })

  test('a stage that could not run says what to do about it', () => {
    expect(stageHint('work_volume', 'ru')).toBe(
      'На верификаторе не хватает места или рабочий том без noexec — см. вывод',
    )
    expect(stageHint('restore', 'ru')).toBe(
      'В образе песочницы нет расширения источника — добавьте его в образ, имя в выводе',
    )
    expect(stageHint('sandbox', 'en')).toBe(
      'There is no container runtime or sandbox image — see the output',
    )
  })

  test('a stage without a hint has none', () => {
    expect(stageHint('cleanup', 'ru')).toBeNull()
    expect(stageHint('future_stage', 'ru')).toBeNull()
  })
})

describe('stagesSpan', () => {
  const stage = (startedAt: string | null, finishedAt: string | null) => ({
    level: 1,
    name: 'restic_check',
    status: 'passed' as const,
    startedAt,
    finishedAt,
    exitCode: 0,
    output: '',
    imageDigest: '',
  })

  test('runs from the first start to the last finish', () => {
    const stages = [
      stage('2026-10-26T04:31:00Z', '2026-10-26T04:32:00Z'),
      stage('2026-10-26T04:30:00Z', '2026-10-26T04:31:00Z'),
      stage('2026-10-26T04:32:00Z', '2026-10-26T04:34:12Z'),
    ]

    expect(stagesSpan(stages)).toEqual({
      start: '2026-10-26T04:30:00Z',
      end: '2026-10-26T04:34:12Z',
    })
  })

  test('is nothing without times', () => {
    expect(stagesSpan([])).toBeNull()
    expect(stagesSpan([stage(null, null)])).toBeNull()
    expect(stagesSpan([stage('2026-10-26T04:30:00Z', null)])).toBeNull()
  })
})

describe('restoreTimeText', () => {
  test.each([
    [252_000, 'ru', '4 мин 12 с'],
    [252_000, 'en', '4 min 12 s'],
    [42_000, 'en', '42 s'],
    [3_900_000, 'ru', '1 ч 5 мин'],
    [null, 'ru', '—'],
  ])('%s ms in %s is %s', (milliseconds, language, text) => {
    expect(restoreTimeText(milliseconds, language)).toBe(text)
  })
})
