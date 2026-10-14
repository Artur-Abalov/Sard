// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

// The restore config schema of the files plugin exactly as the agent announces it
// (agent/plugins/files/restore_schema.json); restores.test.ts compares the two.
export const filesRestoreSchema = {
  $schema: 'https://json-schema.org/draft/2020-12/schema',
  title: 'Files restore',
  type: 'object',
  properties: {
    destination: {
      type: 'string',
      title: 'Destination',
      description:
        'A new directory inside the restore directory of the host, as a relative path: the files of the snapshot appear in it under their original paths. Each name is 1-255 bytes and does not start with a dot; the whole path is at most 4096 bytes. The directory must not exist or must be empty.',
      'x-sard-i18n': {
        ru: {
          title: 'Назначение',
          description:
            'Новый каталог внутри каталога восстановления хоста, относительным путём: файлы снимка появятся в нём под своими исходными путями. Имя — от 1 до 255 байт и не начинается с точки; весь путь — не больше 4096 байт. Каталог не должен существовать или должен быть пустым.',
        },
      },
      minLength: 1,
      maxLength: 4096,
      pattern: '^[^/.\\x00][^/\\x00]{0,254}(/[^/.\\x00][^/\\x00]{0,254})*$',
      examples: ['etc%20data/20261010T030000Z'],
    },
    paths: {
      type: 'array',
      title: 'Paths of the snapshot',
      description:
        'Absolute paths inside the snapshot to restore instead of the whole snapshot. At most 100, without repeats. A path is taken literally: * ? [ and a backslash are not patterns, and a .. component is not allowed. Empty: the whole snapshot.',
      'x-sard-i18n': {
        ru: {
          title: 'Пути снимка',
          description:
            'Абсолютные пути внутри снимка, которые нужно восстановить вместо всего снимка. Не больше 100, без повторов. Путь берётся буквально: * ? [ и обратная косая черта не шаблоны, компонент .. не допускается. Пусто — весь снимок.',
        },
      },
      items: {
        type: 'string',
        minLength: 1,
        maxLength: 4096,
        pattern: '^/[^\\x00*?\\[\\\\]*$',
        not: {
          pattern: '/\\.\\.(/|$)',
        },
      },
      maxItems: 100,
      uniqueItems: true,
      examples: [['/srv/data/sub']],
    },
  },
  required: ['destination'],
  additionalProperties: false,
}
