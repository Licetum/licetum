'use client';

import {
  Button,
  Descriptions,
  Drawer,
  Flex,
  Input,
  Segmented,
  Switch,
  Table,
  Tag,
  Tooltip,
  Tree,
  Typography,
  message,
} from 'antd';
import { RefreshCw, Search } from 'lucide-react';
import Link from 'next/link';
import { useMemo } from 'react';
import { useTranslation } from 'react-i18next';

import type { Resource } from '@/api/admin/model';
import { useListResourcesOfType, useRefreshResourcesOfType } from '@/api/admin/resource/resource';
import { EmptyBlock, ErrorBlock, errorDetail, IdChip, LoadingBlock } from '@/components/kit';
import { useAccess } from '@/lib/access';
import { useQueryState } from '@/lib/use-query-state';

import { useAllTypes, useRefreshAll } from '../data/queries';
import { useGrant } from '../grant/grant-context';
import { useService } from './service-frame';
import { useSourceKinds } from './types-tab';
import { WhoHasAccess } from './who-has-access';

const metaPairs = (r: Resource): [string, string][] =>
  Object.entries(r.meta ?? {}).map(([k, v]) => [k, Array.isArray(v) ? v.join(', ') : String(v)]);

/** The resources of one type as the source reports them: a list, or the tree they form through their parents. */
export function ResourcesBrowser({ typeName }: { typeName: string }) {
  const { t } = useTranslation();
  const access = useAccess();
  const grant = useGrant();
  const { service } = useService();
  const types = useAllTypes();
  const type = (types.data ?? []).find((x) => x.service_id === service.id && x.name === typeName);
  const { sources, pending } = useSourceKinds(type ? [type] : []);
  const hasSource = !!(type && sources.get(type.id));

  const [inactive, setInactive] = useQueryState('inactive');
  const resources = useListResourcesOfType(
    type?.id ?? '',
    inactive ? { include_inactive: true } : undefined,
    {
      query: {
        enabled: !!type && hasSource,
        select: (r) => (r.status === 200 ? (r.data ?? []) : ([] as Resource[])),
      },
    },
  );

  const refreshAll = useRefreshAll();
  const refreshType = useRefreshResourcesOfType();
  const [api, holder] = message.useMessage();
  const refreshNow = async () => {
    if (!type) return;
    try {
      const res = await refreshType.mutateAsync({ typeResourceId: type.id });
      await refreshAll();
      const counts = res.data as { resources: number; inactive: number };
      void api.success(t('resources.refreshed', { count: counts.resources }));
    } catch (e) {
      void api.error(errorDetail(e) ?? t('resources.refreshFailed'));
    }
  };

  const [view, setView] = useQueryState('view', 'list');
  const [search, setSearch] = useQueryState('q');
  const [selectedId, setSelected] = useQueryState('id');

  const list = resources.data ?? [];
  const q = search.trim().toLowerCase();
  const shown = useMemo(
    () =>
      list.filter(
        (r) =>
          !q ||
          `${r.name} ${r.id} ${metaPairs(r)
            .map(([k, v]) => `${k}=${v}`)
            .join(' ')}`
            .toLowerCase()
            .includes(q),
      ),
    [list, q],
  );
  const byId = useMemo(() => new Map(list.map((r) => [r.id, r])), [list]);
  const orphans = list.filter((r) => (r.parents?.length ?? 0) === 0 && !r.parent_id).length;
  const unresolved = list.filter((r) => r.parents?.some((p) => p.resolved === false)).length;
  const switchedOff = list.filter((r) => r.active === false).length;
  const selected = selectedId ? byId.get(selectedId) : undefined;

  const treeData = useMemo(() => {
    const children = new Map<string, Resource[]>();
    const roots: Resource[] = [];
    for (const r of shown) {
      const parent = r.parent_id ?? r.parents?.[0]?.id;
      if (parent && byId.has(parent)) children.set(parent, [...(children.get(parent) ?? []), r]);
      else roots.push(r);
    }
    type Node = { key: string; title: string; children: Node[] };
    const build = (r: Resource): Node => ({
      key: r.id,
      title: `${r.name} (${r.id})`,
      children: (children.get(r.id) ?? []).map(build),
    });
    return roots.map(build);
  }, [shown, byId]);

  if (types.isPending || pending) return <LoadingBlock rows={4} />;
  if (!type)
    return (
      <EmptyBlock
        title={t('resources.noType')}
        action={<Link href={`/services/${service.slug}/types`}>{t('service.tab.types')}</Link>}
      />
    );
  if (!hasSource) {
    return (
      <EmptyBlock
        title={t('resources.noSource.title')}
        hint={t('resources.noSource.hint')}
        action={
          <Button type="primary" href={`/services/${service.slug}/sources`}>
            {t('sources.connect')}
          </Button>
        }
      />
    );
  }

  return (
    <>
      {holder}
      <Flex gap={12} wrap align="center" style={{ marginBottom: 12 }}>
        <Typography.Title level={4} style={{ margin: 0 }}>
          {t('resources.title', { type: type.name })}
        </Typography.Title>
        <span style={{ flex: 1 }} />
        <Button
          icon={<RefreshCw size={14} aria-hidden />}
          loading={refreshType.isPending}
          disabled={!access.whole('type_resource', 'write')}
          onClick={() => void refreshNow()}
        >
          {t('resources.refresh')}
        </Button>
        <Flex align="center" gap={6}>
          <Switch
            size="small"
            checked={!!inactive}
            onChange={(on) => setInactive(on ? '1' : null)}
            aria-label={t('resources.showInactive')}
          />
          <span style={{ fontSize: 13 }}>{t('resources.showInactive')}</span>
        </Flex>
        <Segmented
          aria-label={t('services.viewLabel')}
          value={view}
          onChange={(v) => setView(String(v))}
          options={[
            { label: t('resources.view.list'), value: 'list' },
            { label: t('resources.view.tree'), value: 'tree' },
          ]}
        />
        <Input
          allowClear
          prefix={<Search size={14} aria-hidden />}
          aria-label={t('resources.search')}
          placeholder={t('resources.search')}
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          style={{ width: 300 }}
        />
      </Flex>
      <Typography.Text type="secondary" style={{ display: 'block', marginBottom: 8, fontSize: 12 }}>
        {t('resources.count', { shown: shown.length, total: list.length })}
        {orphans > 0 ? ` · ${t('resources.orphans', { count: orphans })}` : ''}
        {unresolved > 0 ? ` · ${t('resources.unresolved', { count: unresolved })}` : ''}
        {switchedOff > 0 ? ` · ${t('resources.switchedOff', { count: switchedOff })}` : ''}
      </Typography.Text>

      {resources.isPending ? (
        <LoadingBlock rows={5} />
      ) : resources.isError ? (
        <ErrorBlock error={resources.error} onRetry={() => void resources.refetch()} />
      ) : list.length === 0 ? (
        <EmptyBlock title={t('resources.zero.title')} hint={t('resources.zero.hint')} />
      ) : view === 'tree' ? (
        <div
          style={{
            background: 'var(--licetum-surface)',
            border: '1px solid var(--licetum-border)',
            borderRadius: 8,
            padding: 16,
          }}
        >
          <Tree
            treeData={treeData}
            defaultExpandAll
            onSelect={(keys) => keys[0] && setSelected(String(keys[0]))}
            selectedKeys={selectedId ? [selectedId] : []}
          />
        </div>
      ) : (
        <Table<Resource>
          rowKey="id"
          size="middle"
          dataSource={shown}
          pagination={shown.length > 100 ? { pageSize: 100, showSizeChanger: false } : false}
          onRow={(r) => ({ onClick: () => setSelected(r.id), style: { cursor: 'pointer' } })}
          locale={{ emptyText: <EmptyBlock title={t('people.noMatch')} /> }}
          columns={[
            {
              title: t('resources.col.name'),
              key: 'name',
              render: (_, r) => (
                <span style={{ opacity: r.active === false ? 0.55 : 1 }}>
                  <b>{r.name}</b>
                  {r.active === false && (
                    <Tag style={{ marginLeft: 8 }}>{t('resources.inactive')}</Tag>
                  )}
                </span>
              ),
            },
            {
              title: 'id',
              key: 'id',
              width: 160,
              render: (_, r) => <span className="mono">{r.id}</span>,
            },
            {
              title: t('resources.col.parent'),
              key: 'parent',
              render: (_, r) => {
                const ids = r.parents?.length
                  ? r.parents.map((p) => p.id)
                  : r.parent_id
                    ? [r.parent_id]
                    : [];
                if (!ids.length) return <Tag color="warning">{t('resources.orphan')}</Tag>;
                return (
                  <>
                    {ids.map((id) => byId.get(id)?.name ?? id).join(', ')}
                    {r.parents?.some((p) => p.resolved === false) && (
                      <Tooltip title={t('resources.unresolvedHint')}>
                        <Tag color="warning" style={{ marginLeft: 8 }}>
                          {t('resources.unresolvedTag')}
                        </Tag>
                      </Tooltip>
                    )}
                  </>
                );
              },
            },
            {
              title: t('resources.col.attrs'),
              key: 'meta',
              render: (_, r) =>
                metaPairs(r).map(([k, v]) => (
                  <Tag key={k} className="mono">
                    {k} = {v}
                  </Tag>
                )),
            },
          ]}
        />
      )}

      <Drawer
        open={!!selected}
        onClose={() => setSelected(null)}
        size={480}
        destroyOnHidden
        title={selected?.name}
      >
        {selected && (
          <Flex vertical gap={16}>
            <Descriptions
              column={1}
              size="small"
              styles={{ label: { width: 110, color: 'var(--licetum-muted)' } }}
            >
              <Descriptions.Item label="id">
                <IdChip value={selected.id} />
              </Descriptions.Item>
              <Descriptions.Item label={t('resources.col.parent')}>
                {(selected.parents?.length
                  ? selected.parents.map((p) => p.id)
                  : selected.parent_id
                    ? [selected.parent_id]
                    : []
                ).map((id) => (
                  <Tag key={id} style={{ cursor: 'pointer' }} onClick={() => setSelected(id)}>
                    {byId.get(id)?.name ?? id}
                  </Tag>
                ))}
                {!selected.parent_id && !selected.parents?.length && <span>—</span>}
              </Descriptions.Item>
              <Descriptions.Item label={t('resources.children')}>
                {
                  list.filter(
                    (r) =>
                      r.parent_id === selected.id || r.parents?.some((p) => p.id === selected.id),
                  ).length
                }
              </Descriptions.Item>
              {metaPairs(selected).map(([k, v]) => (
                <Descriptions.Item key={k} label={k}>
                  <span className="mono">{v}</span>
                </Descriptions.Item>
              ))}
            </Descriptions>
            <Flex gap={8} wrap>
              {access.whole('assignment_rule', 'write') && (
                <Button
                  type="primary"
                  onClick={() =>
                    grant.open({ serviceId: service.id, typeId: type.id, resourceId: selected.id })
                  }
                >
                  {t('resources.grant')}
                </Button>
              )}
              <Button
                href={`/check?mode=one&service=${service.slug}&type=${encodeURIComponent(type.name)}&resource=${encodeURIComponent(selected.id)}`}
              >
                {t('resources.check')}
              </Button>
            </Flex>
            <Typography.Title level={5} style={{ margin: 0 }}>
              {t('whohas.title')}
            </Typography.Title>
            <WhoHasAccess serviceId={service.id} typeId={type.id} resourceId={selected.id} />
          </Flex>
        )}
      </Drawer>
    </>
  );
}
