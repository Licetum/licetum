// The supported languages and how to pick one. No React here: the server uses it too.
const LANGUAGES = ['ru', 'en', 'zh'] as const;
export type Language = (typeof LANGUAGES)[number];
export const DEFAULT_LANGUAGE: Language = 'en';
export const LANGUAGE_COOKIE = 'licetum_lang';

/** What the language switchers show: the name of each language in itself. */
export const LANGUAGE_OPTIONS: { label: string; value: Language }[] = [
  { label: 'RU', value: 'ru' },
  { label: 'EN', value: 'en' },
  { label: '中文', value: 'zh' },
];

export const isLanguage = (value: string | undefined | null): value is Language =>
  !!value && (LANGUAGES as readonly string[]).includes(value);

/** The best language of an Accept-Language header among the supported ones. */
export function languageFromHeader(header: string | null | undefined): Language {
  for (const part of (header ?? '').split(',')) {
    const tag = part.split(';')[0]?.trim().toLowerCase().slice(0, 2);
    if (isLanguage(tag)) return tag;
  }
  return DEFAULT_LANGUAGE;
}
