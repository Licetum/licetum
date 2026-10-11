'use client';

import { useQueries } from '@tanstack/react-query';
import { Alert, Button, Drawer, Flex, Input, Modal, Select, Table, Tag, Typography } from 'antd';
import { Plus, X } from 'lucide-react';
import Link from 'next/link';
import { useState } from 'react';
import { useTranslation } from 'react-i18next';

import type {
  AccessGroup,
  Synchronizer,
  TypeParent,
  TypeResourceWithService,
} from '@/api/admin/model';
import { listSynchronizers } from '@/api/admin/resource/resource';
import {
  useCreateAccessGroup,
  useCreateTypeResource,
  useDeleteAccessGroup,
  useDeleteTypeResource,
  useListAccessGroups,
  useUpdateAccessGroup,
  useUpdateTypeResource,
} from '@/api/admin/type-resource/type-resource';
import {
  DecisionTag,
  EmptyBlock,
  errorDetail,
  errorExtra,
  ErrorBlock,
  LoadingBlock,
} from '@/components/kit';
import { useAccess } from '@/lib/access';

import { useAllTypes, useRefreshAll } from '../data/queries';
import { useService } from './service-frame';

type Row = { id: string; implied: string[] };

const toRows = (map: Record<string, string[] | null | undefined> | undefined): Row[] =>
  Object.entries(map ?? {}).map(([id, implied]) => ({ id, implied: implied ?? [] }));

/** The source of each of the given types (or null): one request per type, there is no such flag in the type yet. */
export function useSourceKinds(types: { id: string; service_id: string }[]) {
  const results = useQueries({
    queries: types.map((x) => ({
      queryKey: ['sources-of', x.id],
      queryFn: async () => {
        const res = await listSynchronizers(x.service_id, x.id);
        return res.status === 200 ? (res.data ?? []) : [];
      },
    })),
  });
  const map = new Map<string, Synchronizer | null>();
  types.forEach((x, i) => map.set(x.id, results[i]?.data?.[0] ?? null));
  return { sources: map, pending: results.some((r) => r.isPending) };
}

export function TypesTab() {
  const { t } = useTranslation();
  const access = useAccess();
  const { service } = useService();
  const all = useAllTypes();
  const refresh = useRefreshAll();
  const remove = useDeleteTypeResource();

  const [drawer, setDrawer] = useState<{ open: boolean; type?: TypeResourceWithService }>({
    open: false,
  });

  const own = (all.data ?? []).filter((x) => x.service_id === service.id);
  const byId = new Map((all.data ?? []).map((x) => [x.id, x]));
  const { sources } = useSourceKinds(own);
  const canWrite = access.any('type_resource', 'write');

  const askDelete = (type: TypeResourceWithService) =>
    Modal.confirm({
      title: t('types.delete.title', { name: type.name }),
      content: t('types.delete.body'),
      okText: t('common.delete'),
      okButtonProps: { danger: true },
      cancelText: t('common.cancel'),
      onOk: async () => {
        try {
          await remove.mutateAsync({ serviceId: service.id, typeResourceId: type.id });
          await refresh();
        } catch (e) {
          const extra = errorExtra(e);
          Modal.error({
            title: t('types.delete.blocked'),
            content:
              extra?.type === 'blocked_by'
                ? t('types.delete.blockedBy', {
                    what: (extra.blocked_by ?? [])
                      .map((k) => t(`types.blocker.${k}`, { defaultValue: k }))
                      .join(', '),
                  })
                : errorDetail(e),
          });
          throw e;
        }
      },
    });

  if (all.isPending) return <LoadingBlock rows={4} />;
  if (all.isError) return <ErrorBlock error={all.error} onRetry={() => void all.refetch()} />;

  return (
    <>
      <Flex justify="space-between" align="center" wrap gap={8} style={{ marginBottom: 12 }}>
        <Typography.Text type="secondary">{t('types.hint')}</Typography.Text>
        {canWrite && (
          <Button
            type="primary"
            icon={<Plus size={16} aria-hidden />}
            onClick={() => setDrawer({ open: true })}
          >
            {t('types.add')}
          </Button>
        )}
      </Flex>

      {own.length === 0 ? (
        <EmptyBlock
          title={t('types.empty.title')}
          hint={t('types.empty.hint')}
          action={
            canWrite ? (
              <Button type="primary" onClick={() => setDrawer({ open: true })}>
                {t('types.add')}
              </Button>
            ) : undefined
          }
        />
      ) : (
        <Table<TypeResourceWithService>
          rowKey="id"
          size="middle"
          pagination={false}
          dataSource={own}
          expandable={{
            expandedRowRender: (type) => <LevelSets type={type} canWrite={canWrite} />,
          }}
          columns={[
            { title: t('types.col.name'), key: 'name', render: (_, x) => <b>{x.name}</b> },
            {
              title: t('types.col.parents'),
              key: 'parents',
              render: (_, x) =>
                x.parents.length === 0
                  ? '—'
                  : x.parents.map((p) => {
                      const parent = byId.get(p.type_resource_id);
                      const foreign = parent && parent.service_id !== service.id;
                      return (
                        <Tag key={p.type_resource_id} style={{ margin: '0 4px 4px 0' }}>
                          {foreign ? `${parent.service_name} / ` : ''}
                          {parent?.name ?? '…'}
                          {p.inherit?.length ? ` (${p.inherit.join(', ')})` : ''}
                        </Tag>
                      );
                    }),
            },
            {
              title: t('types.col.levels'),
              key: 'levels',
              render: (_, x) => {
                const rows = toRows(x.access_implies);
                return rows.length === 0
                  ? '—'
                  : rows.map((r) => (
                      <div key={r.id} className="mono">
                        {r.id} ⊃ {r.implied.join(', ')}
                      </div>
                    ));
              },
            },
            {
              title: t('types.col.source'),
              key: 'source',
              width: 190,
              render: (_, x) => {
                const s = sources.get(x.id);
                return s ? (
                  <Link href={`/services/${service.slug}/sources`}>
                    <DecisionTag decision="allow">{s.config.kind.toUpperCase()}</DecisionTag>
                  </Link>
                ) : (
                  <Link href={`/services/${service.slug}/sources`}>
                    <DecisionTag decision="inherit">{t('types.noSource')}</DecisionTag>
                  </Link>
                );
              },
            },
            {
              title: '',
              key: 'actions',
              width: 280,
              render: (_, x) => (
                <Flex gap={8} wrap>
                  {sources.get(x.id) && (
                    <Button
                      size="small"
                      href={`/services/${service.slug}/types/${encodeURIComponent(x.name)}/resources`}
                    >
                      {t('types.resources')}
                    </Button>
                  )}
                  {canWrite && (
                    <Button size="small" onClick={() => setDrawer({ open: true, type: x })}>
                      {t('common.edit')}
                    </Button>
                  )}
                  {canWrite && (
                    <Button size="small" danger onClick={() => askDelete(x)}>
                      {t('common.delete')}
                    </Button>
                  )}
                </Flex>
              ),
            },
          ]}
        />
      )}
      <Alert type="info" showIcon style={{ marginTop: 16 }} title={t('types.noSourceNote')} />
      <TypeDrawer
        open={drawer.open}
        type={drawer.type}
        serviceId={service.id}
        onClose={() => setDrawer({ open: false })}
      />
    </>
  );
}

/** The named sets of levels of one type: "@editor = read, write". */
function LevelSets({ type, canWrite }: { type: TypeResourceWithService; canWrite: boolean }) {
  const { t } = useTranslation();
  const refresh = useRefreshAll();
  const groups = useListAccessGroups(type.service_id, type.id, {
    query: { select: (r) => (r.status === 200 ? (r.data ?? []) : ([] as AccessGroup[])) },
  });
  const create = useCreateAccessGroup();
  const update = useUpdateAccessGroup();
  const remove = useDeleteAccessGroup();
  const [edit, setEdit] = useState<{ id?: string; name: string; levels: string[] } | null>(null);
  const [error, setError] = useState<unknown>(null);

  const save = async () => {
    if (!edit) return;
    setError(null);
    try {
      const data = { name: edit.name.trim(), levels: edit.levels };
      if (edit.id)
        await update.mutateAsync({
          serviceId: type.service_id,
          typeResourceId: type.id,
          accessGroupId: edit.id,
          data,
        });
      else await create.mutateAsync({ serviceId: type.service_id, typeResourceId: type.id, data });
      await refresh();
      setEdit(null);
    } catch (e) {
      setError(e);
    }
  };

  return (
    <Flex vertical gap={8}>
      <Typography.Text strong>{t('accessGroups.title')}</Typography.Text>
      <Typography.Text type="secondary" style={{ fontSize: 12 }}>
        {t('accessGroups.hint')}
      </Typography.Text>
      {(groups.data ?? []).length === 0 && !edit && (
        <Typography.Text type="secondary">{t('accessGroups.none')}</Typography.Text>
      )}
      {(groups.data ?? []).map((g) => (
        <Flex key={g.id} gap={8} align="center">
          <span className="mono">
            @{g.name} = {(g.levels ?? []).join(', ')}
          </span>
          {canWrite && (
            <>
              <Button
                size="small"
                onClick={() => setEdit({ id: g.id, name: g.name, levels: g.levels ?? [] })}
              >
                {t('common.edit')}
              </Button>
              <Button
                size="small"
                danger
                onClick={() =>
                  Modal.confirm({
                    title: t('accessGroups.delete', { name: g.name }),
                    okText: t('common.delete'),
                    okButtonProps: { danger: true },
                    cancelText: t('common.cancel'),
                    onOk: async () => {
                      try {
                        await remove.mutateAsync({
                          serviceId: type.service_id,
                          typeResourceId: type.id,
                          accessGroupId: g.id,
                        });
                        await refresh();
                      } catch (e) {
                        Modal.error({ title: t('accessGroups.blocked'), content: errorDetail(e) });
                        throw e;
                      }
                    },
                  })
                }
              >
                {t('common.delete')}
              </Button>
            </>
          )}
        </Flex>
      ))}
      {edit && (
        <Flex vertical gap={8} style={{ maxWidth: 520 }}>
          {error != null && (
            <Alert type="error" showIcon title={errorDetail(error) ?? t('common.saveFailed')} />
          )}
          <Input
            aria-label={t('accessGroups.name')}
            placeholder={t('accessGroups.name')}
            value={edit.name}
            onChange={(e) => setEdit({ ...edit, name: e.target.value })}
            className="mono"
          />
          <Select
            mode="tags"
            aria-label={t('accessGroups.levels')}
            placeholder={t('accessGroups.levels')}
            value={edit.levels}
            onChange={(levels) => setEdit({ ...edit, levels })}
            tokenSeparators={[',', ' ']}
            options={['read', 'write'].map((l) => ({ value: l }))}
          />
          <Flex gap={8}>
            <Button
              type="primary"
              disabled={!edit.name.trim() || edit.levels.length === 0}
              loading={create.isPending || update.isPending}
              onClick={() => void save()}
            >
              {t('common.save')}
            </Button>
            <Button onClick={() => setEdit(null)}>{t('common.cancel')}</Button>
          </Flex>
        </Flex>
      )}
      {canWrite && !edit && (
        <div>
          <Button
            size="small"
            icon={<Plus size={14} aria-hidden />}
            onClick={() => setEdit({ name: '', levels: [] })}
          >
            {t('accessGroups.add')}
          </Button>
        </div>
      )}
    </Flex>
  );
}

function TypeDrawer(props: {
  open: boolean;
  type?: TypeResourceWithService;
  serviceId: string;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const all = useAllTypes();
  const create = useCreateTypeResource();
  const update = useUpdateTypeResource();
  const refresh = useRefreshAll();

  const [name, setName] = useState('');
  const [parents, setParents] = useState<{ id: string; inherit: string[] }[]>([]);
  const [implies, setImplies] = useState<Row[]>([]);
  const [error, setError] = useState<unknown>(null);
  const [loaded, setLoaded] = useState<string | null>(null);

  // The form is filled in when the drawer opens, not on every render.
  const key = props.open ? (props.type?.id ?? 'new') : null;
  if (key !== loaded) {
    setLoaded(key);
    if (key) {
      setName(props.type?.name ?? '');
      setParents(
        (props.type?.parents ?? []).map((p) => ({
          id: p.type_resource_id,
          inherit: p.inherit ?? [],
        })),
      );
      setImplies(toRows(props.type?.access_implies));
      setError(null);
    }
  }

  const candidates = (all.data ?? []).filter((x) => x.id !== props.type?.id);

  const submit = async () => {
    setError(null);
    const body = {
      name: name.trim(),
      parents: parents.map<TypeParent>((p) => ({
        type_resource_id: p.id,
        inherit: p.inherit.length ? p.inherit : null,
      })),
      access_implies: Object.fromEntries(
        implies.filter((r) => r.id.trim()).map((r) => [r.id.trim(), r.implied]),
      ),
    };
    try {
      if (props.type)
        await update.mutateAsync({
          serviceId: props.serviceId,
          typeResourceId: props.type.id,
          data: body,
        });
      else await create.mutateAsync({ serviceId: props.serviceId, data: body });
      await refresh();
      props.onClose();
    } catch (e) {
      setError(e);
    }
  };

  return (
    <Drawer
      open={props.open}
      onClose={props.onClose}
      size={600}
      destroyOnHidden
      title={props.type ? t('types.edit') : t('types.add')}
      footer={
        <Flex justify="flex-end" gap={8}>
          <Button onClick={props.onClose}>{t('common.cancel')}</Button>
          <Button
            type="primary"
            disabled={!name.trim()}
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
        <div>
          <label htmlFor="type-name" style={{ display: 'block', marginBottom: 4, fontWeight: 600 }}>
            {t('types.col.name')}
          </label>
          <Input
            id="type-name"
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder={t('types.namePlaceholder')}
          />
          <Typography.Text type="secondary" style={{ fontSize: 12 }}>
            {t('types.nameHelp')}
          </Typography.Text>
        </div>

        <div>
          <div style={{ fontWeight: 600, marginBottom: 4 }}>{t('types.col.parents')}</div>
          <Typography.Text
            type="secondary"
            style={{ fontSize: 12, display: 'block', marginBottom: 8 }}
          >
            {t('types.parentsHelp')}
          </Typography.Text>
          <Flex vertical gap={8}>
            {parents.map((p) => (
              <Flex key={p.id} gap={8} align="center" wrap>
                <Select
                  showSearch
                  aria-label={t('types.parent')}
                  style={{ minWidth: 260 }}
                  value={p.id}
                  optionFilterProp="label"
                  onChange={(id) =>
                    setParents(parents.map((x) => (x.id === p.id ? { ...x, id } : x)))
                  }
                  options={candidates.map((c) => ({
                    value: c.id,
                    label: `${c.service_name} / ${c.name}`,
                  }))}
                />
                <Select
                  mode="tags"
                  aria-label={t('types.inherit')}
                  placeholder={t('types.inheritAll')}
                  style={{ minWidth: 200, flex: 1 }}
                  value={p.inherit}
                  tokenSeparators={[',', ' ']}
                  onChange={(inherit) =>
                    setParents(parents.map((x) => (x.id === p.id ? { ...x, inherit } : x)))
                  }
                  options={['read', 'write'].map((l) => ({ value: l }))}
                />
                <Button
                  type="text"
                  aria-label={t('cond.remove')}
                  icon={<X size={14} aria-hidden />}
                  onClick={() => setParents(parents.filter((x) => x.id !== p.id))}
                />
              </Flex>
            ))}
            <div>
              <Button
                size="small"
                icon={<Plus size={14} aria-hidden />}
                disabled={candidates.every((c) => parents.some((p) => p.id === c.id))}
                onClick={() =>
                  setParents([
                    ...parents,
                    {
                      id: candidates.find((c) => !parents.some((p) => p.id === c.id))?.id ?? '',
                      inherit: [],
                    },
                  ])
                }
              >
                {t('types.addParent')}
              </Button>
            </div>
          </Flex>
        </div>

        <div>
          <div style={{ fontWeight: 600, marginBottom: 4 }}>{t('types.col.levels')}</div>
          <Typography.Text
            type="secondary"
            style={{ fontSize: 12, display: 'block', marginBottom: 8 }}
          >
            {t('types.impliesHelp')}
          </Typography.Text>
          <Flex vertical gap={8}>
            {implies.map((r, i) => (
              <Flex key={i} gap={8} align="center" wrap>
                <Input
                  aria-label={t('types.level')}
                  className="mono"
                  style={{ width: 140 }}
                  value={r.id}
                  placeholder="write"
                  onChange={(e) =>
                    setImplies(implies.map((x, j) => (j === i ? { ...x, id: e.target.value } : x)))
                  }
                />
                <span aria-hidden>⊃</span>
                <Select
                  mode="tags"
                  aria-label={t('types.includes')}
                  style={{ flex: 1, minWidth: 200 }}
                  value={r.implied}
                  tokenSeparators={[',', ' ']}
                  placeholder="read"
                  onChange={(implied) =>
                    setImplies(implies.map((x, j) => (j === i ? { ...x, implied } : x)))
                  }
                />
                <Button
                  type="text"
                  aria-label={t('cond.remove')}
                  icon={<X size={14} aria-hidden />}
                  onClick={() => setImplies(implies.filter((_, j) => j !== i))}
                />
              </Flex>
            ))}
            <div>
              <Button
                size="small"
                icon={<Plus size={14} aria-hidden />}
                onClick={() => setImplies([...implies, { id: '', implied: [] }])}
              >
                {t('types.addLevel')}
              </Button>
            </div>
          </Flex>
        </div>
      </Flex>
    </Drawer>
  );
}
