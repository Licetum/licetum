'use client';

import { Alert, Button, Drawer, Flex, Typography } from 'antd';
import { useCallback, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';

import type { RolePermissionInput, RolePermissionWithContext } from '@/api/admin/model';
import {
  useCreateRolePermission,
  useUpdateRolePermission,
} from '@/api/admin/role-permission/role-permission';
import { errorDetail } from '@/components/kit';

import { useRefreshAll } from '../data/queries';
import { emptyForm, toPredicate, type GrantForm } from '../grant/grant-model';
import { LevelsFields, WhereFields, useTargetData } from '../grant/target-fields';

function formOf(item?: RolePermissionWithContext): GrantForm {
  const form = emptyForm();
  if (!item) return form;
  form.serviceId = item.service_id;
  form.typeId = item.type_resource_id;
  const access = item.access ?? [];
  form.levels = access.filter((a) => !a.startsWith('@'));
  form.accessGroups = access.filter((a) => a.startsWith('@')).map((a) => a.slice(1));
  if (item.resource_id)
    Object.assign(form, { scope: 'resources', resourceIds: [item.resource_id] });
  else if (item.resource_where?.length) {
    Object.assign(form, {
      scope: 'where',
      where: item.resource_where.map((c, i) => ({
        id: `w${i}`,
        source: 'claim' as const,
        attr: c.attr,
        op: c.op,
        value: Array.isArray(c.value) ? c.value.join(', ') : c.value,
      })),
    });
  } else if (item.resource_from_claim) {
    Object.assign(form, {
      scope: 'claim',
      claim: item.resource_from_claim.claim,
      matchBy: item.resource_from_claim.match_by ?? 'name',
    });
  }
  return form;
}

/** One permission of a role: where, and which levels. A role item takes no "*": denying everything is a denial rule. */
export function PermissionDrawer(props: {
  roleId: string;
  open: boolean;
  item?: RolePermissionWithContext;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const refresh = useRefreshAll();
  const create = useCreateRolePermission();
  const update = useUpdateRolePermission();

  const [form, setForm] = useState<GrantForm>(emptyForm);
  const [error, setError] = useState<unknown>(null);
  const patch = useCallback((next: Partial<GrantForm>) => setForm((f) => ({ ...f, ...next })), []);

  useEffect(() => {
    if (props.open) {
      setForm(formOf(props.item));
      setError(null);
    }
  }, [props.open, props.item]);

  const data = useTargetData(form, patch, props.open);
  const accessList = [...form.levels, ...form.accessGroups.map((g) => `@${g}`)];
  const incomplete =
    !form.typeId ||
    accessList.length === 0 ||
    (form.scope === 'resources' && form.resourceIds.length === 0) ||
    (form.scope === 'where' && form.where.length === 0) ||
    (form.scope === 'claim' && !form.claim.trim());

  const submit = async () => {
    setError(null);
    const base: RolePermissionInput = { type_resource_id: form.typeId!, access: accessList };
    const bodies: RolePermissionInput[] =
      form.scope === 'resources'
        ? form.resourceIds.map((id) => ({ ...base, resource_id: id }))
        : form.scope === 'where'
          ? [{ ...base, resource_where: form.where.map(toPredicate) }]
          : form.scope === 'claim'
            ? [
                {
                  ...base,
                  resource_from_claim: {
                    claim: form.claim.trim(),
                    lookup_type_resource_id: form.typeId!,
                    match_by: form.matchBy || 'name',
                  },
                },
              ]
            : [base];
    try {
      if (props.item)
        await update.mutateAsync({
          roleId: props.roleId,
          permissionId: props.item.id,
          data: bodies[0]!,
        });
      else
        for (const body of bodies) await create.mutateAsync({ roleId: props.roleId, data: body });
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
      title={props.item ? t('role.perm.edit') : t('role.perm.add')}
      footer={
        <Flex justify="flex-end" gap={8}>
          <Button onClick={props.onClose}>{t('common.cancel')}</Button>
          <Button
            type="primary"
            disabled={incomplete}
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
          <Typography.Text
            type="secondary"
            style={{
              fontSize: 12,
              fontWeight: 600,
              letterSpacing: '.04em',
              textTransform: 'uppercase',
              display: 'block',
              marginBottom: 8,
            }}
          >
            {t('grant.where.label')}
          </Typography.Text>
          <WhereFields form={form} patch={patch} data={data} />
        </div>
        <div>
          <Typography.Text
            type="secondary"
            style={{
              fontSize: 12,
              fontWeight: 600,
              letterSpacing: '.04em',
              textTransform: 'uppercase',
              display: 'block',
              marginBottom: 8,
            }}
          >
            {t('grant.what.label')}
          </Typography.Text>
          <LevelsFields form={form} patch={patch} data={data} />
          <Typography.Text
            type="secondary"
            style={{ fontSize: 12, display: 'block', marginTop: 8 }}
          >
            {t('role.perm.noWildcard')}
          </Typography.Text>
        </div>
      </Flex>
    </Drawer>
  );
}
