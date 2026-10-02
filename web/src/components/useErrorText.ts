// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { useTranslation } from 'react-i18next'
import type { ErrorText } from '../fieldErrors'

// Turns the errors of a form place into the text beside it: the server's own words for a
// config error, the localized message of its code otherwise.
export function useErrorText() {
  const { t } = useTranslation()
  return (texts: ErrorText[] | undefined): string | undefined =>
    texts?.map((text) => ('server' in text ? text.server : t(text.key))).join('; ')
}
