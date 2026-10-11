'use client';

import {
  Alert,
  Button,
  Checkbox,
  Flex,
  Input,
  Modal,
  Segmented,
  Select,
  Table,
  Tabs,
  Typography,
} from 'antd';
import Link from 'next/link';
import { usePathname, useRouter } from 'next/navigation';
import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { useRunBatch } from '@/api/admin/batch/batch';
import type { AssignmentRuleInput, TypeAccess } from '@/api/admin/model';
import { useGetPlatform, useGetPlatformMe } from '@/api/admin/platform/platform';
import {
  DecisionTag,
  EmptyBlock,
  errorDetail,
  ErrorBlock,
  LoadingBlock,
  PageHeader,
} from '@/components/kit';
import { describeMember } from '@/domain/members';
import { describeWho, hasId } from '@/domain/rule';
import { useAccess } from '@/lib/access';
import { useUndoNotice } from '@/lib/undo';

import {
  useGroupMembers,
  useGroupNames,
  useGroups,
  useRefreshAll,
  useRules,
} from '../data/queries';

const AREAS = [
  'service',
  'type_resource',
  'group',
  'group_member',
  'role',
  'assignment_rule',
] as const;

export function SettingsTabs() {
  const { t } = useTranslation();
  const router = useRouter();
  const pathname = usePathname();
  return (
    <Tabs
      activeKey={pathname.startsWith('/settings/logs') ? '/settings/logs' : '/settings/admins'}
      onChange={(k) => router.push(k)}
      items={[
        { key: '/settings/admins', label: t('settings.tab.admins') },
        { key: '/settings/logs', label: t('settings.tab.logs') },
      ]}
    />
  );
}

/** Who may manage Licetum itself: the same permissions as any service has, on the service “Licetum”. */
export function SettingsAdmins() {
  const { t } = useTranslation();
  const access = useAccess();
  const platform = useGetPlatform({
    query: { select: (r) => (r.status === 200 ? r.data : undefined) },
  });
  const me = useGetPlatformMe({
    query: { select: (r) => (r.status === 200 ? r.data : undefined) },
  });
  const groups = useGroups();
  const groupNames = useGroupNames();
  const rules = useRules();
  const members = useGroupMembers(platform.data?.admin_group_id ?? '');
  const refresh = useRefreshAll();
  const batch = useRunBatch();
  const undoNotice = useUndoNotice();

  const [granting, setGranting] = useState(false);
  const [who, setWho] = useState<'group' | 'person'>('group');
  const [groupId, setGroupId] = useState<string | undefined>();
  const [person, setPerson] = useState('');
  const [preset, setPreset] = useState<'view' | 'full'>('view');
  const [confirmed, setConfirmed] = useState(false);
  const [error, setError] = useState<unknown>(null);

  const platformId = platform.data?.service_id;
  const adminGroup = groups.data?.find((g) => g.id === platform.data?.admin_group_id);
  const platformRules = useMemo(
    () => (rules.data ?? []).filter((r) => r.service_id === platformId && !hasId(r.role_id)),
    [rules.data, platformId],
  );
  const canWrite = access.whole('assignment_rule', 'write');

  // The matrix: for each recipient, the levels it holds on each area (denials marked).
  const matrix = useMemo(() => {
    const byWho = new Map<
      string,
      { label: string; href?: string; cells: Map<string, { levels: Set<string>; deny: boolean }> }
    >();
    for (const r of platformRules) {
      const w = describeWho(r, groupNames.name, t);
      const key = `${w.kind}:${w.groupId ?? w.username ?? w.label}`;
      const entry = byWho.get(key) ?? {
        label: w.label,
        href: w.groupId
          ? `/people/groups/${w.groupId}`
          : w.username
            ? `/people/${encodeURIComponent(w.username)}`
            : undefined,
        cells: new Map(),
      };
      const cell = entry.cells.get(r.type_resource_name ?? '') ?? {
        levels: new Set<string>(),
        deny: false,
      };
      if (r.effect === 'deny') cell.deny = true;
      else for (const l of r.access ?? []) cell.levels.add(l);
      entry.cells.set(r.type_resource_name ?? '', cell);
      byWho.set(key, entry);
    }
    return [...byWho.values()];
  }, [platformRules, groupNames, t]);

  const grant = async () => {
    if (!platform.data) return;
    setError(null);
    const levels = preset === 'full' ? ['read', 'write'] : ['read'];
    const operations = platform.data.types.map((type) => {
      const body: AssignmentRuleInput = {
        type_resource_id: type.id,
        access: levels,
        name: t('admins.ruleName', {
          area: t(`admins.area.${type.name}`, { defaultValue: type.name }),
        }),
        ...(who === 'group'
          ? { member_of_group_id: groupId }
          : { subject: person.trim(), subject_kind: 'user' as const }),
      };
      return {
        op: 'create' as const,
        entity: 'assignment_rule' as const,
        id: crypto.randomUUID(),
        body,
      };
    });
    try {
      await batch.mutateAsync({ data: { operations } });
      await refresh();
      setGranting(false);
      undoNotice({
        message: t('admins.granted'),
        undo: async () => {
          await batch.mutateAsync({
            data: {
              operations: operations.map((o) => ({
                op: 'delete' as const,
                entity: 'assignment_rule' as const,
                id: o.id,
              })),
            },
          });
          await refresh();
        },
      });
    } catch (e) {
      setError(e);
    }
  };

  const mine = me.data?.types as Record<string, TypeAccess> | undefined;
  const levelsOf = (type: string) => {
    const a = mine?.[type];
    const denied = (a?.denied ?? []).length > 0;
    return {
      read: (a?.whole_type ?? []).includes('read'),
      write: (a?.whole_type ?? []).includes('write'),
      denied,
    };
  };

  if (platform.isPending || me.isPending)
    return (
      <div className="page">
        <LoadingBlock rows={5} />
      </div>
    );
  if (platform.isError || me.isError)
    return (
      <div className="page">
        <ErrorBlock
          error={platform.error ?? me.error}
          onRetry={() => {
            void platform.refetch();
            void me.refetch();
          }}
        />
      </div>
    );

  const memberList = members.data ?? [];
  const canGrant = who === 'group' ? !!groupId : !!person.trim();

  return (
    <div className="page">
      <PageHeader title={t('settings.title')} />
      <SettingsTabs />

      <Flex vertical gap={16}>
        <section
          style={{
            background: 'var(--licetum-surface)',
            border: '1px solid var(--licetum-border)',
            borderRadius: 8,
            padding: 16,
          }}
          aria-labelledby="mine"
        >
          <Typography.Title level={5} id="mine" style={{ marginTop: 0 }}>
            {t('admins.mine')}
          </Typography.Title>
          <Flex gap={12} wrap>
            {AREAS.map((a) => {
              const l = levelsOf(a);
              return (
                <div key={a} style={{ minWidth: 150 }}>
                  <div style={{ color: 'var(--licetum-muted)', fontSize: 12 }}>
                    {t(`admins.area.${a}`)}
                  </div>
                  {l.denied ? (
                    <DecisionTag decision="deny">{t('admins.level.denied')}</DecisionTag>
                  ) : l.write ? (
                    <DecisionTag decision="allow">{t('admins.level.write')}</DecisionTag>
                  ) : l.read ? (
                    <DecisionTag decision="inherit">{t('admins.level.read')}</DecisionTag>
                  ) : (
                    <span style={{ color: 'var(--licetum-muted)' }}>—</span>
                  )}
                </div>
              );
            })}
          </Flex>
        </section>

        <section
          style={{
            background: 'var(--licetum-surface)',
            border: '1px solid var(--licetum-border)',
            borderRadius: 8,
            padding: 16,
          }}
          aria-labelledby="admins"
        >
          <Typography.Title level={5} id="admins" style={{ marginTop: 0 }}>
            {t('admins.group')}
          </Typography.Title>
          {!adminGroup ? (
            <Alert type="error" showIcon title={t('admins.noAdmins')} />
          ) : (
            <>
              <Typography.Paragraph style={{ marginBottom: 8 }}>
                <Link href={`/people/groups/${adminGroup.id}`} style={{ fontWeight: 600 }}>
                  {adminGroup.name}
                </Link>{' '}
                <Typography.Text type="secondary">
                  · {t('admins.members', { count: memberList.length })}
                </Typography.Text>
              </Typography.Paragraph>
              {memberList.length === 0 ? (
                <Alert type="warning" showIcon title={t('admins.noMembers')} />
              ) : (
                <Flex vertical gap={4}>
                  {memberList.map((m) => {
                    const v = describeMember(m, groupNames.name, t);
                    return (
                      <div key={m.id}>
                        {v.kind === 'person' ? (
                          <Link
                            href={`/people/${encodeURIComponent(v.username ?? '')}`}
                            className="mono"
                          >
                            @{v.label}
                          </Link>
                        ) : (
                          <span className="mono">{v.label}</span>
                        )}{' '}
                        <span style={{ color: 'var(--licetum-muted)', fontSize: 12 }}>
                          ({t(`group.kind.${v.kind}Short`)})
                        </span>
                      </div>
                    );
                  })}
                </Flex>
              )}
              <Typography.Paragraph
                type="secondary"
                style={{ fontSize: 12, marginTop: 8, marginBottom: 0 }}
              >
                {t('admins.conditionNote')}
              </Typography.Paragraph>
            </>
          )}
        </section>

        <section
          style={{
            background: 'var(--licetum-surface)',
            border: '1px solid var(--licetum-border)',
            borderRadius: 8,
            padding: 16,
          }}
          aria-labelledby="matrix"
        >
          <Flex justify="space-between" align="center" wrap gap={8} style={{ marginBottom: 8 }}>
            <Typography.Title level={5} id="matrix" style={{ margin: 0 }}>
              {t('admins.matrixTitle')}
            </Typography.Title>
            {canWrite && (
              <Button
                type="primary"
                onClick={() => {
                  setGranting(true);
                  setError(null);
                  setConfirmed(false);
                }}
              >
                {t('admins.grant')}
              </Button>
            )}
          </Flex>
          {matrix.length === 0 ? (
            <EmptyBlock title={t('admins.matrix.empty')} />
          ) : (
            <Table
              size="small"
              pagination={false}
              rowKey="label"
              dataSource={matrix}
              scroll={{ x: 720 }}
              columns={[
                {
                  title: t('access.col.recipient'),
                  key: 'who',
                  render: (_, r) => (r.href ? <Link href={r.href}>{r.label}</Link> : r.label),
                  width: 200,
                  fixed: 'left',
                },
                ...AREAS.map((a) => ({
                  title: t(`admins.area.${a}`),
                  key: a,
                  width: 120,
                  render: (_: unknown, r: (typeof matrix)[number]) => {
                    const cell = r.cells.get(a);
                    if (!cell) return '—';
                    if (cell.deny)
                      return <DecisionTag decision="deny">{t('admins.level.denied')}</DecisionTag>;
                    return cell.levels.has('write') ? (
                      <DecisionTag decision="allow">{t('admins.level.write')}</DecisionTag>
                    ) : (
                      <DecisionTag decision="inherit">{t('admins.level.read')}</DecisionTag>
                    );
                  },
                })),
              ]}
            />
          )}
          <Alert type="warning" showIcon style={{ marginTop: 12 }} title={t('admins.lockNote')} />
          <Typography.Paragraph
            type="secondary"
            style={{ fontSize: 12, marginTop: 8, marginBottom: 0 }}
          >
            {t('admins.recover')}
          </Typography.Paragraph>
        </section>
      </Flex>

      <Modal
        open={granting}
        title={t('admins.grant')}
        onCancel={() => setGranting(false)}
        okText={t('admins.grantConfirm')}
        cancelText={t('common.cancel')}
        okButtonProps={{
          disabled: !canGrant || (preset === 'full' && !confirmed),
          loading: batch.isPending,
        }}
        onOk={() => void grant()}
        destroyOnHidden
      >
        <Flex vertical gap={12}>
          {error != null && (
            <Alert type="error" showIcon title={errorDetail(error) ?? t('common.saveFailed')} />
          )}
          <Segmented
            value={who}
            onChange={(v) => setWho(v as 'group' | 'person')}
            options={[
              { label: t('grant.who.group'), value: 'group' },
              { label: t('grant.who.person'), value: 'person' },
            ]}
            aria-label={t('grant.who.label')}
          />
          {who === 'group' ? (
            <Select
              showSearch
              placeholder={t('grant.who.groupPlaceholder')}
              aria-label={t('grant.who.group')}
              value={groupId}
              onChange={setGroupId}
              optionFilterProp="label"
              options={(groups.data ?? []).map((g) => ({ value: g.id, label: g.name }))}
            />
          ) : (
            <Input
              placeholder={t('grant.who.personPlaceholder')}
              aria-label={t('grant.who.person')}
              value={person}
              onChange={(e) => setPerson(e.target.value)}
              className="mono"
            />
          )}
          <Segmented
            value={preset}
            onChange={(v) => {
              setPreset(v as 'view' | 'full');
              setConfirmed(false);
            }}
            options={[
              { label: t('admins.preset.view'), value: 'view' },
              { label: t('admins.preset.full'), value: 'full' },
            ]}
            aria-label={t('admins.presetLabel')}
          />
          <Typography.Text type="secondary" style={{ fontSize: 13 }}>
            {preset === 'view' ? t('admins.preset.viewHint') : t('admins.preset.fullHint')}
          </Typography.Text>
          {preset === 'full' && (
            <Checkbox checked={confirmed} onChange={(e) => setConfirmed(e.target.checked)}>
              {t('admins.preset.confirm')}
            </Checkbox>
          )}
        </Flex>
      </Modal>
    </div>
  );
}
