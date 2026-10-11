'use client';

import {
  Alert,
  Button,
  Checkbox,
  Descriptions,
  Drawer,
  Flex,
  Form,
  Input,
  Modal,
  Tag,
  Typography,
} from 'antd';
import Link from 'next/link';
import { useRouter } from 'next/navigation';
import { useState } from 'react';
import { useTranslation } from 'react-i18next';

import type { ServiceUpdate } from '@/api/admin/model';
import { useDeleteService, useUpdateService } from '@/api/admin/service/service';
import { errorDetail, errorExtra, IdChip } from '@/components/kit';
import { usePreferences } from '@/components/providers';
import { formatTime } from '@/domain/format';
import { useAccess } from '@/lib/access';

import { useAllTypes, useChangeLog, useRefreshAll, useRules } from '../data/queries';
import { useChangeSentence } from '../history/change-line';
import { useService } from './service-frame';

const card = {
  background: 'var(--licetum-surface)',
  border: '1px solid var(--licetum-border)',
  borderRadius: 8,
  padding: 16,
  flex: '1 1 420px',
  minWidth: 0,
} as const;

export function ServiceOverview() {
  const { t } = useTranslation();
  const { lang } = usePreferences();
  const router = useRouter();
  const access = useAccess();
  const { service, system } = useService();
  const types = useAllTypes();
  const rules = useRules();
  const log = useChangeLog({ serviceId: service.id, enabled: access.whole('service', 'read') });
  const sentence = useChangeSentence();
  const refresh = useRefreshAll();
  const update = useUpdateService();
  const remove = useDeleteService();

  const [editing, setEditing] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const [form] = Form.useForm<ServiceUpdate & { secretMode: string }>();

  const canWrite = access.any('service', 'write');
  const ownTypes = (types.data ?? []).filter((x) => x.service_id === service.id);
  const ownRules = (rules.data ?? []).filter((r) => r.service_id === service.id);
  const recent = (log.data ?? []).slice(-5).reverse();

  const save = async (values: ServiceUpdate & { clearSecret?: boolean }) => {
    setError(null);
    const body: ServiceUpdate = {
      name: values.name,
      // An empty field removes the address or the icon (both are optional).
      url: values.url ?? '',
      icon: values.icon ?? '',
      issuer: values.issuer,
      client_id: values.client_id,
      authorization_url: values.authorization_url,
      token_url: values.token_url,
      jwks_url: values.jwks_url,
    };
    // Omitted keeps the secret; null removes it and makes the client public.
    if (values.clearSecret) body.client_secret = null;
    else if (values.client_secret) body.client_secret = values.client_secret;
    try {
      await update.mutateAsync({ serviceId: service.id, data: body });
      await refresh();
      setEditing(false);
    } catch (e) {
      setError(e);
    }
  };

  const askDelete = () => {
    let typed = '';
    Modal.confirm({
      title: t('service.delete.title', { name: service.name }),
      content: (
        <Flex vertical gap={8}>
          <span>{t('service.delete.body')}</span>
          <Input
            aria-label={t('service.delete.type', { slug: service.slug })}
            placeholder={service.slug}
            onChange={(e) => (typed = e.target.value)}
            className="mono"
          />
        </Flex>
      ),
      okText: t('common.delete'),
      okButtonProps: { danger: true },
      cancelText: t('common.cancel'),
      onOk: async () => {
        if (typed !== service.slug) {
          Modal.error({ title: t('service.delete.mismatch') });
          throw new Error('mismatch');
        }
        try {
          await remove.mutateAsync({ serviceId: service.id });
          await refresh();
          router.push('/services');
        } catch (e) {
          const extra = errorExtra(e);
          Modal.error({
            title: t('service.delete.blocked'),
            content:
              extra?.type === 'blocked_by'
                ? t('service.delete.blockedBy', { what: (extra.blocked_by ?? []).join(', ') })
                : errorDetail(e),
          });
          throw e;
        }
      },
    });
  };

  return (
    <>
      {system && (
        <Alert
          type="warning"
          showIcon
          title={t('services.system.hint')}
          style={{ marginBottom: 16 }}
        />
      )}
      {ownTypes.length === 0 && (
        <Alert
          type="warning"
          showIcon
          style={{ marginBottom: 16 }}
          title={t('service.incomplete.title')}
          description={t('service.incomplete.hint')}
          action={
            <Button onClick={() => router.push(`/services/${service.slug}/types`)}>
              {t('service.incomplete.cta')}
            </Button>
          }
        />
      )}

      <Flex gap={16} wrap align="flex-start">
        <section style={card} aria-labelledby="signin">
          <Flex justify="space-between" align="center" style={{ marginBottom: 8 }}>
            <Typography.Title level={5} id="signin" style={{ margin: 0 }}>
              {t('service.signin')}
            </Typography.Title>
            {canWrite && (
              <Button
                onClick={() => {
                  form.setFieldsValue({
                    ...service,
                    client_secret: undefined,
                    clearSecret: false,
                  } as never);
                  setError(null);
                  setEditing(true);
                }}
              >
                {t('service.edit')}
              </Button>
            )}
          </Flex>
          <Descriptions
            column={1}
            size="small"
            styles={{ label: { width: 140, color: 'var(--licetum-muted)' } }}
          >
            <Descriptions.Item label="Issuer">
              <span className="mono">{service.issuer ?? '—'}</span>
            </Descriptions.Item>
            <Descriptions.Item label={t('service.secretStatus')}>
              {service.has_client_secret ? (
                <Tag color="success">{t('service.secretSet')}</Tag>
              ) : (
                <Tag>{t('service.secretNone')}</Tag>
              )}
            </Descriptions.Item>
            <Descriptions.Item label="client_id">
              <span className="mono">{service.client_id ?? '—'}</span>
            </Descriptions.Item>
            <Descriptions.Item label={t('service.authUrl')}>
              <span className="mono">{service.authorization_url ?? '—'}</span>
            </Descriptions.Item>
            <Descriptions.Item label={t('service.tokenUrl')}>
              <span className="mono">{service.token_url ?? '—'}</span>
            </Descriptions.Item>
            <Descriptions.Item label="JWKS">
              <span className="mono">{service.jwks_url ?? '—'}</span>
            </Descriptions.Item>
            <Descriptions.Item label={t('service.appUrl')}>{service.url ?? '—'}</Descriptions.Item>
            <Descriptions.Item label="id">
              <IdChip value={service.id} />
            </Descriptions.Item>
          </Descriptions>
        </section>

        <section style={card} aria-labelledby="state">
          <Typography.Title level={5} id="state" style={{ marginTop: 0 }}>
            {t('service.state')}
          </Typography.Title>
          <Flex gap={24} wrap>
            <div>
              <div style={{ color: 'var(--licetum-muted)' }}>{t('services.col.types')}</div>
              <Link
                href={`/services/${service.slug}/types`}
                style={{ fontSize: 24, fontWeight: 600 }}
              >
                {ownTypes.length}
              </Link>
            </div>
            <div>
              <div style={{ color: 'var(--licetum-muted)' }}>{t('services.col.rules')}</div>
              <Link
                href={`/access?service=${encodeURIComponent(service.name)}`}
                style={{ fontSize: 24, fontWeight: 600 }}
              >
                {ownRules.length}
              </Link>
            </div>
          </Flex>
        </section>

        <section style={card} aria-labelledby="recent">
          <Flex justify="space-between" align="center">
            <Typography.Title level={5} id="recent" style={{ margin: 0 }}>
              {t('service.recent')}
            </Typography.Title>
            <Link href="/history">{t('service.allHistory')}</Link>
          </Flex>
          {recent.length === 0 ? (
            <Typography.Text type="secondary">{t('home.recentNone')}</Typography.Text>
          ) : (
            recent.map((r) => (
              <div
                key={r.id}
                style={{
                  padding: '6px 0',
                  borderBottom: '1px solid var(--licetum-border)',
                  fontSize: 13,
                }}
              >
                <span style={{ color: 'var(--licetum-muted)' }}>{formatTime(r.at, lang)}</span> ·{' '}
                {sentence(r)}
              </div>
            ))
          )}
        </section>
      </Flex>

      {canWrite && !system && (
        <section
          style={{
            marginTop: 16,
            border: '1px solid var(--licetum-deny-fg)',
            borderRadius: 8,
            padding: 16,
          }}
          aria-labelledby="danger"
        >
          <Typography.Title
            level={5}
            id="danger"
            style={{ marginTop: 0, color: 'var(--licetum-deny-fg)' }}
          >
            {t('service.danger')}
          </Typography.Title>
          <Flex gap={12} align="center" wrap>
            <Button danger onClick={askDelete}>
              {t('service.delete.action')}
            </Button>
            <Typography.Text type="secondary" style={{ fontSize: 13 }}>
              {t('service.delete.hint')}
            </Typography.Text>
          </Flex>
        </section>
      )}

      <Drawer
        open={editing}
        onClose={() => setEditing(false)}
        title={t('service.edit')}
        size={560}
        destroyOnHidden
        footer={
          <Flex justify="flex-end" gap={8}>
            <Button onClick={() => setEditing(false)}>{t('common.cancel')}</Button>
            <Button type="primary" loading={update.isPending} onClick={() => form.submit()}>
              {t('common.save')}
            </Button>
          </Flex>
        }
      >
        {error != null && (
          <Alert
            type="error"
            showIcon
            title={errorDetail(error) ?? t('common.saveFailed')}
            style={{ marginBottom: 16 }}
          />
        )}
        <Form
          form={form}
          layout="vertical"
          onFinish={(v) => void save(v as never)}
          requiredMark={false}
        >
          <Form.Item name="name" label={t('service.name')} rules={[{ required: true }]}>
            <Input />
          </Form.Item>
          <Form.Item name="url" label={t('service.appUrl')}>
            <Input />
          </Form.Item>
          <Form.Item name="icon" label={t('service.iconUrl')}>
            <Input />
          </Form.Item>
          <Form.Item name="issuer" label="Issuer">
            <Input className="mono" />
          </Form.Item>
          <Form.Item name="client_id" label="client_id">
            <Input className="mono" />
          </Form.Item>
          <Form.Item
            name="client_secret"
            label={t('service.newSecret')}
            extra={t('service.secretHelp')}
          >
            <Input.Password autoComplete="new-password" placeholder={t('service.secretKeep')} />
          </Form.Item>
          <Form.Item name="clearSecret" valuePropName="checked">
            <Checkbox disabled={!service.has_client_secret}>{t('service.makePublic')}</Checkbox>
          </Form.Item>
          <Form.Item name="authorization_url" label={t('service.authUrl')}>
            <Input className="mono" />
          </Form.Item>
          <Form.Item name="token_url" label={t('service.tokenUrl')}>
            <Input className="mono" />
          </Form.Item>
          <Form.Item name="jwks_url" label="JWKS">
            <Input className="mono" />
          </Form.Item>
        </Form>
      </Drawer>
    </>
  );
}
