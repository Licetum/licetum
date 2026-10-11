'use client';

import { Button, Card, Flex, Typography } from 'antd';
import type { ReactNode } from 'react';
import { useTranslation } from 'react-i18next';

import { Brand } from './brand';
import { LanguageMenu } from './language-menu';
import { usePreferences } from './providers';

/** The frame of the screens outside the application: sign-in, no access, sign-in error. */
export function AuthCard({ children }: { children: ReactNode }) {
  const { t } = useTranslation();
  const { scheme, setMode } = usePreferences();
  return (
    <main style={{ minHeight: '100vh', display: 'grid', placeItems: 'center', padding: 24 }}>
      <Card style={{ width: '100%', maxWidth: 440 }} styles={{ body: { padding: 32 } }}>
        <Flex vertical gap={16}>
          <div style={{ marginBottom: 8 }}>
            <Brand size={24} />
          </div>
          {children}
          <Flex justify="space-between" align="center" wrap gap={8} style={{ marginTop: 16 }}>
            <LanguageMenu />
            <Button
              type="link"
              size="small"
              onClick={() => setMode(scheme === 'dark' ? 'light' : 'dark')}
              style={{ color: 'var(--licetum-muted)' }}
            >
              {scheme === 'dark' ? t('prefs.themeLight') : t('prefs.themeDark')}
            </Button>
          </Flex>
        </Flex>
      </Card>
    </main>
  );
}

export function AuthTitle({ children }: { children: ReactNode }) {
  return (
    <Typography.Title level={3} style={{ margin: 0 }}>
      {children}
    </Typography.Title>
  );
}
