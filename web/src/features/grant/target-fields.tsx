'use client';

import { Checkbox, Flex, Input, Segmented, Select, Typography } from 'antd';
import { useEffect, useMemo } from 'react';
import { useTranslation } from 'react-i18next';

import { useListResourcesOfType, useListSynchronizers } from '@/api/admin/resource/resource';
import { useListAccessGroups } from '@/api/admin/type-resource/type-resource';

import { useAllTypes } from '../data/queries';
import { CondEditor } from './cond-editor';
import { levelsOf, type GrantForm, type ScopeMode } from './grant-model';

/** Everything the "what" and "where" parts of a permission need to know about the chosen type. */
export function useTargetData(
  form: GrantForm,
  patch: (next: Partial<GrantForm>) => void,
  enabled: boolean,
) {
  const types = useAllTypes();
  const services = useMemo(() => {
    const seen = new Map<string, string>();
    for (const type of types.data ?? []) seen.set(type.service_id, type.service_name);
    return [...seen].map(([id, name]) => ({ value: id, label: name }));
  }, [types.data]);
  const typesOfService = useMemo(
    () => (types.data ?? []).filter((x) => x.service_id === form.serviceId),
    [types.data, form.serviceId],
  );
  const type = useMemo(
    () => (types.data ?? []).find((x) => x.id === form.typeId),
    [types.data, form.typeId],
  );

  // A type given by the opener brings its service along.
  useEffect(() => {
    if (type && form.serviceId !== type.service_id) patch({ serviceId: type.service_id });
  }, [type, form.serviceId, patch]);

  const typeReady = !!form.serviceId && !!form.typeId;
  const synchronizers = useListSynchronizers(form.serviceId ?? '', form.typeId ?? '', {
    query: {
      enabled: enabled && typeReady,
      select: (r) => (r.status === 200 ? (r.data ?? []) : []),
    },
  });
  const hasSource = (synchronizers.data?.length ?? 0) > 0;
  const accessGroups = useListAccessGroups(form.serviceId ?? '', form.typeId ?? '', {
    query: {
      enabled: enabled && typeReady,
      select: (r) => (r.status === 200 ? (r.data ?? []) : []),
    },
  });
  const resources = useListResourcesOfType(form.typeId ?? '', undefined, {
    query: {
      enabled: enabled && typeReady && form.scope === 'resources' && hasSource,
      select: (r) => (r.status === 200 ? (r.data ?? []) : []),
    },
  });
  const levelChoices = useMemo(() => levelsOf(type), [type]);

  return {
    types,
    services,
    typesOfService,
    type,
    typeReady,
    synchronizers,
    hasSource,
    accessGroups,
    resources,
    levelChoices,
  };
}

export type TargetData = ReturnType<typeof useTargetData>;

function presetOf(levels: string[], all: string[]): string {
  const sorted = [...levels].sort().join();
  if (sorted === 'read') return 'read';
  if (sorted === 'read,write') return 'edit';
  if (sorted === [...all].sort().join()) return 'all';
  return 'custom';
}

/** The levels of access: three common sets, the full list of the type's levels, and its named sets (@name). */
export function LevelsFields({
  form,
  patch,
  data,
}: {
  form: GrantForm;
  patch: (next: Partial<GrantForm>) => void;
  data: TargetData;
}) {
  const { t } = useTranslation();
  return (
    <Flex vertical gap={8}>
      <Segmented
        aria-label={t('grant.what.presets')}
        value={presetOf(form.levels, data.levelChoices)}
        onChange={(v) => {
          if (v === 'read') patch({ levels: ['read'] });
          if (v === 'edit') patch({ levels: ['read', 'write'] });
          if (v === 'all') patch({ levels: data.levelChoices });
        }}
        options={[
          { label: t('grant.what.read'), value: 'read' },
          { label: t('grant.what.edit'), value: 'edit' },
          { label: t('grant.what.all'), value: 'all' },
          { label: t('grant.what.custom'), value: 'custom' },
        ]}
      />
      <Checkbox.Group
        value={form.levels}
        onChange={(levels) => patch({ levels: levels as string[] })}
        options={[
          ...data.levelChoices.map((l) => ({ label: <span className="mono">{l}</span>, value: l })),
          ...(form.deny
            ? [
                {
                  label: <span className="mono">* ({t('grant.what.everyLevel')})</span>,
                  value: '*',
                },
              ]
            : []),
        ]}
      />
      {(data.accessGroups.data?.length ?? 0) > 0 && (
        <Checkbox.Group
          value={form.accessGroups}
          onChange={(accessGroupsValue) => patch({ accessGroups: accessGroupsValue as string[] })}
          options={(data.accessGroups.data ?? []).map((g) => ({
            label: (
              <span className="mono">
                @{g.name} = {(g.levels ?? []).join(', ')}
              </span>
            ),
            value: g.name,
          }))}
        />
      )}
    </Flex>
  );
}

/** The place: a service, a type, and which of its resources — all of them, chosen ones, by attribute, by the person's claim. */
export function WhereFields({
  form,
  patch,
  data,
}: {
  form: GrantForm;
  patch: (next: Partial<GrantForm>) => void;
  data: TargetData;
}) {
  const { t } = useTranslation();
  return (
    <Flex vertical gap={8}>
      <Flex gap={8} wrap>
        <Select
          showSearch
          aria-label={t('grant.where.service')}
          placeholder={t('grant.where.service')}
          style={{ minWidth: 220, flex: 1 }}
          value={form.serviceId}
          onChange={(serviceId) =>
            patch({ serviceId, typeId: undefined, scope: 'all', resourceIds: [], accessGroups: [] })
          }
          optionFilterProp="label"
          loading={data.types.isPending}
          options={data.services}
        />
        <Select
          showSearch
          aria-label={t('grant.where.type')}
          placeholder={t('grant.where.type')}
          style={{ minWidth: 220, flex: 1 }}
          disabled={!form.serviceId}
          value={form.typeId}
          onChange={(typeId) => patch({ typeId, scope: 'all', resourceIds: [], accessGroups: [] })}
          optionFilterProp="label"
          options={data.typesOfService.map((x) => ({ value: x.id, label: x.name }))}
        />
      </Flex>
      {data.typeReady && (
        <>
          <Segmented
            aria-label={t('grant.where.scope')}
            value={form.scope}
            onChange={(scope) => patch({ scope: scope as ScopeMode })}
            options={[
              { label: t('grant.where.all'), value: 'all' },
              { label: t('grant.where.resources'), value: 'resources', disabled: !data.hasSource },
              { label: t('grant.where.byAttr'), value: 'where', disabled: !data.hasSource },
              { label: t('grant.where.byClaim'), value: 'claim', disabled: !data.hasSource },
            ]}
          />
          {!data.hasSource && !data.synchronizers.isPending && (
            <Typography.Text type="secondary" style={{ fontSize: 12 }}>
              {t('grant.where.noSource')}
            </Typography.Text>
          )}
          {form.scope === 'resources' && (
            <Select
              mode="multiple"
              showSearch
              aria-label={t('grant.where.resources')}
              placeholder={t('grant.where.resourcesPlaceholder')}
              value={form.resourceIds}
              onChange={(resourceIds) => patch({ resourceIds })}
              loading={data.resources.isPending}
              optionFilterProp="label"
              options={(data.resources.data ?? []).map((r) => ({
                value: r.id,
                label: `${r.name} (${r.id})`,
              }))}
              notFoundContent={t('grant.where.noResources')}
              maxTagCount="responsive"
            />
          )}
          {form.scope === 'resources' && form.resourceIds.length > 1 && (
            <Typography.Text type="secondary" style={{ fontSize: 12 }}>
              {t('grant.where.manyRules', { count: form.resourceIds.length })}
            </Typography.Text>
          )}
          {form.scope === 'where' && (
            <>
              <CondEditor
                value={form.where}
                onChange={(where) => patch({ where })}
                addLabel={t('cond.add')}
                label={t('grant.where.byAttr')}
                attrPlaceholder={t('grant.where.attrPlaceholder')}
              />
              <Typography.Text type="secondary" style={{ fontSize: 12 }}>
                {t('grant.where.byAttrHint')}
              </Typography.Text>
            </>
          )}
          {form.scope === 'claim' && (
            <Flex gap={8} wrap>
              <Input
                aria-label={t('grant.where.claim')}
                placeholder={t('grant.where.claim')}
                value={form.claim}
                onChange={(e) => patch({ claim: e.target.value })}
                className="mono"
                style={{ flex: 1, minWidth: 160 }}
              />
              <Input
                aria-label={t('grant.where.matchBy')}
                placeholder={t('grant.where.matchBy')}
                value={form.matchBy}
                onChange={(e) => patch({ matchBy: e.target.value })}
                className="mono"
                style={{ flex: 1, minWidth: 160 }}
              />
              <Typography.Text type="secondary" style={{ fontSize: 12, width: '100%' }}>
                {t('grant.where.byClaimHint')}
              </Typography.Text>
            </Flex>
          )}
        </>
      )}
    </Flex>
  );
}
