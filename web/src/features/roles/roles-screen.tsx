'use client';

import { useQueries } from '@tanstack/react-query';
import { Alert, Button, Flex, Input, Modal, Table, Typography } from 'antd';
import { Plus } from 'lucide-react';
import Link from 'next/link';
import { useRouter } from 'next/navigation';
import { useState } from 'react';
import { useTranslation } from 'react-i18next';

import { listRolePermissions } from '@/api/admin/role-permission/role-permission';
import { useCreateRole } from '@/api/admin/role/role';
import { EmptyBlock, errorDetail, ErrorBlock, LoadingBlock, PageHeader } from '@/components/kit';
import { useAccess } from '@/lib/access';

import { AccessTabs } from '../access/access-tabs';
import { useRefreshAll, useRoles, useRules } from '../data/queries';

export function RolesScreen() {
  const { t } = useTranslation();
  const router = useRouter();
  const access = useAccess();
  const roles = useRoles();
  const rules = useRules();
  const create = useCreateRole();
  const refresh = useRefreshAll();

  const [creating, setCreating] = useState(false);
  const [name, setName] = useState('');
  const [error, setError] = useState<unknown>(null);

  const counts = useQueries({
    queries: (roles.data ?? []).map((r) => ({
      queryKey: ['permission-count', r.id],
      queryFn: async () => {
        const res = await listRolePermissions(r.id);
        return res.status === 200 ? (res.data ?? []).length : 0;
      },
    })),
  });
  const countOf = (index: number) => counts[index]?.data;
  const canCreate = access.whole('role', 'write');

  const submit = async () => {
    setError(null);
    try {
      const res = await create.mutateAsync({ data: { name: name.trim() } });
      await refresh();
      setCreating(false);
      setName('');
      router.push(`/roles/${(res.data as { id: string }).id}`);
    } catch (e) {
      setError(e);
    }
  };

  return (
    <div className="page">
      <PageHeader
        title={t('nav.roles')}
        extra={
          canCreate && (
            <Button
              type="primary"
              icon={<Plus size={16} aria-hidden />}
              onClick={() => setCreating(true)}
            >
              {t('roles.new')}
            </Button>
          )
        }
      />
      <AccessTabs />
      {roles.isPending ? (
        <LoadingBlock rows={4} />
      ) : roles.isError ? (
        <ErrorBlock error={roles.error} onRetry={() => void roles.refetch()} />
      ) : (roles.data ?? []).length === 0 ? (
        <EmptyBlock
          title={t('roles.empty.title')}
          hint={t('roles.empty.hint')}
          action={
            canCreate ? (
              <Button type="primary" onClick={() => setCreating(true)}>
                {t('roles.new')}
              </Button>
            ) : undefined
          }
        />
      ) : (
        <Table
          rowKey={(r) => r.id}
          size="middle"
          pagination={false}
          dataSource={roles.data}
          onRow={(r) => ({
            onClick: () => router.push(`/roles/${r.id}`),
            style: { cursor: 'pointer' },
          })}
          columns={[
            {
              title: t('roles.col.name'),
              key: 'name',
              render: (_, r) => (
                <Link
                  href={`/roles/${r.id}`}
                  onClick={(e) => e.stopPropagation()}
                  style={{ fontWeight: 500 }}
                >
                  {r.name}
                </Link>
              ),
            },
            {
              title: t('roles.col.perms'),
              key: 'perms',
              width: 120,
              render: (_, __, i) => countOf(i) ?? '…',
            },
            {
              title: t('roles.col.usage'),
              key: 'usage',
              width: 180,
              render: (_, r) => {
                const n = (rules.data ?? []).filter((x) => x.role_id === r.id).length;
                return n === 0 ? (
                  '—'
                ) : (
                  <Link href={`/roles/${r.id}?tab=usage`} onClick={(e) => e.stopPropagation()}>
                    {t('roles.usageCount', { count: n })}
                  </Link>
                );
              },
            },
          ]}
        />
      )}
      <Modal
        open={creating}
        title={t('roles.new')}
        okText={t('common.create')}
        cancelText={t('common.cancel')}
        okButtonProps={{ disabled: !name.trim(), loading: create.isPending }}
        onCancel={() => setCreating(false)}
        onOk={() => void submit()}
        destroyOnHidden
      >
        <Flex vertical gap={8}>
          {error != null && (
            <Alert type="error" showIcon title={errorDetail(error) ?? t('common.saveFailed')} />
          )}
          <Input
            autoFocus
            aria-label={t('roles.nameLabel')}
            placeholder={t('roles.namePlaceholder')}
            value={name}
            onChange={(e) => setName(e.target.value)}
            onPressEnter={() => name.trim() && void submit()}
          />
          <Typography.Text type="secondary" style={{ fontSize: 13 }}>
            {t('roles.nameHelp')}
          </Typography.Text>
        </Flex>
      </Modal>
    </div>
  );
}
