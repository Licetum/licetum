import { createInstance, type i18n } from 'i18next';
import { initReactI18next } from 'react-i18next';

import type { Language } from './config';
import en from './locales/en.json';
import ru from './locales/ru.json';
import zh from './locales/zh.json';

export * from './config';

/** A separate instance per language: the same code runs on the server (rendering) and in the browser. */
export function createI18n(lng: Language): i18n {
  const instance = createInstance();
  void instance.use(initReactI18next).init({
    lng,
    fallbackLng: 'en',
    resources: { en: { translation: en }, ru: { translation: ru }, zh: { translation: zh } },
    interpolation: { escapeValue: false },
    initAsync: false,
    returnNull: false,
  });
  return instance;
}
