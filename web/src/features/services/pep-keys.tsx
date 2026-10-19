'use client';

import { Alert, Button, Drawer, Flex, Modal, Table, Tooltip, Typography, message } from 'antd';
import { Copy } from 'lucide-react';
import { useState } from 'react';
import { useTranslation } from 'react-i18next';

import type { PEPClient, PEPKey } from '@/api/admin/model';
import { useIssuePepKey, useListPepKeys, useRevokePepKey } from '@/api/admin/pep/pep';
import { DecisionTag, EmptyBlock, errorDetail, ErrorBlock, LoadingBlock } from '@/components/kit';
import { formatDateTime } from '@/domain/format';

import { useRefreshAll } from '../data/queries';
import { activeKeys, keyState, MAX_ACTIVE_KEYS } from './pep-model';

/**
 * The API keys of an api_key client: when each was issued and whether it works, issuing a new one and revoking one. A key's
 * secret is shown once, when it is issued, and is held by nothing but the dialog that shows it.
 */
export function PepKeysDrawer(props: {
  serviceId: string;
  client: PEPClient | null;
  canWrite: boolean;
  onClose: () => void;
}) {
  const { t, i18n } = useTranslation();
  const lang = i18n.language;
  const refresh = useRefreshAll();
  const client = props.client;
  const keys = useListPepKeys(props.serviceId, client?.id ?? '', {
    query: { enabled: !!client, select: (r) => (r.status === 200 ? (r.data ?? []) : []) },
  });
  // The answer that carries the secret is not kept in the query cache once the dialog is gone (gcTime 0).
  const issue = useIssuePepKey({ mutation: { gcTime: 0 } });
  const revoke = useRevokePepKey();
  const [issued, setIssued] = useState<string | null>(null);
  const [error, setError] = useState<unknown>(null);

  const full = activeKeys(keys.data ?? []) >= MAX_ACTIVE_KEYS;

  const issueKey = async () => {
    if (!client) return;
    setError(null);
    try {
      const res = await issue.mutateAsync({ serviceId: props.serviceId, clientId: client.id });
      if (res.status === 200) setIssued(res.data.key);
      await refresh();
    } catch (e) {
      setError(e);
    }
  };

  const closeIssued = () => {
    setIssued(null);
    issue.reset();
  };

  const askRevoke = (key: PEPKey) =>
    Modal.confirm({
      title: t('pep.keys.revoke.title', { id: key.key_id }),
      content: t('pep.keys.revoke.body'),
      okText: t('pep.keys.revoke.action'),
      okButtonProps: { danger: true },
      cancelText: t('common.cancel'),
      onOk: async () => {
        if (!client) return;
        try {
          await revoke.mutateAsync({
            serviceId: props.serviceId,
            clientId: client.id,
            keyId: key.key_id,
          });
          await refresh();
        } catch (e) {
          Modal.error({ title: t('common.saveFailed'), content: errorDetail(e) });
        }
      },
    });

  return (
    <>
      <Drawer
        open={!!client}
        onClose={props.onClose}
        size={640}
        destroyOnHidden
        title={t('pep.keys.title', { name: client?.name })}
        extra={
          props.canWrite && (
            <Tooltip title={full ? t('pep.keys.full', { count: MAX_ACTIVE_KEYS }) : undefined}>
              <Button
                type="primary"
                disabled={full || keys.isPending}
                loading={issue.isPending}
                onClick={() => void issueKey()}
              >
                {t('pep.keys.issue')}
              </Button>
            </Tooltip>
          )
        }
      >
        <Flex vertical gap={12}>
          <Typography.Paragraph type="secondary" style={{ margin: 0 }}>
            {t('pep.keys.hint')}
          </Typography.Paragraph>
          {error != null && (
            <Alert type="error" showIcon title={errorDetail(error) ?? t('common.saveFailed')} />
          )}
          {keys.isPending ? (
            <LoadingBlock rows={2} />
          ) : keys.isError ? (
            <ErrorBlock error={keys.error} onRetry={() => void keys.refetch()} />
          ) : (keys.data ?? []).length === 0 ? (
            <EmptyBlock title={t('pep.keys.empty')} hint={t('pep.keys.emptyHint')} />
          ) : (
            <Table<PEPKey>
              rowKey="key_id"
              size="small"
              pagination={false}
              dataSource={keys.data}
              columns={[
                {
                  title: t('pep.keys.col.id'),
                  key: 'id',
                  render: (_, k) => <span className="mono">lct_{k.key_id}</span>,
                },
                {
                  title: t('pep.keys.col.created'),
                  key: 'created',
                  render: (_, k) => formatDateTime(k.created_at, lang),
                },
                {
                  title: t('pep.keys.col.state'),
                  key: 'state',
                  render: (_, k) => {
                    const state = keyState(k);
                    return (
                      <DecisionTag
                        decision={
                          state === 'active' ? 'allow' : state === 'revoked' ? 'deny' : 'inherit'
                        }
                      >
                        {t(`pep.keys.state.${state}`)}
                      </DecisionTag>
                    );
                  },
                },
                {
                  title: t('pep.keys.col.until'),
                  key: 'until',
                  render: (_, k) =>
                    k.revoked_at
                      ? t('pep.keys.revokedAt', { at: formatDateTime(k.revoked_at, lang) })
                      : k.expires_at
                        ? formatDateTime(k.expires_at, lang)
                        : t('pep.keys.never'),
                },
                {
                  title: '',
                  key: 'actions',
                  width: 100,
                  render: (_, k) =>
                    props.canWrite && keyState(k) === 'active' ? (
                      <Button size="small" danger onClick={() => askRevoke(k)}>
                        {t('pep.keys.revoke.action')}
                      </Button>
                    ) : null,
                },
              ]}
            />
          )}
        </Flex>
      </Drawer>
      <IssuedKeyModal secret={issued} onClose={closeIssued} />
    </>
  );
}

/** The key that was just issued, with the warning that this is the only time it is shown. Closing it forgets the key. */
function IssuedKeyModal({ secret, onClose }: { secret: string | null; onClose: () => void }) {
  const { t } = useTranslation();
  const [api, holder] = message.useMessage();
  return (
    <Modal
      open={secret !== null}
      title={t('pep.issued.title')}
      onCancel={onClose}
      mask={{ closable: false }}
      destroyOnHidden
      footer={
        <Button type="primary" onClick={onClose}>
          {t('pep.issued.done')}
        </Button>
      }
    >
      {holder}
      <Flex vertical gap={12}>
        <Alert type="warning" showIcon title={t('pep.issued.warning')} />
        <div style={{ position: 'relative' }}>
          <pre
            className="mono"
            data-testid="issued-key"
            style={{
              margin: 0,
              padding: '12px 48px 12px 12px',
              background: 'var(--licetum-inherit-bg)',
              borderRadius: 8,
              whiteSpace: 'pre-wrap',
              wordBreak: 'break-all',
              userSelect: 'all',
            }}
          >
            {secret}
          </pre>
          <Button
            size="small"
            icon={<Copy size={14} aria-hidden />}
            aria-label={t('common.copy')}
            style={{ position: 'absolute', top: 8, right: 8 }}
            onClick={async () => {
              await navigator.clipboard?.writeText(secret ?? '');
              void api.success(t('common.copied'), 1.5);
            }}
          />
        </div>
        <Typography.Text type="secondary" style={{ fontSize: 13 }}>
          {t('pep.issued.use')}
        </Typography.Text>
      </Flex>
    </Modal>
  );
}
