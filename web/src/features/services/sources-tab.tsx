'use client';

import { Alert, Button, Drawer, Flex, Input, Modal, Segmented, Table, Tag, Typography } from 'antd';
import { Plus, X } from 'lucide-react';
import Link from 'next/link';
import { useState } from 'react';
import { useTranslation } from 'react-i18next';

import type {
  SourceTestResult,
  Synchronizer,
  SynchronizerConfig,
  TypeResourceWithService,
} from '@/api/admin/model';
import {
  useCreateSynchronizer,
  useDeleteSynchronizer,
  useTestSynchronizer,
  useUpdateSynchronizer,
} from '@/api/admin/resource/resource';
import { DecisionTag, EmptyBlock, errorDetail, ErrorBlock, LoadingBlock } from '@/components/kit';
import { useAccess } from '@/lib/access';

import { useAllTypes, useRefreshAll } from '../data/queries';
import { useService } from './service-frame';
import { useSourceKinds } from './types-tab';

type Header = { id: number; name: string; value: string };
let seq = 0;

export function SourcesTab() {
  const { t } = useTranslation();
  const access = useAccess();
  const { service } = useService();
  const all = useAllTypes();
  const refresh = useRefreshAll();
  const remove = useDeleteSynchronizer();
  const own = (all.data ?? []).filter((x) => x.service_id === service.id);
  const { sources, pending } = useSourceKinds(own);
  const [drawer, setDrawer] = useState<{
    type: TypeResourceWithService;
    source: Synchronizer | null;
  } | null>(null);
  const canWrite = access.any('type_resource', 'write');

  const askDelete = (type: TypeResourceWithService, source: Synchronizer) =>
    Modal.confirm({
      title: t('sources.delete.title', { type: type.name }),
      content: t('sources.delete.body'),
      okText: t('common.delete'),
      okButtonProps: { danger: true },
      cancelText: t('common.cancel'),
      onOk: async () => {
        try {
          await remove.mutateAsync({
            serviceId: service.id,
            typeResourceId: type.id,
            synchronizerId: source.id,
          });
          await refresh();
        } catch (e) {
          Modal.error({ title: t('common.saveFailed'), content: errorDetail(e) });
          throw e;
        }
      },
    });

  if (all.isPending) return <LoadingBlock rows={3} />;
  if (all.isError) return <ErrorBlock error={all.error} onRetry={() => void all.refetch()} />;
  if (own.length === 0) {
    return (
      <EmptyBlock
        title={t('sources.noTypes.title')}
        hint={t('sources.noTypes.hint')}
        action={
          <Button type="primary" href={`/services/${service.slug}/types`}>
            {t('service.incomplete.cta')}
          </Button>
        }
      />
    );
  }

  return (
    <>
      <Typography.Paragraph type="secondary">{t('sources.hint')}</Typography.Paragraph>
      <Table<TypeResourceWithService>
        rowKey="id"
        size="middle"
        pagination={false}
        loading={pending}
        dataSource={own}
        columns={[
          {
            title: t('types.col.name'),
            key: 'name',
            width: 200,
            render: (_, x) => <b>{x.name}</b>,
          },
          {
            title: t('sources.col.source'),
            key: 'source',
            render: (_, x) => {
              const s = sources.get(x.id);
              if (!s)
                return <span style={{ color: 'var(--licetum-muted)' }}>{t('sources.none')}</span>;
              return (
                <Flex vertical gap={2}>
                  <span>
                    <DecisionTag decision="allow">{s.config.kind.toUpperCase()}</DecisionTag>{' '}
                    <span className="mono" style={{ color: 'var(--licetum-muted)' }}>
                      {s.config.kind === 'http' ? s.config.url : (s.config.dsn ?? '')}
                    </span>
                  </span>
                </Flex>
              );
            },
          },
          {
            title: '',
            key: 'actions',
            width: 330,
            render: (_, x) => {
              const s = sources.get(x.id) ?? null;
              return (
                <Flex gap={8} wrap>
                  {s && (
                    <Button
                      size="small"
                      href={`/services/${service.slug}/types/${encodeURIComponent(x.name)}/resources`}
                    >
                      {t('types.resources')}
                    </Button>
                  )}
                  {canWrite && (
                    <Button
                      size="small"
                      type={s ? 'default' : 'primary'}
                      onClick={() => setDrawer({ type: x, source: s })}
                    >
                      {s ? t('common.edit') : t('sources.connect')}
                    </Button>
                  )}
                  {canWrite && s && (
                    <Button size="small" danger onClick={() => askDelete(x, s)}>
                      {t('common.delete')}
                    </Button>
                  )}
                </Flex>
              );
            },
          },
        ]}
      />
      <Typography.Paragraph type="secondary" style={{ fontSize: 12, marginTop: 12 }}>
        {t('sources.rawNote')}{' '}
        <Link href={`/services/${service.slug}/integration`}>{t('service.tab.integration')}</Link>
      </Typography.Paragraph>
      <SourceDrawer state={drawer} serviceId={service.id} onClose={() => setDrawer(null)} />
    </>
  );
}

export function SourceDrawer(props: {
  state: { type: TypeResourceWithService; source: Synchronizer | null } | null;
  serviceId: string;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const create = useCreateSynchronizer();
  const update = useUpdateSynchronizer();
  const refresh = useRefreshAll();

  const [kind, setKind] = useState<'http' | 'sql'>('http');
  const [url, setUrl] = useState('');
  const [headers, setHeaders] = useState<Header[]>([]);
  const [dsn, setDsn] = useState('');
  const [query, setQuery] = useState('');
  const [listQuery, setListQuery] = useState('');
  const [parentsQuery, setParentsQuery] = useState('');
  const [parentsListQuery, setParentsListQuery] = useState('');
  const [probe, setProbe] = useState('');
  const [error, setError] = useState<unknown>(null);
  const [loaded, setLoaded] = useState<string | null>(null);
  const [report, setReport] = useState<SourceTestResult | null>(null);
  const [testError, setTestError] = useState<unknown>(null);

  const key = props.state ? `${props.state.type.id}:${props.state.source?.id ?? 'new'}` : null;
  if (key !== loaded) {
    setLoaded(key);
    if (props.state) {
      const c = props.state.source?.config;
      setKind((c?.kind as 'http' | 'sql') ?? 'http');
      setUrl(c?.url ?? '');
      setHeaders(
        Object.entries(c?.headers ?? {}).map(([name, value]) => ({
          id: ++seq,
          name,
          value: String(value),
        })),
      );
      setDsn(c?.dsn ?? '');
      setQuery(c?.query ?? '');
      setListQuery(c?.list_query ?? '');
      setParentsQuery(c?.parents_query ?? '');
      setParentsListQuery(c?.parents_list_query ?? '');
      setProbe('');
      setError(null);
      setReport(null);
      setTestError(null);
    }
  }

  const configReady =
    kind === 'http'
      ? url.trim() !== ''
      : dsn.trim() !== '' && query.trim() !== '' && listQuery.trim() !== '';
  const ready = probe.trim() !== '' && configReady;

  const testSource = useTestSynchronizer();

  const buildConfig = (): SynchronizerConfig => {
    const config: SynchronizerConfig =
      kind === 'http'
        ? {
            kind: 'http',
            url: url.trim(),
            headers: Object.fromEntries(
              headers.filter((h) => h.name.trim()).map((h) => [h.name.trim(), h.value]),
            ),
          }
        : {
            kind: 'sql',
            dsn: dsn.trim(),
            query: query.trim(),
            list_query: listQuery.trim(),
            ...(parentsQuery.trim() ? { parents_query: parentsQuery.trim() } : {}),
            ...(parentsListQuery.trim() ? { parents_list_query: parentsListQuery.trim() } : {}),
          };
    return config;
  };

  /** Asks the source with the configuration as it stands, without saving it. */
  const runTest = async () => {
    if (!props.state) return;
    setTestError(null);
    setReport(null);
    try {
      const res = await testSource.mutateAsync({
        serviceId: props.serviceId,
        typeResourceId: props.state.type.id,
        data: { config: buildConfig(), synchronizer_id: props.state.source?.id ?? null },
      });
      setReport(res.data as SourceTestResult);
    } catch (e) {
      setTestError(e);
    }
  };

  const submit = async () => {
    if (!props.state) return;
    setError(null);
    const data = { config: buildConfig(), probe_resource_id: probe.trim() };
    try {
      if (props.state.source) {
        await update.mutateAsync({
          serviceId: props.serviceId,
          typeResourceId: props.state.type.id,
          synchronizerId: props.state.source.id,
          data,
        });
      } else {
        await create.mutateAsync({
          serviceId: props.serviceId,
          typeResourceId: props.state.type.id,
          data,
        });
      }
      await refresh();
      props.onClose();
    } catch (e) {
      setError(e);
    }
  };

  const field = (label: string, node: React.ReactNode, help?: string) => (
    <div>
      <div style={{ fontWeight: 600, marginBottom: 4 }}>{label}</div>
      {node}
      {help && (
        <Typography.Text type="secondary" style={{ fontSize: 12 }}>
          {help}
        </Typography.Text>
      )}
    </div>
  );

  return (
    <Drawer
      open={!!props.state}
      onClose={props.onClose}
      size={640}
      destroyOnHidden
      title={props.state ? t('sources.drawer.title', { type: props.state.type.name }) : ''}
      footer={
        <Flex justify="flex-end" gap={8}>
          <Button onClick={props.onClose}>{t('common.cancel')}</Button>
          <Button
            type="primary"
            disabled={!ready}
            loading={create.isPending || update.isPending}
            onClick={() => void submit()}
          >
            {t('common.save')}
          </Button>
        </Flex>
      }
    >
      <Flex vertical gap={16}>
        {error != null && (
          <Alert type="error" showIcon title={errorDetail(error) ?? t('common.saveFailed')} />
        )}
        <Segmented
          aria-label={t('sources.kind')}
          value={kind}
          onChange={(v) => setKind(v as 'http' | 'sql')}
          options={[
            { label: 'HTTP', value: 'http' },
            { label: 'PostgreSQL', value: 'sql' },
          ]}
        />

        {kind === 'http' ? (
          <>
            {field(
              t('sources.url'),
              <Input
                value={url}
                onChange={(e) => setUrl(e.target.value)}
                placeholder="https://docs.internal/api/documents"
                className="mono"
              />,
              t('sources.urlHelp'),
            )}
            {field(
              t('sources.headers'),
              <Flex vertical gap={8}>
                {headers.map((h) => (
                  <Flex key={h.id} gap={8}>
                    <Input
                      aria-label={t('sources.headerName')}
                      className="mono"
                      style={{ width: 180 }}
                      value={h.name}
                      placeholder="Authorization"
                      onChange={(e) =>
                        setHeaders(
                          headers.map((x) => (x.id === h.id ? { ...x, name: e.target.value } : x)),
                        )
                      }
                    />
                    <Input.Password
                      aria-label={t('sources.headerValue')}
                      style={{ flex: 1 }}
                      value={h.value}
                      autoComplete="off"
                      onChange={(e) =>
                        setHeaders(
                          headers.map((x) => (x.id === h.id ? { ...x, value: e.target.value } : x)),
                        )
                      }
                    />
                    <Button
                      type="text"
                      aria-label={t('cond.remove')}
                      icon={<X size={14} aria-hidden />}
                      onClick={() => setHeaders(headers.filter((x) => x.id !== h.id))}
                    />
                  </Flex>
                ))}
                <div>
                  <Button
                    size="small"
                    icon={<Plus size={14} aria-hidden />}
                    onClick={() => setHeaders([...headers, { id: ++seq, name: '', value: '' }])}
                  >
                    {t('sources.addHeader')}
                  </Button>
                </div>
              </Flex>,
              t('sources.headersHelp'),
            )}
          </>
        ) : (
          <>
            {field(
              t('sources.dsn'),
              <Input.Password
                value={dsn}
                onChange={(e) => setDsn(e.target.value)}
                placeholder="postgresql://reader:password@host:5432/db"
                autoComplete="off"
                className="mono"
              />,
              t('sources.dsnHelp'),
            )}
            {field(
              t('sources.listQuery'),
              <Input.TextArea
                rows={3}
                value={listQuery}
                onChange={(e) => setListQuery(e.target.value)}
                className="mono"
                placeholder="SELECT id, name, parent_id FROM documents"
              />,
              t('sources.listQueryHelp'),
            )}
            {field(
              t('sources.query'),
              <Input.TextArea
                rows={3}
                value={query}
                onChange={(e) => setQuery(e.target.value)}
                className="mono"
                placeholder="SELECT id, name, parent_id FROM documents WHERE id = :id"
              />,
              t('sources.queryHelp'),
            )}
            {field(
              t('sources.parentsList'),
              <Input.TextArea
                rows={2}
                value={parentsListQuery}
                onChange={(e) => setParentsListQuery(e.target.value)}
                className="mono"
                placeholder="SELECT document_id AS id, 'folder' AS parent_type, folder_id AS parent_id FROM links"
              />,
              t('sources.parentsHelp'),
            )}
            {field(
              t('sources.parentsOne'),
              <Input.TextArea
                rows={2}
                value={parentsQuery}
                onChange={(e) => setParentsQuery(e.target.value)}
                className="mono"
              />,
            )}
          </>
        )}

        {field(
          t('sources.probe'),
          <Input
            value={probe}
            onChange={(e) => setProbe(e.target.value)}
            placeholder="d42"
            className="mono"
          />,
          t('sources.probeHelp'),
        )}
        <Alert type="info" showIcon title={t('sources.attrsNote')} />

        <div
          style={{
            border: '1px solid var(--licetum-border)',
            borderRadius: 8,
            padding: 12,
          }}
        >
          <Flex align="center" gap={12} wrap>
            <Button
              disabled={!configReady}
              loading={testSource.isPending}
              onClick={() => void runTest()}
            >
              {t('sources.test.run')}
            </Button>
            <Typography.Text type="secondary" style={{ fontSize: 12 }}>
              {t('sources.test.hint')}
            </Typography.Text>
          </Flex>
          {testError != null && (
            <Alert
              style={{ marginTop: 12 }}
              type="error"
              showIcon
              title={errorDetail(testError) ?? t('sources.test.failed')}
            />
          )}
          {report && <SourceTestReport report={report} />}
        </div>
      </Flex>
    </Drawer>
  );
}

/** What a source gave when it was asked: how many, the first few, what looks wrong, and the attributes. */
function SourceTestReport({ report }: { report: SourceTestResult }) {
  const { t } = useTranslation();
  const problems = report.duplicate_count + report.orphan_count + report.unresolvable_links;
  return (
    <Flex vertical gap={10} style={{ marginTop: 12 }} data-testid="source-test">
      <Alert
        type={report.total === 0 ? 'warning' : problems > 0 ? 'warning' : 'success'}
        showIcon
        title={t('sources.test.total', { count: report.total, ms: report.duration_ms })}
        description={report.total === 0 ? t('sources.test.empty') : undefined}
      />
      {report.sample.length > 0 && (
        <Table<SourceTestResult['sample'][number]>
          size="small"
          rowKey="id"
          pagination={false}
          dataSource={report.sample}
          columns={[
            {
              title: 'id',
              dataIndex: 'id',
              render: (v: string) => <span className="mono">{v}</span>,
            },
            { title: t('resources.col.name'), dataIndex: 'name' },
            {
              title: t('resources.col.parent'),
              key: 'parent',
              render: (_, r) =>
                (r.parents?.length
                  ? r.parents.map((p) => p.id)
                  : r.parent_id
                    ? [r.parent_id]
                    : []
                ).join(', ') || '—',
            },
          ]}
        />
      )}
      {report.duplicate_count > 0 && (
        <Alert
          type="warning"
          showIcon
          title={t('sources.test.duplicates', { count: report.duplicate_count })}
          description={<span className="mono">{report.duplicates.join(', ')}</span>}
        />
      )}
      {report.orphan_count > 0 && (
        <Alert
          type="warning"
          showIcon
          title={t('sources.test.orphans', { count: report.orphan_count })}
          description={
            <Flex vertical gap={2}>
              <span>{t('sources.test.orphansHint')}</span>
              {report.orphans.slice(0, 5).map((o) => (
                <span key={`${o.id}/${o.parent_id}`} className="mono">
                  {o.id} → {o.parent_type}/{o.parent_id}
                </span>
              ))}
            </Flex>
          }
        />
      )}
      {report.unresolvable_links > 0 && (
        <Alert
          type="warning"
          showIcon
          title={t('sources.test.unresolvable', { count: report.unresolvable_links })}
        />
      )}
      <div>
        <Typography.Text strong>{t('sources.test.attrs')}</Typography.Text>
        <div style={{ marginTop: 4 }}>
          {report.attributes.length === 0 ? (
            <Typography.Text type="secondary">{t('sources.test.noAttrs')}</Typography.Text>
          ) : (
            report.attributes.map((a) => (
              <Tag key={a.name} className="mono">
                {a.name} · {a.count}
              </Tag>
            ))
          )}
        </div>
      </div>
    </Flex>
  );
}
