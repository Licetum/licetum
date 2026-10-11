'use client';

import { Alert, Button, Dropdown, Flex, Layout, Skeleton, Tabs, type MenuProps } from 'antd';
import { BookOpen, ChevronDown, LogOut, Monitor, Moon, Search, Settings, Sun } from 'lucide-react';
import Link from 'next/link';
import { usePathname, useRouter, useSearchParams } from 'next/navigation';
import { useEffect, useState, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';

import { Brand } from '@/components/brand';
import { LanguageMenu } from '@/components/language-menu';
import { usePreferences } from '@/components/providers';
import { AccessProvider, useAccess, type Policy } from '@/lib/access';
import type { Identity } from '@/lib/jwt';

import { GrantProvider, useGrant } from '../grant/grant-context';
import { GrantDrawer } from '../grant/grant-drawer';
import { CommandPalette } from './command-palette';

type NavItem = { key: string; href: string; label: string; prefixes: string[]; policy: Policy };

function useNav(): NavItem[] {
  const { t } = useTranslation();
  return [
    { key: 'home', href: '/', label: t('nav.home'), prefixes: [], policy: 'service' },
    {
      key: 'access',
      href: '/access',
      label: t('nav.access'),
      prefixes: ['/access', '/people', '/roles'],
      policy: 'assignment_rule',
    },
    {
      key: 'services',
      href: '/services',
      label: t('nav.services'),
      prefixes: ['/services'],
      policy: 'service',
    },
    {
      key: 'check',
      href: '/check',
      label: t('nav.check'),
      prefixes: ['/check'],
      policy: 'service',
    },
    {
      key: 'history',
      href: '/history',
      label: t('nav.history'),
      prefixes: ['/history'],
      policy: 'service',
    },
  ];
}

function isActive(item: NavItem, pathname: string): boolean {
  if (item.key === 'home') return pathname === '/';
  return item.prefixes.some((p) => pathname === p || pathname.startsWith(`${p}/`));
}

export function AppShell({ identity, children }: { identity: Identity; children: ReactNode }) {
  return (
    <AccessProvider>
      <GrantProvider>
        <Frame identity={identity}>{children}</Frame>
        <GrantDrawer />
      </GrantProvider>
    </AccessProvider>
  );
}

function Frame({ identity, children }: { identity: Identity; children: ReactNode }) {
  const { t } = useTranslation();
  const router = useRouter();
  const pathname = usePathname();
  const access = useAccess();
  const nav = useNav();
  const [paletteOpen, setPaletteOpen] = useState(false);
  const { open: openGrant } = useGrant();
  const params = useSearchParams();

  // /access?grant=1 (and /access/new) opens the panel "Give access" over the list.
  useEffect(() => {
    if (params.get('grant') === '1') {
      openGrant();
      const next = new URLSearchParams(params.toString());
      next.delete('grant');
      const query = next.toString();
      router.replace(query ? `${pathname}?${query}` : pathname, { scroll: false });
    }
  }, [params, openGrant, router, pathname]);

  // The person has signed in but the platform grants nothing: the rest of the application is closed to them.
  useEffect(() => {
    if (access.empty) router.replace('/no-access');
  }, [access.empty, router]);

  // ⌘K / Ctrl+K opens the palette from anywhere.
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'k') {
        event.preventDefault();
        setPaletteOpen(true);
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, []);

  const visible = nav.filter((item) => item.key === 'home' || access.any(item.policy, 'read'));

  return (
    <Layout style={{ minHeight: '100vh', background: 'var(--licetum-layout)' }}>
      <a className="skip-link" href="#main">
        {t('shell.skip')}
      </a>
      <Layout.Header
        className="app-header"
        style={{
          display: 'flex',
          alignItems: 'center',
          gap: 24,
          borderBottom: '1px solid var(--licetum-border)',
          position: 'sticky',
          top: 0,
          zIndex: 100,
        }}
      >
        <Link href="/" aria-label="Licetum" style={{ color: 'inherit' }}>
          <Brand />
        </Link>
        <nav
          className="app-nav"
          aria-label={t('shell.mainNav')}
          style={{ flex: 1, minWidth: 0, alignSelf: 'stretch', display: 'flex' }}
        >
          {access.loading ? (
            <Flex gap={4}>
              {[0, 1, 2, 3].map((i) => (
                <Skeleton.Button key={i} active size="small" style={{ width: 72 }} />
              ))}
            </Flex>
          ) : (
            <Tabs
              className="app-tabs"
              size="small"
              activeKey={visible.find((item) => isActive(item, pathname))?.key ?? ''}
              onChange={(key) => {
                const item = visible.find((x) => x.key === key);
                if (item) router.push(item.href);
              }}
              items={visible.map((item) => ({ key: item.key, label: item.label }))}
            />
          )}
        </nav>
        <Flex align="center" gap={8}>
          <Button
            onClick={() => setPaletteOpen(true)}
            icon={<Search size={16} aria-hidden />}
            aria-label={t('shell.search')}
          >
            <span style={{ color: 'var(--licetum-muted)' }}>{t('shell.searchShort')}</span>
            <kbd className="mono" style={{ marginLeft: 8, color: 'var(--licetum-muted)' }}>
              ⌘K
            </kbd>
          </Button>
          <LanguageMenu />
          <ThemeSwitch />
          <UserMenu identity={identity} />
        </Flex>
      </Layout.Header>
      <Layout.Content>
        <div id="main" tabIndex={-1} style={{ outline: 'none' }}>
          {access.failed ? (
            <div className="page">
              <Alert
                type="error"
                showIcon
                title={t('shell.accessFailed')}
                action={
                  <Button onClick={() => window.location.reload()}>{t('common.retry')}</Button>
                }
              />
            </div>
          ) : (
            children
          )}
        </div>
      </Layout.Content>
      <CommandPalette
        open={paletteOpen}
        onClose={() => setPaletteOpen(false)}
        onGrant={() => openGrant()}
      />
    </Layout>
  );
}

function ThemeSwitch() {
  const { t } = useTranslation();
  const { mode, setMode } = usePreferences();
  const icon =
    mode === 'dark' ? (
      <Moon size={16} aria-hidden />
    ) : mode === 'light' ? (
      <Sun size={16} aria-hidden />
    ) : (
      <Monitor size={16} aria-hidden />
    );
  const name = t(
    mode === 'dark'
      ? 'prefs.themeDarkName'
      : mode === 'light'
        ? 'prefs.themeLightName'
        : 'prefs.themeSystemName',
  );
  const items: MenuProps['items'] = [
    { key: 'light', label: t('prefs.themeLightName'), icon: <Sun size={14} aria-hidden /> },
    { key: 'dark', label: t('prefs.themeDarkName'), icon: <Moon size={14} aria-hidden /> },
    { key: 'system', label: t('prefs.themeSystemName'), icon: <Monitor size={14} aria-hidden /> },
  ];
  return (
    <Dropdown
      menu={{
        items,
        selectable: true,
        selectedKeys: [mode],
        onClick: ({ key }) => setMode(key as 'light' | 'dark' | 'system'),
      }}
      trigger={['click']}
    >
      <Button aria-label={t('prefs.theme')} icon={icon}>
        <span className="hide-narrow">{name}</span>
        <ChevronDown size={14} aria-hidden />
      </Button>
    </Dropdown>
  );
}

function UserMenu({ identity }: { identity: Identity }) {
  const { t } = useTranslation();
  const router = useRouter();
  const access = useAccess();
  const role =
    access.whole('service', 'write') && access.whole('assignment_rule', 'write')
      ? t('shell.roleAdmin')
      : t('shell.roleReader');

  const signOut = async () => {
    await fetch('/auth/logout', { method: 'POST' });
    window.location.assign('/login');
  };

  const items: MenuProps['items'] = [
    {
      key: 'who',
      disabled: true,
      label: (
        <div style={{ color: 'var(--licetum-text)', cursor: 'default' }}>
          <div style={{ fontWeight: 600 }}>{identity.name}</div>
          <div className="mono" style={{ color: 'var(--licetum-muted)' }}>
            @{identity.username}
          </div>
          {!access.loading && (
            <div style={{ color: 'var(--licetum-muted)', fontSize: 12 }}>{role}</div>
          )}
        </div>
      ),
    },
    { type: 'divider' },
    { key: 'settings', icon: <Settings size={14} aria-hidden />, label: t('user.settings') },
    { key: 'docs', icon: <BookOpen size={14} aria-hidden />, label: t('user.docs') },
    { type: 'divider' },
    { key: 'logout', icon: <LogOut size={14} aria-hidden />, label: t('user.logout') },
  ];

  return (
    <Dropdown
      trigger={['click']}
      menu={{
        items,
        onClick: ({ key }) => {
          if (key === 'logout') void signOut();
          if (key === 'settings') router.push('/settings/admins');
          if (key === 'docs')
            window.open('https://github.com/Licetum/licetum', '_blank', 'noopener');
        },
      }}
    >
      <Button type="text" aria-label={t('user.menu')}>
        {identity.name} ▾
      </Button>
    </Dropdown>
  );
}
