'use client';

import { Alert, Button, Flex, Modal, Switch, Table, Tag, Tooltip, Typography } from 'antd';
import Link from 'next/link';
import { useState } from 'react';
import { useTranslation } from 'react-i18next';

import type { PEPClient } from '@/api/admin/model';
import { useDeletePepClient, useListPepClients, useUpdatePepClient } from '@/api/admin/pep/pep';
import { usePolicyProperties } from '@/api/admin/policy/policy';
import { DecisionTag, EmptyBlock, errorDetail, ErrorBlock, LoadingBlock } from '@/components/kit';
import { useAccess } from '@/lib/access';

import { useRefreshAll } from '../data/queries';
import { PepClientDrawer } from './pep-client-drawer';
import { PepKeysDrawer } from './pep-keys';
import { useService } from './service-frame';

const card = {
  background: 'var(--licetum-surface)',
  border: '1px solid var(--licetum-border)',
  borderRadius: 8,
  padding: 16,
} as const;

/**
 * The applications that ask this service's PDP (its PEPs) and how each is known: an API key Licetum issues, or the access
 * tokens of a registered issuer. A client may call only the operations it was given.
 */
export function PepTab() {
  const { t } = useTranslation();
  const access = useAccess();
  const { service } = useService();
  const refresh = useRefreshAll();
  const update = useUpdatePepClient();
  const remove = useDeletePepClient();
  const clients = useListPepClients(service.id, {
    query: { select: (r) => (r.status === 200 ? (r.data ?? []) : []) },
  });

  const [drawer, setDrawer] = useState<{ client: PEPClient | null } | null>(null);
  const [keysOf, setKeysOf] = useState<PEPClient | null>(null);
  const [error, setError] = useState<unknown>(null);
  const canWrite = access.any('service', 'write');

  const toggle = async (client: PEPClient, enabled: boolean) => {
    setError(null);
    try {
      await update.mutateAsync({ serviceId: service.id, clientId: client.id, data: { enabled } });
      await refresh();
    } catch (e) {
      setError(e);
    }
  };

  const askDelete = (client: PEPClient) =>
    Modal.confirm({
      title: t('pep.delete.title', { name: client.name }),
      content: t('pep.delete.body'),
      okText: t('common.delete'),
      okButtonProps: { danger: true },
      cancelText: t('common.cancel'),
      onOk: async () => {
        try {
          await remove.mutateAsync({ serviceId: service.id, clientId: client.id });
          await refresh();
        } catch (e) {
          Modal.error({ title: t('common.saveFailed'), content: errorDetail(e) });
        }
      },
    });

  if (clients.isPending) return <LoadingBlock rows={3} />;
  if (clients.isError)
    return <ErrorBlock error={clients.error} onRetry={() => void clients.refetch()} />;

  const list = clients.data ?? [];
  const create = (
    <Button type="primary" onClick={() => setDrawer({ client: null })}>
      {t('pep.create')}
    </Button>
  );

  return (
    <Flex vertical gap={16}>
      <Flex justify="space-between" align="flex-start" gap={16} wrap>
        <Typography.Paragraph type="secondary" style={{ margin: 0, maxWidth: 720, flex: 1 }}>
          {t('pep.hint')}
        </Typography.Paragraph>
        {canWrite && list.length > 0 && create}
      </Flex>
      {error != null && (
        <Alert type="error" showIcon title={errorDetail(error) ?? t('common.saveFailed')} />
      )}

      {list.length === 0 ? (
        <EmptyBlock
          title={t('pep.empty.title')}
          hint={t('pep.empty.hint')}
          action={canWrite ? create : undefined}
        />
      ) : (
        <Table<PEPClient>
          rowKey="id"
          size="middle"
          pagination={false}
          dataSource={list}
          scroll={{ x: 880 }}
          columns={[
            {
              title: t('pep.col.name'),
              key: 'name',
              render: (_, c) => <b>{c.name}</b>,
            },
            {
              title: t('pep.col.kind'),
              key: 'kind',
              width: 150,
              render: (_, c) => <Tag style={{ margin: 0 }}>{t(`pep.kind.${c.kind}`)}</Tag>,
            },
            {
              title: t('pep.col.identity'),
              key: 'identity',
              render: (_, c) =>
                c.kind === 'jwt' ? (
                  <Flex vertical gap={2}>
                    <span className="mono" style={{ wordBreak: 'break-all' }}>
                      {c.issuer}
                    </span>
                    <span className="mono" style={{ color: 'var(--licetum-muted)' }}>
                      {c.client_id}
                    </span>
                  </Flex>
                ) : (
                  <span style={{ color: 'var(--licetum-muted)' }}>{t('pep.keyIdentity')}</span>
                ),
            },
            {
              title: t('pep.col.operations'),
              key: 'operations',
              render: (_, c) => (
                <Flex gap={4} wrap>
                  {c.operations.map((op) => (
                    <Tag key={op} className="mono" style={{ margin: 0 }}>
                      {op}
                    </Tag>
                  ))}
                </Flex>
              ),
            },
            {
              title: t('pep.col.enabled'),
              key: 'enabled',
              width: 110,
              render: (_, c) =>
                canWrite ? (
                  <Tooltip title={c.enabled ? t('pep.disableHint') : t('pep.enableHint')}>
                    <Switch
                      checked={c.enabled}
                      aria-label={`${t('pep.col.enabled')}: ${c.name}`}
                      loading={update.isPending && update.variables?.clientId === c.id}
                      onChange={(checked) => void toggle(c, checked)}
                    />
                  </Tooltip>
                ) : c.enabled ? (
                  <DecisionTag decision="allow">{t('pep.enabled')}</DecisionTag>
                ) : (
                  <DecisionTag decision="deny">{t('pep.disabled')}</DecisionTag>
                ),
            },
            {
              title: '',
              key: 'actions',
              width: 260,
              render: (_, c) => (
                <Flex gap={8} wrap>
                  {c.kind === 'api_key' && (
                    <Button size="small" onClick={() => setKeysOf(c)}>
                      {t('pep.keys.open')}
                    </Button>
                  )}
                  {canWrite && (
                    <Button size="small" onClick={() => setDrawer({ client: c })}>
                      {t('common.edit')}
                    </Button>
                  )}
                  {canWrite && (
                    <Button size="small" danger onClick={() => askDelete(c)}>
                      {t('common.delete')}
                    </Button>
                  )}
                </Flex>
              ),
            },
          ]}
        />
      )}

      <PropertiesPanel />

      <Typography.Paragraph type="secondary" style={{ fontSize: 12, margin: 0 }}>
        {t('pep.issuersNote')} <Link href="/settings/issuers">{t('settings.tab.issuers')}</Link>
        <br />
        {t('pep.integrationNote')}{' '}
        <Link href={`/services/${service.slug}/integration`}>{t('service.tab.integration')}</Link>
      </Typography.Paragraph>

      <PepClientDrawer
        open={drawer !== null}
        serviceId={service.id}
        state={drawer?.client ?? null}
        onClose={() => setDrawer(null)}
        onCreated={(created) => {
          // A new api_key client has no key yet: the next thing to do is to issue one.
          if (created.kind === 'api_key') setKeysOf(created);
        }}
      />
      <PepKeysDrawer
        serviceId={service.id}
        client={keysOf}
        canWrite={canWrite}
        onClose={() => setKeysOf(null)}
      />
    </Flex>
  );
}

/** What the PEPs of this service have to send for the answers to be the real ones: the properties and attributes the policy reads. */
function PropertiesPanel() {
  const { t } = useTranslation();
  const { service } = useService();
  const properties = usePolicyProperties(service.id, {
    query: { select: (r) => (r.status === 200 ? (r.data ?? []) : []) },
  });
  const read = (properties.data ?? []).filter((p) => p.source === 'claim');
  const context = (properties.data ?? []).filter((p) => p.source === 'context');

  return (
    <section style={card} aria-labelledby="pep-properties" data-testid="pep-properties">
      <Typography.Title level={5} id="pep-properties" style={{ marginTop: 0 }}>
        {t('pep.properties.title')}
      </Typography.Title>
      {properties.isPending ? (
        <LoadingBlock rows={1} />
      ) : properties.isError ? (
        <ErrorBlock error={properties.error} onRetry={() => void properties.refetch()} />
      ) : read.length + context.length === 0 ? (
        <Typography.Text type="secondary">{t('pep.properties.none')}</Typography.Text>
      ) : (
        <Flex vertical gap={8}>
          <Flex gap={6} wrap>
            {read.map((p) => (
              <Tooltip
                key={p.name}
                title={t('pep.properties.usage', {
                  rules: p.rules,
                  targets: p.targets,
                  memberships: p.memberships,
                })}
              >
                <Tag className="mono" style={{ margin: 0 }}>
                  subject.properties.{p.name}
                </Tag>
              </Tooltip>
            ))}
            {context.map((p) => (
              <Tooltip key={`context-${p.name}`} title={t('pep.properties.contextHint')}>
                <Tag className="mono" color="processing" style={{ margin: 0 }}>
                  context.{p.name}
                </Tag>
              </Tooltip>
            ))}
          </Flex>
          <Alert type="warning" showIcon title={t('pep.properties.warn')} />
        </Flex>
      )}
    </section>
  );
}
