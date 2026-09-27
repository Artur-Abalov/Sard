// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import i18n from 'i18next'
import { initReactI18next } from 'react-i18next'
import { languages } from './languages'
import en from './locales/en.json'
import ru from './locales/ru.json'

// Keep <html lang> in sync so screen readers use the right language.
i18n.on('languageChanged', (lng) => {
  document.documentElement.lang = lng
})

void i18n.use(initReactI18next).init({
  resources: { ru: { translation: ru }, en: { translation: en } },
  lng: languages[0],
  fallbackLng: 'en',
  interpolation: { escapeValue: false }, // React escapes already
})

export default i18n
