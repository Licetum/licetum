import 'server-only';
import { cookies, headers } from 'next/headers';

import {
  DEFAULT_LANGUAGE,
  LANGUAGE_COOKIE,
  isLanguage,
  languageFromHeader,
  type Language,
} from '@/i18n/config';
import type { Scheme } from '@/theme/tokens';

type ThemeMode = 'light' | 'dark' | 'system';
const THEME_COOKIE = 'licetum_theme';
/** The scheme "system" resolved to in the browser, kept so the next render on the server matches it. */
const SCHEME_COOKIE = 'licetum_scheme';

export type Prefs = { lang: Language; mode: ThemeMode; scheme: Scheme };

export async function getPrefs(): Promise<Prefs> {
  const jar = await cookies();
  const stored = jar.get(LANGUAGE_COOKIE)?.value;
  const lang: Language = isLanguage(stored)
    ? stored
    : languageFromHeader((await headers()).get('accept-language')) || DEFAULT_LANGUAGE;

  const modeValue = jar.get(THEME_COOKIE)?.value;
  const mode: ThemeMode = modeValue === 'light' || modeValue === 'dark' ? modeValue : 'system';
  const resolved = jar.get(SCHEME_COOKIE)?.value;
  const scheme: Scheme = mode === 'system' ? (resolved === 'dark' ? 'dark' : 'light') : mode;
  return { lang, mode, scheme };
}
