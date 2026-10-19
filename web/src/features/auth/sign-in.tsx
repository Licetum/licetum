'use client';

import { Alert, Button, Collapse, Flex, Typography } from 'antd';
import { Ban, TriangleAlert } from 'lucide-react';
import { useState } from 'react';
import { useTranslation } from 'react-i18next';

import { AuthCard, AuthTitle } from '@/components/auth-card';

export function SignInScreen({ returnTo }: { returnTo: string }) {
  const { t } = useTranslation();
  return (
    <AuthCard>
      <AuthTitle>{t('auth.title')}</AuthTitle>
      <Typography.Paragraph type="secondary" style={{ margin: 0 }}>
        {t('auth.subtitle')}
      </Typography.Paragraph>
      {/* A plain link, not a router link: the route answers with a redirect to the identity provider. */}
      <Button
        type="primary"
        size="large"
        block
        href={`/auth/login?returnTo=${encodeURIComponent(returnTo)}`}
      >
        {t('auth.signIn')}
      </Button>
      <Collapse
        ghost
        size="small"
        items={[
          {
            key: 'help',
            label: t('auth.helpTitle'),
            children: <Typography.Text type="secondary">{t('auth.help')}</Typography.Text>,
          },
        ]}
      />
    </AuthCard>
  );
}

const ERRORS = [
  'not_configured',
  'denied',
  'state',
  'idp_unreachable',
  'invalid_response',
  'idp_error',
  'unknown',
] as const;

export function SignInErrorScreen({ code }: { code: string }) {
  const { t } = useTranslation();
  const known = (ERRORS as readonly string[]).includes(code) ? code : 'unknown';
  return (
    <AuthCard>
      <Flex align="center" gap={12}>
        <TriangleAlert size={28} aria-hidden style={{ color: 'var(--licetum-except-fg)' }} />
        <AuthTitle>{t('auth.errorTitle')}</AuthTitle>
      </Flex>
      <Alert type="error" showIcon={false} title={t(`auth.error.${known}`)} />
      <Button type="primary" size="large" block href="/auth/login">
        {t('auth.retry')}
      </Button>
      <Typography.Text type="secondary" style={{ fontSize: 13 }}>
        {t('auth.errorHint')}
      </Typography.Text>
    </AuthCard>
  );
}

export function NoAccessScreen({ name, username }: { name: string; username: string }) {
  const { t } = useTranslation();
  const [copied, setCopied] = useState(false);
  const details = [
    `user: ${username}`,
    `time: ${new Date().toISOString()}`,
    `page: ${typeof window === 'undefined' ? '' : window.location.origin}`,
  ].join('\n');

  const signOut = async () => {
    await fetch('/auth/logout', { method: 'POST' });
    window.location.assign('/login');
  };

  return (
    <AuthCard>
      <Flex align="center" gap={12}>
        <Ban size={28} aria-hidden style={{ color: 'var(--licetum-deny-fg)' }} />
        <AuthTitle>{t('auth.noAccessTitle')}</AuthTitle>
      </Flex>
      <Typography.Paragraph type="secondary" style={{ margin: 0 }}>
        {t('auth.noAccessBody', { name: name || username, username })}
      </Typography.Paragraph>
      <Flex vertical gap={8}>
        <Button type="primary" size="large" onClick={signOut}>
          {t('auth.signOutAndRetry')}
        </Button>
        <Button
          onClick={async () => {
            await navigator.clipboard?.writeText(details);
            setCopied(true);
          }}
        >
          {copied ? t('auth.copied') : t('auth.copyDetails')}
        </Button>
      </Flex>
    </AuthCard>
  );
}
