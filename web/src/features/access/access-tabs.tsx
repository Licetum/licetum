'use client';

import { Tabs } from 'antd';
import { usePathname, useRouter } from 'next/navigation';
import { useTranslation } from 'react-i18next';

import { useAccess } from '@/lib/access';

/** The section "Access" owns three pages; they are tabs under the header, not separate items of the top bar. */
export function AccessTabs() {
  const { t } = useTranslation();
  const router = useRouter();
  const pathname = usePathname();
  const access = useAccess();

  const items = [
    { key: '/access', label: t('nav.grants'), show: access.any('assignment_rule', 'read') },
    { key: '/people', label: t('nav.people'), show: access.any('group', 'read') },
    { key: '/roles', label: t('nav.roles'), show: access.any('role', 'read') },
  ].filter((i) => i.show);

  const active =
    items.find((i) => pathname === i.key || pathname.startsWith(`${i.key}/`))?.key ?? '/access';
  return (
    <Tabs
      activeKey={active}
      onChange={(key) => router.push(key)}
      items={items.map(({ key, label }) => ({ key, label }))}
      style={{ marginBottom: 8 }}
      aria-label={t('nav.access')}
    />
  );
}
