'use client';

import { Alert, Button, Dropdown, Flex, Modal, Table, Tabs, Tag } from 'antd';
import { MoreHorizontal, Plus } from 'lucide-react';
import Link from 'next/link';
import { useRouter } from 'next/navigation';
import { useState } from 'react';
import { useTranslation } from 'react-i18next';

import type { RolePermissionWithContext } from '@/api/admin/model';
import {
  useDeleteRolePermission,
  useListRolePermissions,
} from '@/api/admin/role-permission/role-permission';
import { useDeleteRole, useUpdateRole } from '@/api/admin/role/role';
import { EditableTitle } from '@/components/editable-title';
import {
  EmptyBlock,
  errorDetail,
  errorExtra,
  ErrorBlock,
  LoadingBlock,
  PageHeader,
} from '@/components/kit';
import { describeConditions } from '@/domain/rule';
import { useAccess } from '@/lib/access';
import { useQueryState } from '@/lib/use-query-state';

import { StatusTag, WhatCell, WhereCell, WhoCell } from '../access/rule-cells';
import { useRefreshAll, useRoles, useRules } from '../data/queries';
import { useGrant } from '../grant/grant-context';
import { PermissionDrawer } from './permission-drawer';

export function RolePage({ roleId }: { roleId: string }) {
  const { t } = useTranslation();
  const router = useRouter();
  const access = useAccess();
  const grant = useGrant();
  const roles = useRoles();
  const rules = useRules();
  const perms = useListRolePermissions(roleId, {
    query: {
      select: (r) => (r.status === 200 ? (r.data ?? []) : ([] as RolePermissionWithContext[])),
    },
  });
  const updateRole = useUpdateRole();
  const removeRole = useDeleteRole();
  const removePerm = useDeleteRolePermission();
  const refresh = useRefreshAll();

  const [tab, setTab] = useQueryState('tab', 'permissions');
  const [drawer, setDrawer] = useState<{ open: boolean; item?: RolePermissionWithContext }>({
    open: false,
  });

  const role = roles.data?.find((r) => r.id === roleId);
  const using = (rules.data ?? []).filter((r) => r.role_id === roleId);
  const canWrite = access.any('role', 'write');

  if (roles.isPending)
    return (
      <div className="page">
        <LoadingBlock rows={4} />
      </div>
    );
  if (roles.isError)
    return (
      <div className="page">
        <ErrorBlock error={roles.error} onRetry={() => void roles.refetch()} />
      </div>
    );
  if (!role)
    return (
      <div className="page">
        <EmptyBlock
          title={t('role.notFound')}
          action={<Link href="/roles">{t('nav.roles')}</Link>}
        />
      </div>
    );

  const askDelete = () =>
    Modal.confirm({
      title: t('role.delete.title', { name: role.name }),
      content: t('role.delete.body'),
      okText: t('common.delete'),
      okButtonProps: { danger: true },
      cancelText: t('common.cancel'),
      onOk: async () => {
        try {
          await removeRole.mutateAsync({ roleId });
          await refresh();
          router.push('/roles');
        } catch (e) {
          const extra = errorExtra(e);
          Modal.error({
            title: t('role.delete.blocked'),
            content: extra?.type === 'blocked_by' ? t('role.delete.blockedBy') : errorDetail(e),
          });
          throw e;
        }
      },
    });

  const rename = async (name: string) => {
    if (!name.trim() || name.trim() === role.name) return;
    try {
      await updateRole.mutateAsync({ roleId, data: { name: name.trim() } });
      await refresh();
    } catch (e) {
      Modal.error({ title: t('common.saveFailed'), content: errorDetail(e) });
    }
  };

  const dropPerm = async (p: RolePermissionWithContext) => {
    Modal.confirm({
      title: t('role.perm.deleteTitle'),
      content: using.length > 0 ? t('role.perm.deleteImpact', { count: using.length }) : undefined,
      okText: t('common.delete'),
      okButtonProps: { danger: true },
      cancelText: t('common.cancel'),
      onOk: async () => {
        await removePerm.mutateAsync({ roleId, permissionId: p.id });
        await refresh();
      },
    });
  };

  const items = perms.data ?? [];

  return (
    <div className="page">
      <PageHeader
        crumbs={[{ label: t('nav.roles'), href: '/roles' }, { label: role.name }]}
        title={
          canWrite ? (
            <EditableTitle
              value={role.name}
              label={t('group.rename')}
              onSave={(v) => void rename(v)}
            />
          ) : (
            role.name
          )
        }
        extra={
          <>
            {access.whole('assignment_rule', 'write') && (
              <Button type="primary" onClick={() => grant.open({ roleId })}>
                {t('roles.grant')}
              </Button>
            )}
            {access.whole('role', 'write') && (
              <Dropdown
                trigger={['click']}
                menu={{
                  items: [{ key: 'delete', danger: true, label: t('common.delete') }],
                  onClick: askDelete,
                }}
              >
                <Button
                  aria-label={t('common.actions')}
                  icon={<MoreHorizontal size={16} aria-hidden />}
                />
              </Dropdown>
            )}
          </>
        }
      />
      <Tabs
        activeKey={tab}
        onChange={setTab}
        items={[
          { key: 'permissions', label: t('role.tab.permissions', { count: items.length }) },
          { key: 'usage', label: t('role.tab.usage', { count: using.length }) },
        ]}
      />

      {tab === 'permissions' && (
        <>
          {canWrite && (
            <Flex justify="flex-end" style={{ marginBottom: 12 }}>
              <Button
                type="primary"
                icon={<Plus size={16} aria-hidden />}
                onClick={() => setDrawer({ open: true })}
              >
                {t('role.perm.add')}
              </Button>
            </Flex>
          )}
          {perms.isPending ? (
            <LoadingBlock rows={3} />
          ) : perms.isError ? (
            <ErrorBlock error={perms.error} onRetry={() => void perms.refetch()} />
          ) : items.length === 0 ? (
            <EmptyBlock
              title={t('role.perm.empty')}
              action={
                canWrite ? (
                  <Button type="primary" onClick={() => setDrawer({ open: true })}>
                    {t('role.perm.add')}
                  </Button>
                ) : undefined
              }
            />
          ) : (
            <Table<RolePermissionWithContext>
              rowKey="id"
              size="middle"
              pagination={false}
              dataSource={items}
              columns={[
                {
                  title: t('access.col.where'),
                  key: 'where',
                  render: (_, p) => (
                    <div>
                      <div>{p.type_resource_name}</div>
                      <div style={{ color: 'var(--licetum-muted)', fontSize: 12 }}>
                        {p.service_name}
                      </div>
                    </div>
                  ),
                },
                {
                  title: t('role.col.scope'),
                  key: 'scope',
                  render: (_, p) =>
                    p.resource_id ? (
                      <span className="mono">{p.resource_id}</span>
                    ) : p.resource_from_claim ? (
                      t('rule.fromClaim', { claim: p.resource_from_claim.claim })
                    ) : p.resource_where?.length ? (
                      t('rule.where', { cond: describeConditions(p.resource_where) })
                    ) : (
                      t('rule.wholeType')
                    ),
                },
                {
                  title: t('check.level'),
                  key: 'access',
                  width: 220,
                  render: (_, p) =>
                    (p.access ?? []).map((a) => (
                      <Tag key={a} className="mono">
                        {a}
                      </Tag>
                    )),
                },
                {
                  title: t('access.col.status'),
                  key: 'status',
                  width: 150,
                  render: (_, p) =>
                    p.target_valid ? null : <Tag color="error">{t('rule.status.dead')}</Tag>,
                },
                {
                  title: '',
                  key: 'actions',
                  width: 170,
                  render: (_, p) =>
                    canWrite ? (
                      <Flex gap={8}>
                        <Button size="small" onClick={() => setDrawer({ open: true, item: p })}>
                          {t('common.edit')}
                        </Button>
                        <Button size="small" danger onClick={() => void dropPerm(p)}>
                          {t('common.delete')}
                        </Button>
                      </Flex>
                    ) : null,
                },
              ]}
            />
          )}
          <Alert type="info" showIcon style={{ marginTop: 16 }} title={t('role.perm.impactNote')} />
        </>
      )}

      {tab === 'usage' &&
        (using.length === 0 ? (
          <EmptyBlock title={t('role.usage.none')} hint={t('role.usage.noneHint')} />
        ) : (
          <Table
            rowKey="id"
            size="middle"
            pagination={false}
            dataSource={using}
            onRow={(r) => ({
              onClick: () => router.push(`/access/${r.id}`),
              style: { cursor: 'pointer' },
            })}
            columns={[
              { title: t('access.col.who'), key: 'who', render: (_, r) => <WhoCell rule={r} /> },
              {
                title: t('access.col.what'),
                key: 'what',
                width: 220,
                render: (_, r) => <WhatCell rule={r} />,
              },
              {
                title: t('access.col.where'),
                key: 'where',
                render: (_, r) => <WhereCell rule={r} />,
              },
              {
                title: t('access.col.status'),
                key: 'status',
                width: 150,
                render: (_, r) => <StatusTag rule={r} />,
              },
            ]}
          />
        ))}

      <PermissionDrawer
        roleId={roleId}
        open={drawer.open}
        item={drawer.item}
        onClose={() => setDrawer({ open: false })}
      />
    </div>
  );
}
