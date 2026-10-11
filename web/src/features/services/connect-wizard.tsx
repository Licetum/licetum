'use client';

import { Alert, Button, Flex, Form, Input, Select, Steps, Typography } from 'antd';
import { Plus, X } from 'lucide-react';
import Link from 'next/link';
import { usePathname, useRouter, useSearchParams } from 'next/navigation';
import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';

import type { ServiceInput, TypeResourceWithService } from '@/api/admin/model';
import { useListResourcesOfType } from '@/api/admin/resource/resource';
import { useCreateService } from '@/api/admin/service/service';
import { useCreateTypeResource } from '@/api/admin/type-resource/type-resource';
import { DecisionTag, errorDetail, PageHeader } from '@/components/kit';
import { useAccess } from '@/lib/access';
import { discoveredEndpointsSchema } from '@/lib/schemas';

import { useAllTypes, useRefreshAll, useServices } from '../data/queries';
import { SourceDrawer } from './sources-tab';
import { useSourceKinds } from './types-tab';

const slugify = (name: string) =>
  name
    .toLowerCase()
    .replace(/[^a-z0-9_-]+/g, '-')
    .replace(/^[^a-z0-9]+|[-_]+$/g, '')
    .slice(0, 64);

type Draft = { name: string; parent: string };
let seq = 0;

/** Connecting a service in four steps — sign-in, resource types, source, check. Each step saves what it did, so a half-done setup can be continued. */
export function ConnectWizard() {
  const { t } = useTranslation();
  const router = useRouter();
  const access = useAccess();
  const services = useServices();
  const types = useAllTypes();
  const params = useSearchParams();
  const pathname = usePathname();
  const step = params.get('step') ?? '1';
  const slug = params.get('slug') ?? '';
  // Both parameters change in one navigation: two separate updates would each start from the old URL.
  const go = (nextStep: number, nextSlug = slug) => {
    const next = new URLSearchParams();
    if (nextSlug) next.set('slug', nextSlug);
    next.set('step', String(nextStep));
    router.replace(`${pathname}?${next.toString()}`, { scroll: false });
  };

  const service = (services.data ?? []).find((s) => s.slug === slug);
  const current = Math.min(Math.max(Number(step) || 1, 1), 4);
  const own = (types.data ?? []).filter((x) => x.service_id === service?.id);

  // A step beyond the first needs the service created in the first one.
  useEffect(() => {
    if (current > 1 && !slug) go(1);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [current, slug]);

  if (!access.whole('service', 'write')) {
    return (
      <div className="page">
        <Alert type="warning" showIcon title={t('wizard.noRight')} />
      </div>
    );
  }

  return (
    <div className="page">
      <PageHeader
        crumbs={[{ label: t('nav.services'), href: '/services' }, { label: t('wizard.title') }]}
        title={t('wizard.title')}
        extra={
          <Button href={service ? `/services/${service.slug}` : '/services'}>
            {t('wizard.leave')}
          </Button>
        }
      />
      <Steps
        current={current - 1}
        style={{ marginBottom: 24, maxWidth: 760 }}
        items={[
          { title: t('wizard.step.signin') },
          { title: t('wizard.step.types') },
          { title: t('wizard.step.source') },
          { title: t('wizard.step.check') },
        ]}
      />
      <div style={{ maxWidth: 760 }}>
        {current === 1 && (
          <StepSignIn existing={service?.slug} onDone={(created) => go(2, created)} />
        )}
        {current === 2 && service && (
          <StepTypes
            serviceId={service.id}
            existing={own}
            onBack={() => go(1)}
            onDone={() => go(3)}
          />
        )}
        {current === 3 && service && (
          <StepSources
            serviceId={service.id}
            types={own}
            onBack={() => go(2)}
            onDone={() => go(4)}
          />
        )}
        {current === 4 && service && (
          <StepCheck
            slug={service.slug}
            types={own}
            onBack={() => go(3)}
            onDone={() => router.push(`/services/${service.slug}`)}
          />
        )}
      </div>
    </div>
  );
}

function StepSignIn({ existing, onDone }: { existing?: string; onDone: (slug: string) => void }) {
  const { t } = useTranslation();
  const create = useCreateService();
  const refresh = useRefreshAll();
  const [form] = Form.useForm<ServiceInput & { advanced?: boolean }>();
  const [error, setError] = useState<unknown>(null);
  const [discovery, setDiscovery] = useState<'idle' | 'busy' | 'ok' | 'fail'>('idle');
  const [advanced, setAdvanced] = useState(false);

  if (existing) {
    return (
      <Flex vertical gap={12}>
        <Alert type="success" showIcon title={t('wizard.signin.done', { slug: existing })} />
        <Flex gap={8}>
          <Button type="primary" onClick={() => onDone(existing)}>
            {t('wizard.next')}
          </Button>
        </Flex>
      </Flex>
    );
  }

  const discover = async () => {
    const issuer = String(form.getFieldValue('issuer') ?? '').trim();
    if (!issuer) return;
    setDiscovery('busy');
    try {
      const res = await fetch(`/api/discover?issuer=${encodeURIComponent(issuer)}`);
      if (!res.ok) throw new Error(String(res.status));
      const found = discoveredEndpointsSchema.parse(await res.json());
      form.setFieldsValue({
        authorization_url: found.authorization_url,
        token_url: found.token_url,
        jwks_url: found.jwks_url,
        ...(found.issuer ? { issuer: found.issuer } : {}),
      });
      setDiscovery('ok');
    } catch {
      setDiscovery('fail');
      setAdvanced(true);
    }
  };

  const submit = async (values: ServiceInput) => {
    setError(null);
    // The address and the icon are optional; an empty field is no address.
    const body: ServiceInput = {
      ...values,
      url: values.url?.trim() || null,
      icon: values.icon?.trim() || null,
      client_secret: values.client_secret || null,
    };
    try {
      await create.mutateAsync({ data: body });
      await refresh();
      onDone(values.slug);
    } catch (e) {
      setError(e);
    }
  };

  return (
    <Form
      form={form}
      layout="vertical"
      requiredMark={false}
      onFinish={(v) => void submit(v)}
      initialValues={{ client_secret: '' }}
    >
      <Flex vertical gap={0}>
        {error != null && (
          <Alert
            type="error"
            showIcon
            title={errorDetail(error) ?? t('common.saveFailed')}
            style={{ marginBottom: 16 }}
          />
        )}
        <Form.Item label={t('wizard.issuer')} htmlFor="issuer" extra={t('wizard.issuerHelp')}>
          <Flex gap={8}>
            <Form.Item name="issuer" noStyle rules={[{ required: true }]}>
              <Input
                id="issuer"
                placeholder="https://sso.example.com/realms/main"
                className="mono"
                onPressEnter={(e) => {
                  e.preventDefault();
                  void discover();
                }}
              />
            </Form.Item>
            <Button onClick={() => void discover()} loading={discovery === 'busy'}>
              {t('wizard.discoverBtn')}
            </Button>
          </Flex>
        </Form.Item>
        {discovery === 'ok' && (
          <Alert
            type="success"
            showIcon
            title={t('wizard.discover.ok')}
            style={{ marginBottom: 16 }}
          />
        )}
        {discovery === 'fail' && (
          <Alert
            type="warning"
            showIcon
            title={t('wizard.discover.fail')}
            style={{ marginBottom: 16 }}
          />
        )}
        <Button
          type="link"
          style={{ alignSelf: 'flex-start', paddingLeft: 0 }}
          onClick={() => setAdvanced((v) => !v)}
        >
          {advanced ? t('wizard.hideUrls') : t('wizard.showUrls')}
        </Button>
        <div style={{ display: advanced ? 'block' : 'none' }}>
          <Form.Item
            name="authorization_url"
            label={t('service.authUrl')}
            rules={[{ required: true }]}
          >
            <Input className="mono" />
          </Form.Item>
          <Form.Item name="token_url" label={t('service.tokenUrl')} rules={[{ required: true }]}>
            <Input className="mono" />
          </Form.Item>
          <Form.Item name="jwks_url" label="JWKS" rules={[{ required: true }]}>
            <Input className="mono" />
          </Form.Item>
        </div>
        <Flex gap={16} wrap>
          <Form.Item
            name="client_id"
            label="client_id"
            rules={[{ required: true }]}
            style={{ flex: 1, minWidth: 220 }}
          >
            <Input className="mono" />
          </Form.Item>
          <Form.Item
            name="client_secret"
            label={t('wizard.secretLabel')}
            extra={t('wizard.secret.help')}
            style={{ flex: 1, minWidth: 220 }}
          >
            <Input.Password autoComplete="new-password" />
          </Form.Item>
        </Flex>
        <Flex gap={16} wrap>
          <Form.Item
            name="name"
            label={t('service.name')}
            rules={[{ required: true }]}
            style={{ flex: 1, minWidth: 220 }}
          >
            <Input
              onChange={(e) => {
                if (!form.isFieldTouched('slug'))
                  form.setFieldValue('slug', slugify(e.target.value));
              }}
            />
          </Form.Item>
          <Form.Item
            name="slug"
            label="Slug"
            extra={t('wizard.slug.help')}
            rules={[
              {
                required: true,
                pattern: /^[a-z0-9][a-z0-9_-]*$/,
                message: t('wizard.slug.invalid'),
              },
            ]}
            style={{ flex: 1, minWidth: 220 }}
          >
            <Input className="mono" maxLength={64} />
          </Form.Item>
        </Flex>
        <Flex gap={16} wrap>
          <Form.Item
            name="url"
            label={t('service.appUrl')}
            extra={t('wizard.url.help')}
            style={{ flex: 1, minWidth: 220 }}
          >
            <Input placeholder="https://docs.example.com" />
          </Form.Item>
          <Form.Item
            name="icon"
            label={t('service.iconUrl')}
            extra={t('wizard.icon.help')}
            style={{ flex: 1, minWidth: 220 }}
          >
            <Input placeholder="https://docs.example.com/icon.png" />
          </Form.Item>
        </Flex>
        <Flex justify="flex-end" gap={8}>
          <Button type="primary" htmlType="submit" loading={create.isPending}>
            {t('wizard.next')}
          </Button>
        </Flex>
      </Flex>
    </Form>
  );
}

const TEMPLATES: Record<string, Draft[]> = {
  one: [{ name: '', parent: '' }],
  tree: [
    { name: 'folder', parent: '' },
    { name: 'document', parent: 'folder' },
  ],
};

function StepTypes({
  serviceId,
  existing,
  onBack,
  onDone,
}: {
  serviceId: string;
  existing: TypeResourceWithService[];
  onBack: () => void;
  onDone: () => void;
}) {
  const { t } = useTranslation();
  const create = useCreateTypeResource();
  const refresh = useRefreshAll();
  const [rows, setRows] = useState<(Draft & { id: number })[]>([]);
  const [error, setError] = useState<unknown>(null);

  const add = (drafts: Draft[]) => setRows(drafts.map((d) => ({ ...d, id: ++seq })));

  const save = async () => {
    setError(null);
    const created = new Map<string, string>(existing.map((x) => [x.name, x.id]));
    try {
      // Parents are created before their children: a row may name an earlier row (or an existing type) as its parent.
      for (const row of rows) {
        const parentId = row.parent ? created.get(row.parent) : undefined;
        const res = await create.mutateAsync({
          serviceId,
          data: {
            name: row.name.trim(),
            parents: parentId ? [{ type_resource_id: parentId }] : [],
            access_implies: { write: ['read'] },
          },
        });
        created.set(row.name.trim(), (res.data as { id: string }).id);
      }
      await refresh();
      setRows([]);
      onDone();
    } catch (e) {
      setError(e);
      await refresh();
    }
  };

  const names = [...existing.map((x) => x.name), ...rows.map((r) => r.name.trim()).filter(Boolean)];
  const ready = rows.length === 0 ? existing.length > 0 : rows.every((r) => r.name.trim());

  return (
    <Flex vertical gap={16}>
      <Typography.Paragraph type="secondary" style={{ margin: 0 }}>
        {t('wizard.types.hint')}
      </Typography.Paragraph>
      {error != null && (
        <Alert type="error" showIcon title={errorDetail(error) ?? t('common.saveFailed')} />
      )}
      {existing.length > 0 && (
        <Flex gap={8} wrap align="center">
          <span>{t('wizard.types.existing')}</span>
          {existing.map((x) => (
            <DecisionTag key={x.id} decision="inherit">
              {x.name}
            </DecisionTag>
          ))}
        </Flex>
      )}
      <Flex gap={8} wrap>
        <Button onClick={() => add(TEMPLATES.one!)}>{t('wizard.types.template.one')}</Button>
        <Button onClick={() => add(TEMPLATES.tree!)}>{t('wizard.types.template.tree')}</Button>
        <Button
          icon={<Plus size={14} aria-hidden />}
          onClick={() => setRows([...rows, { id: ++seq, name: '', parent: '' }])}
        >
          {t('wizard.types.add')}
        </Button>
      </Flex>
      {rows.map((r, i) => (
        <Flex key={r.id} gap={8} wrap align="center">
          <Input
            aria-label={t('types.col.name')}
            style={{ width: 240 }}
            placeholder={t('types.namePlaceholder')}
            value={r.name}
            onChange={(e) =>
              setRows(rows.map((x) => (x.id === r.id ? { ...x, name: e.target.value } : x)))
            }
          />
          <Select
            allowClear
            aria-label={t('types.parent')}
            placeholder={t('wizard.types.noParent')}
            style={{ width: 240 }}
            value={r.parent || undefined}
            onChange={(parent) =>
              setRows(rows.map((x) => (x.id === r.id ? { ...x, parent: parent ?? '' } : x)))
            }
            options={[
              ...existing.map((x) => x.name),
              ...rows
                .slice(0, i)
                .map((x) => x.name.trim())
                .filter(Boolean),
            ].map((n) => ({ value: n }))}
          />
          <Button
            type="text"
            aria-label={t('cond.remove')}
            icon={<X size={14} aria-hidden />}
            onClick={() => setRows(rows.filter((x) => x.id !== r.id))}
          />
        </Flex>
      ))}
      {names.length > 0 && rows.length > 0 && (
        <Typography.Text type="secondary" style={{ fontSize: 12 }}>
          {t('wizard.types.order')}
        </Typography.Text>
      )}
      <Flex justify="space-between">
        <Button onClick={onBack}>{t('wizard.back')}</Button>
        <Button
          type="primary"
          disabled={!ready}
          loading={create.isPending}
          onClick={() => (rows.length === 0 ? onDone() : void save())}
        >
          {t('wizard.next')}
        </Button>
      </Flex>
    </Flex>
  );
}

function StepSources({
  serviceId,
  types,
  onBack,
  onDone,
}: {
  serviceId: string;
  types: TypeResourceWithService[];
  onBack: () => void;
  onDone: () => void;
}) {
  const { t } = useTranslation();
  const { sources } = useSourceKinds(types);
  const [drawer, setDrawer] = useState<{ type: TypeResourceWithService; source: null } | null>(
    null,
  );
  return (
    <Flex vertical gap={16}>
      <Typography.Paragraph type="secondary" style={{ margin: 0 }}>
        {t('wizard.sources.hint')}
      </Typography.Paragraph>
      {types.map((x) => {
        const s = sources.get(x.id);
        return (
          <Flex
            key={x.id}
            gap={12}
            align="center"
            wrap
            style={{
              background: 'var(--licetum-surface)',
              border: '1px solid var(--licetum-border)',
              borderRadius: 8,
              padding: '10px 14px',
            }}
          >
            <b style={{ minWidth: 140 }}>{x.name}</b>
            {s ? (
              <DecisionTag decision="allow">{s.config.kind.toUpperCase()}</DecisionTag>
            ) : (
              <DecisionTag decision="inherit">{t('types.noSource')}</DecisionTag>
            )}
            <span className="mono" style={{ color: 'var(--licetum-muted)', flex: 1 }}>
              {s?.config.url ?? ''}
            </span>
            {!s && (
              <Button
                size="small"
                type="primary"
                onClick={() => setDrawer({ type: x, source: null })}
              >
                {t('sources.connect')}
              </Button>
            )}
          </Flex>
        );
      })}
      <Typography.Text type="secondary" style={{ fontSize: 13 }}>
        {t('wizard.sources.skip')}
      </Typography.Text>
      <Flex justify="space-between">
        <Button onClick={onBack}>{t('wizard.back')}</Button>
        <Button type="primary" onClick={onDone}>
          {t('wizard.next')}
        </Button>
      </Flex>
      <SourceDrawer state={drawer} serviceId={serviceId} onClose={() => setDrawer(null)} />
    </Flex>
  );
}

function ResourceCount({ type, hasSource }: { type: TypeResourceWithService; hasSource: boolean }) {
  const { t } = useTranslation();
  const res = useListResourcesOfType(type.id, undefined, {
    query: { enabled: hasSource, select: (r) => (r.status === 200 ? (r.data ?? []) : []) },
  });
  if (!hasSource)
    return <span style={{ color: 'var(--licetum-muted)' }}>{t('wizard.check.noSource')}</span>;
  if (res.isPending) return <span>…</span>;
  if (res.isError) return <DecisionTag decision="deny">{t('wizard.check.failed')}</DecisionTag>;
  const n = res.data?.length ?? 0;
  return n > 0 ? (
    <DecisionTag decision="allow">{t('wizard.check.count', { count: n })}</DecisionTag>
  ) : (
    <DecisionTag decision="except">{t('wizard.check.zero')}</DecisionTag>
  );
}

function StepCheck({
  slug,
  types,
  onBack,
  onDone,
}: {
  slug: string;
  types: TypeResourceWithService[];
  onBack: () => void;
  onDone: () => void;
}) {
  const { t } = useTranslation();
  const { sources } = useSourceKinds(types);
  return (
    <Flex vertical gap={16}>
      <Typography.Paragraph type="secondary" style={{ margin: 0 }}>
        {t('wizard.check.hint')}
      </Typography.Paragraph>
      {types.map((x) => (
        <Flex
          key={x.id}
          gap={12}
          align="center"
          style={{
            background: 'var(--licetum-surface)',
            border: '1px solid var(--licetum-border)',
            borderRadius: 8,
            padding: '10px 14px',
          }}
        >
          <b style={{ minWidth: 140 }}>{x.name}</b>
          <ResourceCount type={x} hasSource={!!sources.get(x.id)} />
        </Flex>
      ))}
      <Alert
        type="info"
        showIcon
        title={t('wizard.check.next.title')}
        description={
          <span>
            {t('wizard.check.next.body')}{' '}
            <Link href={`/services/${slug}/integration`}>{t('service.tab.integration')}</Link>
          </span>
        }
      />
      <Flex justify="space-between">
        <Button onClick={onBack}>{t('wizard.back')}</Button>
        <Button type="primary" onClick={onDone}>
          {t('wizard.done')}
        </Button>
      </Flex>
    </Flex>
  );
}
