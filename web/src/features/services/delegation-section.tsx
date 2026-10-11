'use client';

import { Alert, Button, Flex, Select, Table, Tag, Tooltip, Typography, message } from 'antd';
import { Copy } from 'lucide-react';
import { useState } from 'react';
import { useTranslation } from 'react-i18next';

import type { DelegatedCall } from '@/api/admin/model';
import { useListDelegatedCalls, usePolicyClaims } from '@/api/admin/policy/policy';
import { useUpdateService } from '@/api/admin/service/service';
import { EmptyBlock, errorDetail, ErrorBlock, LoadingBlock } from '@/components/kit';
import { usePreferences } from '@/components/providers';
import { formatDateTime } from '@/domain/format';
import { useAccess } from '@/lib/access';

import { useRefreshAll } from '../data/queries';
import { useService } from './service-frame';

const card = {
  background: 'var(--licetum-surface)',
  border: '1px solid var(--licetum-border)',
  borderRadius: 8,
  padding: 16,
} as const;

/** The curl call that asks on behalf of a person, with the claims the policy reads as placeholders. */
function example(checkUrl: string, slug: string, claims: string[]): string {
  const body = {
    subject: 'alice',
    claims: Object.fromEntries(
      claims.filter((c) => c !== 'preferred_username').map((c) => [c, '…']),
    ),
    checks: [{ type: 'TYPE', resource_id: 'RESOURCE_ID', action: 'read' }],
  };
  return `curl -X POST '${checkUrl}/service/${slug}/on_behalf/check' \\\n  -H "Authorization: Bearer $CLIENT_TOKEN" \\\n  -H 'Content-Type: application/json' \\\n  -d '${JSON.stringify(body)}'`;
}

/**
 * Asking on behalf of a person: which client applications the service trusts to do it, which claims they have to send
 * (the ones the policy reads), a call to copy, and the audit of the calls made.
 */
export function DelegationSection({ checkUrl }: { checkUrl: string }) {
  const { t } = useTranslation();
  const { lang } = usePreferences();
  const access = useAccess();
  const { service } = useService();
  const refresh = useRefreshAll();
  const update = useUpdateService();
  const [api, holder] = message.useMessage();
  const canWrite = access.any('service', 'write');

  const [draft, setDraft] = useState<string[] | null>(null);
  const [error, setError] = useState<unknown>(null);
  const clients = draft ?? service.trusted_clients;

  const claims = usePolicyClaims(service.id, {
    query: { select: (r) => (r.status === 200 ? (r.data ?? []) : []) },
  });
  const [before, setBefore] = useState(0);
  const calls = useListDelegatedCalls(
    service.id,
    { limit: 20, ...(before ? { before } : {}) },
    { query: { select: (r) => (r.status === 200 ? r.data : null) } },
  );

  const save = async () => {
    if (!draft) return;
    setError(null);
    try {
      await update.mutateAsync({ serviceId: service.id, data: { trusted_clients: draft } });
      await refresh();
      setDraft(null);
      void api.success(t('delegation.saved'), 2);
    } catch (e) {
      setError(e);
    }
  };

  const read = (claims.data ?? []).filter((c) => c.source === 'claim');
  const context = (claims.data ?? []).filter((c) => c.source === 'context');
  const snippet = example(
    checkUrl,
    service.slug,
    read.map((c) => c.name),
  );

  return (
    <section style={card} aria-labelledby="delegation" data-testid="delegation">
      {holder}
      <Typography.Title level={5} id="delegation" style={{ marginTop: 0 }}>
        {t('delegation.title')}
      </Typography.Title>
      <Typography.Paragraph type="secondary">{t('delegation.intro')}</Typography.Paragraph>

      <Flex vertical gap={8} style={{ marginBottom: 16 }}>
        <Typography.Text strong>{t('delegation.clients')}</Typography.Text>
        <Select
          mode="tags"
          aria-label={t('delegation.clients')}
          style={{ width: '100%' }}
          tokenSeparators={[',', ' ']}
          value={clients}
          onChange={(value) => setDraft(value)}
          disabled={!canWrite}
          placeholder={t('delegation.clientsPlaceholder')}
          open={false}
          suffixIcon={null}
        />
        <Typography.Text type="secondary" style={{ fontSize: 12 }}>
          {t('delegation.clientsHelp')}
        </Typography.Text>
        {error != null && (
          <Alert type="error" showIcon title={errorDetail(error) ?? t('common.saveFailed')} />
        )}
        {draft && (
          <Flex gap={8}>
            <Button type="primary" loading={update.isPending} onClick={() => void save()}>
              {t('common.save')}
            </Button>
            <Button onClick={() => setDraft(null)}>{t('common.cancel')}</Button>
          </Flex>
        )}
      </Flex>

      <Flex vertical gap={8} style={{ marginBottom: 16 }}>
        <Typography.Text strong>{t('delegation.claims.title')}</Typography.Text>
        {claims.isPending ? (
          <LoadingBlock rows={1} />
        ) : claims.isError ? (
          <ErrorBlock error={claims.error} onRetry={() => void claims.refetch()} />
        ) : read.length + context.length === 0 ? (
          <Typography.Text type="secondary">{t('delegation.claims.none')}</Typography.Text>
        ) : (
          <>
            <Flex gap={6} wrap data-testid="policy-claims">
              {read.map((c) => (
                <Tooltip
                  key={c.name}
                  title={t('delegation.claims.usage', {
                    rules: c.rules,
                    targets: c.targets,
                    memberships: c.memberships,
                  })}
                >
                  <Tag className="mono" style={{ margin: 0 }}>
                    {c.name}
                  </Tag>
                </Tooltip>
              ))}
              {context.map((c) => (
                <Tooltip key={`ctx-${c.name}`} title={t('delegation.claims.contextHint')}>
                  <Tag className="mono" color="processing" style={{ margin: 0 }}>
                    request.{c.name}
                  </Tag>
                </Tooltip>
              ))}
            </Flex>
            <Alert type="warning" showIcon title={t('delegation.claims.warn')} />
          </>
        )}
      </Flex>

      <Flex vertical gap={8} style={{ marginBottom: 16 }}>
        <Typography.Text strong>{t('delegation.example')}</Typography.Text>
        <div style={{ position: 'relative' }}>
          <pre
            className="mono"
            style={{
              margin: 0,
              padding: '14px 16px',
              background: 'var(--licetum-inherit-bg)',
              borderRadius: 8,
              overflowX: 'auto',
            }}
          >
            {snippet}
          </pre>
          <Button
            size="small"
            icon={<Copy size={14} aria-hidden />}
            aria-label={t('common.copy')}
            style={{ position: 'absolute', top: 8, right: 8 }}
            onClick={async () => {
              await navigator.clipboard?.writeText(snippet);
              void api.success(t('common.copied'), 1.5);
            }}
          />
        </div>
      </Flex>

      <Flex vertical gap={8}>
        <Typography.Text strong>{t('delegation.calls.title')}</Typography.Text>
        {calls.isPending ? (
          <LoadingBlock rows={2} />
        ) : calls.isError ? (
          <ErrorBlock error={calls.error} onRetry={() => void calls.refetch()} />
        ) : !calls.data || calls.data.items.length === 0 ? (
          <EmptyBlock title={t('delegation.calls.empty')} hint={t('delegation.calls.emptyHint')} />
        ) : (
          <>
            <Table<DelegatedCall>
              size="small"
              rowKey="id"
              pagination={false}
              dataSource={calls.data.items}
              columns={[
                {
                  title: t('delegation.calls.at'),
                  key: 'at',
                  width: 170,
                  render: (_, c) => formatDateTime(c.at, lang),
                },
                {
                  title: t('delegation.calls.client'),
                  key: 'client',
                  render: (_, c) => <span className="mono">{c.caller_client}</span>,
                },
                {
                  title: t('delegation.calls.subject'),
                  key: 'subject',
                  render: (_, c) => <span className="mono">{c.subject}</span>,
                },
                {
                  title: t('delegation.calls.what'),
                  key: 'what',
                  render: (_, c) =>
                    c.operation === 'check'
                      ? t('delegation.calls.checked', { allowed: c.allowed, total: c.questions })
                      : t('delegation.calls.permission'),
                },
              ]}
            />
            {calls.data.next_before != null && (
              <Button onClick={() => setBefore(calls.data!.next_before!)}>
                {t('delegation.calls.older')}
              </Button>
            )}
          </>
        )}
      </Flex>
    </section>
  );
}
