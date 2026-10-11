'use client';

import {
  Alert,
  Breadcrumb,
  Button,
  Empty,
  Flex,
  Skeleton,
  Tooltip,
  Typography,
  message,
} from 'antd';
import { Ban, Check, Contrast, Copy, CornerDownRight } from 'lucide-react';
import Link from 'next/link';
import type { ReactNode } from 'react';
import { useTranslation } from 'react-i18next';

import type { Decision } from '@/domain/rule';

/** The header of a page: breadcrumbs, the title, a line under it and the actions on the right. */
export function PageHeader(props: {
  title: ReactNode;
  subtitle?: ReactNode;
  crumbs?: { label: ReactNode; href?: string }[];
  extra?: ReactNode;
  tag?: ReactNode;
}) {
  return (
    <div style={{ marginBottom: 16 }}>
      {props.crumbs && props.crumbs.length > 0 && (
        <Breadcrumb
          style={{ marginBottom: 4, fontSize: 13 }}
          items={props.crumbs.map((c) => ({
            title: c.href ? <Link href={c.href}>{c.label}</Link> : c.label,
          }))}
        />
      )}
      <Flex align="flex-start" justify="space-between" gap={16} wrap>
        <div style={{ minWidth: 0, flex: '1 1 320px' }}>
          <Flex align="center" gap={12} wrap>
            <Typography.Title level={2} style={{ margin: 0, fontSize: 24, lineHeight: '32px' }}>
              {props.title}
            </Typography.Title>
            {props.tag}
          </Flex>
          {props.subtitle && (
            <div style={{ color: 'var(--licetum-muted)', marginTop: 4 }}>{props.subtitle}</div>
          )}
        </div>
        {props.extra && (
          <Flex gap={8} wrap align="center">
            {props.extra}
          </Flex>
        )}
      </Flex>
    </div>
  );
}

const DECISION_ICON: Record<Decision, ReactNode> = {
  allow: <Check size={13} aria-hidden />,
  deny: <Ban size={13} aria-hidden />,
  except: <Contrast size={13} aria-hidden />,
};

/** The decision of a rule: shape and word as well as colour (nothing is told by colour alone). */
export function DecisionTag({
  decision,
  children,
}: {
  decision: Decision | 'inherit';
  children: ReactNode;
}) {
  if (decision === 'inherit') {
    return (
      <span className="decision neutral">
        <CornerDownRight size={13} aria-hidden />
        {children}
      </span>
    );
  }
  return (
    <span className={`decision ${decision}`}>
      {DECISION_ICON[decision]}
      {children}
    </span>
  );
}

/** An identifier in monospace; a click copies it. */
export function IdChip({ value, label }: { value: string; label?: string }) {
  const { t } = useTranslation();
  const [api, holder] = message.useMessage();
  return (
    <>
      {holder}
      <Tooltip title={t('common.copy')}>
        <button
          type="button"
          className="mono"
          aria-label={`${t('common.copy')}: ${label ?? value}`}
          onClick={async () => {
            await navigator.clipboard?.writeText(value);
            void api.success(t('common.copied'), 1.5);
          }}
          style={{
            all: 'unset',
            cursor: 'copy',
            color: 'var(--licetum-muted)',
            display: 'inline-flex',
            gap: 4,
            alignItems: 'center',
          }}
        >
          {label ?? value}
          <Copy size={12} aria-hidden />
        </button>
      </Tooltip>
    </>
  );
}

export function LoadingBlock({ rows = 4 }: { rows?: number }) {
  return <Skeleton active paragraph={{ rows }} />;
}

/** An error with the way out: try again. `error` is the object the API client threw. */
export function ErrorBlock({
  error,
  onRetry,
  title,
}: {
  error?: unknown;
  onRetry?: () => void;
  title?: string;
}) {
  const { t } = useTranslation();
  const detail = errorDetail(error);
  return (
    <Alert
      type="error"
      showIcon
      title={title ?? t('common.loadFailed')}
      description={detail}
      action={onRetry ? <Button onClick={onRetry}>{t('common.retry')}</Button> : undefined}
    />
  );
}

export function EmptyBlock(props: { title: ReactNode; hint?: ReactNode; action?: ReactNode }) {
  return (
    <Empty
      image={Empty.PRESENTED_IMAGE_SIMPLE}
      description={
        <div>
          <div style={{ fontWeight: 600, color: 'var(--licetum-text)' }}>{props.title}</div>
          {props.hint && (
            <div style={{ color: 'var(--licetum-muted)', maxWidth: 480, margin: '4px auto 0' }}>
              {props.hint}
            </div>
          )}
        </div>
      }
      style={{ padding: '32px 0' }}
    >
      {props.action}
    </Empty>
  );
}

/** The text the API put in `detail` (it comes in the language of the page), or the status. */
export function errorDetail(error: unknown): string | undefined {
  if (!error || typeof error !== 'object') return undefined;
  const data = (error as { data?: { detail?: unknown } }).data;
  if (data && typeof data.detail === 'string') return data.detail;
  const status = (error as { status?: number }).status;
  return status ? `HTTP ${status}` : undefined;
}

/** The machine-readable part of an API error: `extra.code` and, for `blocked_by`, the blocking kinds. */
export function errorExtra(
  error: unknown,
): { type?: string; code?: string; blocked_by?: string[] } | undefined {
  const data = (error as { data?: { extra?: unknown } } | undefined)?.data;
  return data && typeof data.extra === 'object'
    ? (data.extra as { type?: string; code?: string; blocked_by?: string[] })
    : undefined;
}
