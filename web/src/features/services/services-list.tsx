'use client';

import {
  Alert,
  Avatar,
  Button,
  Card,
  Dropdown,
  Flex,
  Input,
  Segmented,
  Select,
  Table,
  Tag,
  Typography,
} from 'antd';
import { MoreHorizontal, Plus, Search } from 'lucide-react';
import Link from 'next/link';
import { useRouter } from 'next/navigation';
import { useMemo } from 'react';
import { useTranslation } from 'react-i18next';

import { useGetPlatform } from '@/api/admin/platform/platform';
import {
  DecisionTag,
  EmptyBlock,
  ErrorBlock,
  IdChip,
  LoadingBlock,
  PageHeader,
} from '@/components/kit';
import { useAccess } from '@/lib/access';
import { useQueryState } from '@/lib/use-query-state';

import { useAllTypes, useRules, useServices } from '../data/queries';
import { factsOf, type ServiceFacts } from './service-status';

function StatusTag({ facts }: { facts: ServiceFacts }) {
  const { t } = useTranslation();
  return facts.status === 'ready' ? (
    <DecisionTag decision="allow">{t('services.status.ready')}</DecisionTag>
  ) : (
    <DecisionTag decision="except">{t('services.status.incomplete')}</DecisionTag>
  );
}

export function ServicesList() {
  const { t } = useTranslation();
  const router = useRouter();
  const access = useAccess();
  const services = useServices();
  const types = useAllTypes();
  const rules = useRules();
  const platform = useGetPlatform({
    query: { select: (r) => (r.status === 200 ? r.data : undefined) },
  });

  const [view, setView] = useQueryState('view', 'cards');
  const [search, setSearch] = useQueryState('q');
  const [status, setStatus] = useQueryState('status', 'all');

  const facts = useMemo(
    () =>
      (services.data ?? [])
        .map((s) => factsOf(s, types.data ?? [], rules.data ?? [], platform.data?.service_id))
        .sort(
          (a, b) =>
            Number(a.system) - Number(b.system) || a.service.name.localeCompare(b.service.name),
        ),
    [services.data, types.data, rules.data, platform.data],
  );

  const q = search.trim().toLowerCase();
  const shown = facts.filter(
    (f) =>
      (!q || `${f.service.name} ${f.service.slug}`.toLowerCase().includes(q)) &&
      (status === 'all' || f.status === status),
  );
  const onlySystem = facts.length > 0 && facts.every((f) => f.system);
  const canCreate = access.whole('service', 'write');

  return (
    <div className="page">
      <PageHeader
        title={t('nav.services')}
        extra={
          <>
            <Segmented
              aria-label={t('services.viewLabel')}
              value={view}
              onChange={(v) => setView(String(v))}
              options={[
                { label: t('services.view.cards'), value: 'cards' },
                { label: t('services.view.table'), value: 'table' },
              ]}
            />
            {canCreate && (
              <Button
                type="primary"
                icon={<Plus size={16} aria-hidden />}
                onClick={() => router.push('/services/new')}
              >
                {t('services.connect')}
              </Button>
            )}
          </>
        }
      />

      <Flex gap={12} wrap align="center" style={{ marginBottom: 16 }}>
        <Input
          allowClear
          prefix={<Search size={14} aria-hidden />}
          aria-label={t('services.search')}
          placeholder={t('services.search')}
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          style={{ width: 320, maxWidth: '100%' }}
        />
        <Select
          aria-label={t('services.filter.status')}
          value={status}
          onChange={setStatus}
          style={{ minWidth: 220 }}
          options={[
            { value: 'all', label: t('services.filter.all') },
            { value: 'ready', label: t('services.status.ready') },
            { value: 'incomplete', label: t('services.status.incomplete') },
          ]}
        />
        <span style={{ flex: 1 }} />
        <Typography.Text type="secondary" style={{ fontSize: 12 }}>
          {t('services.shown', { shown: shown.length, total: facts.length })}
        </Typography.Text>
      </Flex>

      {services.isPending ? (
        <LoadingBlock rows={5} />
      ) : services.isError ? (
        <ErrorBlock error={services.error} onRetry={() => void services.refetch()} />
      ) : (
        <>
          {onlySystem && (
            <EmptyBlock
              title={t('services.empty.title')}
              hint={t('services.empty.hint')}
              action={
                canCreate ? (
                  <Button type="primary" onClick={() => router.push('/services/new')}>
                    {t('services.connect')}
                  </Button>
                ) : undefined
              }
            />
          )}
          {shown.length === 0 && !onlySystem ? (
            <EmptyBlock
              title={t('services.noMatch')}
              action={
                <Button
                  onClick={() => {
                    setSearch(null);
                    setStatus(null);
                  }}
                >
                  {t('common.resetFilters')}
                </Button>
              }
            />
          ) : view === 'table' ? (
            <Table<ServiceFacts>
              rowKey={(f) => f.service.id}
              dataSource={shown}
              pagination={false}
              onRow={(f) => ({
                onClick: () => router.push(`/services/${f.service.slug}`),
                style: { cursor: 'pointer' },
              })}
              columns={[
                {
                  title: t('services.col.service'),
                  key: 'name',
                  render: (_, f) => (
                    <span>
                      <Link
                        href={`/services/${f.service.slug}`}
                        onClick={(e) => e.stopPropagation()}
                      >
                        {f.service.name}
                      </Link>{' '}
                      <span className="mono" style={{ color: 'var(--licetum-muted)' }}>
                        {f.service.slug}
                      </span>{' '}
                      {f.system && <Tag>{t('services.systemTag')}</Tag>}
                    </span>
                  ),
                },
                {
                  title: t('services.col.status'),
                  key: 'status',
                  width: 220,
                  render: (_, f) => <StatusTag facts={f} />,
                },
                {
                  title: t('services.col.types'),
                  key: 'types',
                  width: 100,
                  render: (_, f) => f.types.length,
                },
                {
                  title: t('services.col.rules'),
                  key: 'rules',
                  width: 110,
                  render: (_, f) => f.rules,
                },
              ]}
            />
          ) : (
            <Flex gap={16} wrap>
              {shown.map((f) => (
                <Card
                  key={f.service.id}
                  hoverable
                  size="small"
                  style={{
                    width: 340,
                    maxWidth: '100%',
                    borderStyle: f.system ? 'dashed' : 'solid',
                  }}
                  onClick={() => router.push(`/services/${f.service.slug}`)}
                >
                  <Flex gap={12} align="flex-start">
                    <Avatar
                      shape="square"
                      size={40}
                      src={f.service.icon || undefined}
                      style={{
                        background: 'var(--licetum-inherit-bg)',
                        color: 'var(--licetum-accent)',
                        fontWeight: 600,
                      }}
                    >
                      {f.service.name.slice(0, 1).toUpperCase()}
                    </Avatar>
                    <div style={{ flex: 1, minWidth: 0 }}>
                      <Flex align="center" gap={8} wrap>
                        <Link
                          href={`/services/${f.service.slug}`}
                          onClick={(e) => e.stopPropagation()}
                          style={{ fontWeight: 600, fontSize: 15 }}
                        >
                          {f.service.name}
                        </Link>
                        {f.system && <Tag style={{ margin: 0 }}>{t('services.systemTag')}</Tag>}
                      </Flex>
                      <span onClick={(e) => e.stopPropagation()}>
                        <IdChip value={f.service.slug} />
                      </span>
                    </div>
                    <span onClick={(e) => e.stopPropagation()}>
                      <Dropdown
                        trigger={['click']}
                        menu={{
                          items: [
                            { key: 'open', label: t('services.open') },
                            { key: 'integration', label: t('services.tab.integration') },
                          ],
                          onClick: ({ key }) =>
                            router.push(
                              key === 'open'
                                ? `/services/${f.service.slug}`
                                : `/services/${f.service.slug}/integration`,
                            ),
                        }}
                      >
                        <Button
                          type="text"
                          size="small"
                          aria-label={t('common.actions')}
                          icon={<MoreHorizontal size={16} aria-hidden />}
                        />
                      </Dropdown>
                    </span>
                  </Flex>
                  <Flex gap={8} align="center" wrap style={{ marginTop: 12 }}>
                    <StatusTag facts={f} />
                  </Flex>
                  <div style={{ color: 'var(--licetum-muted)', fontSize: 13, marginTop: 8 }}>
                    {t('services.counts', { types: f.types.length, rules: f.rules })}
                  </div>
                  {f.system && (
                    <div style={{ color: 'var(--licetum-muted)', fontSize: 12, marginTop: 8 }}>
                      {t('services.system.hint')}
                    </div>
                  )}
                </Card>
              ))}
            </Flex>
          )}
        </>
      )}
      {types.isError && (
        <Alert
          style={{ marginTop: 16 }}
          type="warning"
          showIcon
          title={t('services.countsFailed')}
        />
      )}
    </div>
  );
}
