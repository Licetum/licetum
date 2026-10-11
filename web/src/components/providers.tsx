'use client';

import { AntdRegistry } from '@ant-design/nextjs-registry';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { App as AntApp, ConfigProvider } from 'antd';
import enUS from 'antd/locale/en_US';
import ruRU from 'antd/locale/ru_RU';
import zhCN from 'antd/locale/zh_CN';
import dayjs from 'dayjs';
import 'dayjs/locale/ru';
import 'dayjs/locale/zh-cn';
import { useRouter } from 'next/navigation';
import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from 'react';
import { I18nextProvider } from 'react-i18next';

import { DevTools } from '@/components/dev-tools';
import { LANGUAGE_COOKIE, createI18n, type Language } from '@/i18n';
import { themeFor, type Scheme } from '@/theme/tokens';

type Mode = 'light' | 'dark' | 'system';

const ANTD_LOCALE = { ru: ruRU, en: enUS, zh: zhCN } as const;
const DAYJS_LOCALE: Record<Language, string> = { ru: 'ru', en: 'en', zh: 'zh-cn' };

type Preferences = {
  lang: Language;
  setLang: (lang: Language) => void;
  mode: Mode;
  scheme: Scheme;
  setMode: (mode: Mode) => void;
};

const PreferencesContext = createContext<Preferences | null>(null);

export const usePreferences = (): Preferences => {
  const value = useContext(PreferencesContext);
  if (!value) throw new Error('usePreferences outside Providers');
  return value;
};

function setCookie(name: string, value: string) {
  document.cookie = `${name}=${encodeURIComponent(value)}; path=/; max-age=31536000; samesite=lax`;
}

export function Providers(props: {
  lang: Language;
  mode: Mode;
  scheme: Scheme;
  children: ReactNode;
}) {
  const router = useRouter();
  const [lang, setLangState] = useState<Language>(props.lang);
  const [mode, setModeState] = useState<Mode>(props.mode);
  const [scheme, setScheme] = useState<Scheme>(props.scheme);

  const i18n = useMemo(() => createI18n(lang), [lang]);
  const [queryClient] = useState(
    () =>
      new QueryClient({
        defaultOptions: {
          queries: {
            // An admin works with data that others change too; a short freshness window keeps lists honest.
            staleTime: 10_000,
            refetchOnWindowFocus: true,
            retry: (count, error) => {
              const status = (error as { status?: number }).status;
              return !(status && status >= 400 && status < 500) && count < 2;
            },
          },
        },
      }),
  );

  // "system" follows the operating system and remembers what it resolved to for the next server render.
  useEffect(() => {
    const media = window.matchMedia('(prefers-color-scheme: dark)');
    const apply = () => {
      const next: Scheme = mode === 'system' ? (media.matches ? 'dark' : 'light') : mode;
      setScheme(next);
      document.documentElement.dataset.theme = next;
      if (mode === 'system') setCookie('licetum_scheme', next);
    };
    apply();
    media.addEventListener('change', apply);
    return () => media.removeEventListener('change', apply);
  }, [mode]);

  useEffect(() => {
    document.documentElement.lang = lang;
    dayjs.locale(DAYJS_LOCALE[lang]);
  }, [lang]);

  const setLang = useCallback(
    (next: Language) => {
      setCookie(LANGUAGE_COOKIE, next);
      setLangState(next);
      router.refresh();
    },
    [router],
  );
  const setMode = useCallback((next: Mode) => {
    setCookie('licetum_theme', next);
    setModeState(next);
  }, []);

  const preferences = useMemo(
    () => ({ lang, setLang, mode, scheme, setMode }),
    [lang, setLang, mode, scheme, setMode],
  );

  return (
    <PreferencesContext.Provider value={preferences}>
      <I18nextProvider i18n={i18n}>
        <QueryClientProvider client={queryClient}>
          <AntdRegistry>
            <ConfigProvider theme={themeFor(scheme)} locale={ANTD_LOCALE[lang]}>
              <AntApp>{props.children}</AntApp>
              <DevTools />
            </ConfigProvider>
          </AntdRegistry>
        </QueryClientProvider>
      </I18nextProvider>
    </PreferencesContext.Provider>
  );
}
