'use client';

import {
  Alert,
  Button,
  Checkbox,
  Drawer,
  Flex,
  Input,
  Modal,
  Select,
  Table,
  Tag,
  Typography,
} from 'antd';
import { useState } from 'react';
import { useTranslation } from 'react-i18next';

import { PEPIssuerAlgsItem, type PEPIssuer } from '@/api/admin/model';
import { useDeletePepIssuer, useListPepIssuers, usePutPepIssuer } from '@/api/admin/pep/pep';
import { EmptyBlock, errorDetail, ErrorBlock, LoadingBlock, PageHeader } from '@/components/kit';
import { useAccess } from '@/lib/access';
import { discoveredEndpointsSchema } from '@/lib/schemas';

import { useRefreshAll } from '../data/queries';
import {
  emptyIssuerForm,
  formOfIssuer,
  issuerBody,
  issuerProblem,
  type IssuerForm,
} from '../services/pep-model';
import { SettingsTabs } from './settings-admins';

const ALGS = Object.values(PEPIssuerAlgsItem);

/**
 * The identity providers whose access tokens the PEPs of the services may present. They are shared by all services: a PEP
 * client of a service names one of them and the client id its tokens carry.
 */
export function SettingsIssuers() {
  const { t } = useTranslation();
  const access = useAccess();
  const refresh = useRefreshAll();
  const remove = useDeletePepIssuer();
  const issuers = useListPepIssuers({
    query: { select: (r) => (r.status === 200 ? (r.data ?? []) : []) },
  });
  const [drawer, setDrawer] = useState<{ issuer: PEPIssuer | null } | null>(null);
  const canWrite = access.whole('service', 'write');

  const askDelete = (issuer: PEPIssuer) =>
    Modal.confirm({
      title: t('issuers.delete.title', { issuer: issuer.issuer }),
      content: t('issuers.delete.body'),
      okText: t('common.delete'),
      okButtonProps: { danger: true },
      cancelText: t('common.cancel'),
      onOk: async () => {
        try {
          await remove.mutateAsync({ params: { issuer: issuer.issuer } });
          await refresh();
        } catch (e) {
          // 409: a client still uses it (a deleted client included).
          Modal.error({ title: t('issuers.delete.failed'), content: errorDetail(e) });
        }
      },
    });

  return (
    <div className="page">
      <PageHeader
        title={t('settings.title')}
        extra={
          canWrite && (
            <Button type="primary" onClick={() => setDrawer({ issuer: null })}>
              {t('issuers.add')}
            </Button>
          )
        }
      />
      <SettingsTabs />

      <Flex vertical gap={16}>
        <Typography.Paragraph type="secondary" style={{ margin: 0, maxWidth: 760 }}>
          {t('issuers.hint')}
        </Typography.Paragraph>
        {issuers.isPending ? (
          <LoadingBlock rows={3} />
        ) : issuers.isError ? (
          <ErrorBlock error={issuers.error} onRetry={() => void issuers.refetch()} />
        ) : (issuers.data ?? []).length === 0 ? (
          <EmptyBlock
            title={t('issuers.empty.title')}
            hint={t('issuers.empty.hint')}
            action={
              canWrite ? (
                <Button type="primary" onClick={() => setDrawer({ issuer: null })}>
                  {t('issuers.add')}
                </Button>
              ) : undefined
            }
          />
        ) : (
          <Table<PEPIssuer>
            rowKey="issuer"
            size="middle"
            pagination={false}
            dataSource={issuers.data}
            scroll={{ x: 900 }}
            columns={[
              {
                title: t('issuers.col.issuer'),
                key: 'issuer',
                render: (_, i) => (
                  <Flex vertical gap={2}>
                    <b className="mono" style={{ wordBreak: 'break-all' }}>
                      {i.issuer}
                    </b>
                    <span
                      className="mono"
                      style={{ color: 'var(--licetum-muted)', wordBreak: 'break-all' }}
                    >
                      {i.jwks_url}
                    </span>
                  </Flex>
                ),
              },
              {
                title: t('issuers.col.audiences'),
                key: 'audiences',
                render: (_, i) =>
                  i.audiences && i.audiences.length > 0 ? (
                    <Flex gap={4} wrap>
                      {i.audiences.map((a) => (
                        <Tag key={a} className="mono" style={{ margin: 0 }}>
                          {a}
                        </Tag>
                      ))}
                    </Flex>
                  ) : (
                    <span style={{ color: 'var(--licetum-muted)' }}>
                      {t('issuers.audienceDefault')}
                    </span>
                  ),
              },
              {
                title: t('issuers.col.algs'),
                key: 'algs',
                render: (_, i) =>
                  i.algs && i.algs.length > 0 ? (
                    <Flex gap={4} wrap>
                      {i.algs.map((a) => (
                        <Tag key={a} className="mono" style={{ margin: 0 }}>
                          {a}
                        </Tag>
                      ))}
                    </Flex>
                  ) : (
                    <span style={{ color: 'var(--licetum-muted)' }}>
                      {t('issuers.algsDefault')}
                    </span>
                  ),
              },
              {
                title: t('issuers.col.typ'),
                key: 'typ',
                width: 120,
                render: (_, i) =>
                  i.require_typ ? (
                    <Tag className="mono" style={{ margin: 0 }}>
                      at+jwt
                    </Tag>
                  ) : (
                    <span style={{ color: 'var(--licetum-muted)' }}>—</span>
                  ),
              },
              {
                title: '',
                key: 'actions',
                width: 160,
                render: (_, i) =>
                  canWrite ? (
                    <Flex gap={8}>
                      <Button size="small" onClick={() => setDrawer({ issuer: i })}>
                        {t('common.edit')}
                      </Button>
                      <Button size="small" danger onClick={() => askDelete(i)}>
                        {t('common.delete')}
                      </Button>
                    </Flex>
                  ) : null,
              },
            ]}
          />
        )}
        <Typography.Paragraph type="secondary" style={{ fontSize: 12, margin: 0 }}>
          {t('issuers.clientsNote')}
        </Typography.Paragraph>
      </Flex>

      <IssuerDrawer
        state={drawer}
        existing={(issuers.data ?? []).map((i) => i.issuer)}
        onClose={() => setDrawer(null)}
      />
    </div>
  );
}

function IssuerDrawer(props: {
  state: { issuer: PEPIssuer | null } | null;
  existing: string[];
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const put = usePutPepIssuer();
  const refresh = useRefreshAll();
  const [form, setForm] = useState<IssuerForm>(emptyIssuerForm);
  const [error, setError] = useState<unknown>(null);
  const [detect, setDetect] = useState<'idle' | 'busy' | 'ok' | 'fail'>('idle');
  const [loaded, setLoaded] = useState<string | null>(null);

  const editing = props.state?.issuer ?? null;
  const key = props.state ? (editing?.issuer ?? 'new') : null;
  if (key !== loaded) {
    setLoaded(key);
    if (props.state) {
      setForm(editing ? formOfIssuer(editing) : emptyIssuerForm());
      setError(null);
      setDetect('idle');
    }
  }

  const set = (next: Partial<IssuerForm>) => setForm((f) => ({ ...f, ...next }));
  const problem = issuerProblem(form);
  // Registering a name that exists replaces its settings: said before it happens.
  const replaces = !editing && props.existing.includes(form.issuer.trim());

  const discover = async () => {
    const issuer = form.issuer.trim();
    if (!issuer) return;
    setDetect('busy');
    try {
      const res = await fetch(`/api/discover?issuer=${encodeURIComponent(issuer)}`);
      if (!res.ok) throw new Error(String(res.status));
      const found = discoveredEndpointsSchema.parse(await res.json());
      // The name of an issuer is compared with the iss of a token exactly: the document says what it is.
      set({
        jwksUrl: found.jwks_url ?? form.jwksUrl,
        ...(!editing && found.issuer ? { issuer: found.issuer } : {}),
      });
      setDetect(found.jwks_url ? 'ok' : 'fail');
    } catch {
      setDetect('fail');
    }
  };

  const submit = async () => {
    setError(null);
    try {
      await put.mutateAsync({ data: issuerBody(form) });
      await refresh();
      props.onClose();
    } catch (e) {
      setError(e);
    }
  };

  return (
    <Drawer
      open={!!props.state}
      onClose={props.onClose}
      size={560}
      destroyOnHidden
      title={editing ? t('issuers.drawer.edit') : t('issuers.drawer.add')}
      footer={
        <Flex justify="flex-end" gap={8}>
          <Button onClick={props.onClose}>{t('common.cancel')}</Button>
          <Button
            type="primary"
            disabled={problem !== null}
            loading={put.isPending}
            onClick={() => void submit()}
          >
            {editing ? t('common.save') : t('common.create')}
          </Button>
        </Flex>
      }
    >
      <Flex vertical gap={16}>
        {error != null && (
          <Alert type="error" showIcon title={errorDetail(error) ?? t('common.saveFailed')} />
        )}
        <div>
          <label
            htmlFor="issuer-name"
            style={{ fontWeight: 600, display: 'block', marginBottom: 4 }}
          >
            {t('issuers.field.issuer')}
          </label>
          <Flex gap={8}>
            <Input
              id="issuer-name"
              className="mono"
              value={form.issuer}
              disabled={!!editing}
              placeholder="https://sso.example.com/realms/main"
              onChange={(e) => set({ issuer: e.target.value })}
              onPressEnter={() => void discover()}
            />
            <Button loading={detect === 'busy'} onClick={() => void discover()}>
              {t('issuers.detectBtn')}
            </Button>
          </Flex>
          <Typography.Text type="secondary" style={{ fontSize: 12 }}>
            {t('issuers.field.issuerHelp')}
          </Typography.Text>
          {detect === 'ok' && (
            <Alert
              style={{ marginTop: 8 }}
              type="success"
              showIcon
              title={t('issuers.detect.ok')}
            />
          )}
          {detect === 'fail' && (
            <Alert
              style={{ marginTop: 8 }}
              type="warning"
              showIcon
              title={t('issuers.detect.fail')}
            />
          )}
          {replaces && (
            <Alert style={{ marginTop: 8 }} type="info" showIcon title={t('issuers.replaces')} />
          )}
        </div>

        <div>
          <label
            htmlFor="issuer-jwks"
            style={{ fontWeight: 600, display: 'block', marginBottom: 4 }}
          >
            {t('issuers.field.jwks')}
          </label>
          <Input
            id="issuer-jwks"
            className="mono"
            value={form.jwksUrl}
            placeholder="https://sso.example.com/realms/main/protocol/openid-connect/certs"
            onChange={(e) => set({ jwksUrl: e.target.value })}
          />
          <Typography.Text type="secondary" style={{ fontSize: 12 }}>
            {t('issuers.field.jwksHelp')}
          </Typography.Text>
        </div>

        <div>
          <div style={{ fontWeight: 600, marginBottom: 4 }}>{t('issuers.field.audiences')}</div>
          <Select
            mode="tags"
            aria-label={t('issuers.field.audiences')}
            style={{ width: '100%' }}
            tokenSeparators={[',', ' ']}
            open={false}
            suffixIcon={null}
            value={form.audiences}
            placeholder={t('issuers.field.audiencesPlaceholder')}
            onChange={(audiences) => set({ audiences })}
          />
          <Typography.Text type="secondary" style={{ fontSize: 12 }}>
            {t('issuers.field.audiencesHelp')}
          </Typography.Text>
        </div>

        <div>
          <div style={{ fontWeight: 600, marginBottom: 4 }}>{t('issuers.field.algs')}</div>
          <Select
            mode="multiple"
            aria-label={t('issuers.field.algs')}
            style={{ width: '100%' }}
            value={form.algs}
            placeholder={t('issuers.algsDefault')}
            onChange={(algs) => set({ algs })}
            options={ALGS.map((a) => ({ value: a, label: a }))}
          />
          <Typography.Text type="secondary" style={{ fontSize: 12 }}>
            {t('issuers.field.algsHelp')}
          </Typography.Text>
        </div>

        <Checkbox checked={form.requireTyp} onChange={(e) => set({ requireTyp: e.target.checked })}>
          {t('issuers.field.requireTyp')}
        </Checkbox>
        {problem && (
          <Typography.Text type="secondary" style={{ fontSize: 12 }}>
            {t(problem)}
          </Typography.Text>
        )}
      </Flex>
    </Drawer>
  );
}
