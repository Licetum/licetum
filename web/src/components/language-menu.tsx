'use client';

import { Button, Dropdown, type MenuProps } from 'antd';
import { ChevronDown, Languages } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import { usePreferences } from '@/components/providers';
import { LANGUAGE_OPTIONS, type Language } from '@/i18n';

/** The language names in their own languages, for the menu. */
const NAMES: Record<Language, string> = { ru: 'Русский', en: 'English', zh: '中文' };

/** A menu of the interface languages: the button shows the current one, the menu lists them all. */
export function LanguageMenu() {
  const { t } = useTranslation();
  const { lang, setLang } = usePreferences();
  const items: MenuProps['items'] = LANGUAGE_OPTIONS.map((o) => ({
    key: o.value,
    label: NAMES[o.value],
  }));
  const current = LANGUAGE_OPTIONS.find((o) => o.value === lang)?.label;
  return (
    <Dropdown
      menu={{
        items,
        selectable: true,
        selectedKeys: [lang],
        onClick: ({ key }) => setLang(key as Language),
      }}
      trigger={['click']}
    >
      <Button aria-label={t('prefs.language')} icon={<Languages size={16} aria-hidden />}>
        {current}
        <ChevronDown size={14} aria-hidden />
      </Button>
    </Dropdown>
  );
}
