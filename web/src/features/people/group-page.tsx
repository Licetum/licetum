'use client';

import {
  Alert,
  Button,
  DatePicker,
  Dropdown,
  Flex,
  Input,
  Modal,
  Segmented,
  Select,
  Switch,
  Table,
  Tabs,
  Tag,
  Typography,
} from 'antd';
import dayjs from 'dayjs';
import { MoreHorizontal, Plus } from 'lucide-react';
import Link from 'next/link';
import { useRouter } from 'next/navigation';
import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { useRunBatch } from '@/api/admin/batch/batch';
import { useDeleteGroup, useUpdateGroup } from '@/api/admin/group/group';
import type { GroupMember, GroupMemberInput } from '@/api/admin/model';
import { EditableTitle } from '@/components/editable-title';
import {
  EmptyBlock,
  errorDetail,
  errorExtra,
  ErrorBlock,
  LoadingBlock,
  PageHeader,
} from '@/components/kit';
import { usePreferences } from '@/components/providers';
import { formatDate } from '@/domain/format';
import { endOfDayISO } from '@/domain/format';
import { describeMember, personPredicate } from '@/domain/members';
import { useAccess } from '@/lib/access';
import { useUndoNotice } from '@/lib/undo';
import { useQueryState } from '@/lib/use-query-state';

import { WhatCell, WhereCell } from '../access/rule-cells';
import {
  useAllMembers,
  useGroupMembers,
  useGroupNames,
  useGroups,
  useRefreshAll,
  useRules,
} from '../data/queries';
import { CondEditor } from '../grant/cond-editor';
import { useGrant } from '../grant/grant-context';
import { condReady, toPredicate, type Cond } from '../grant/grant-model';

type Kind = 'person' | 'nested' | 'condition';

export function GroupPage({ groupId }: { groupId: string }) {
  const { t } = useTranslation();
  const { lang } = usePreferences();
  const router = useRouter();
  const access = useAccess();
  const grant = useGrant();
  const groups = useGroups();
  const groupNames = useGroupNames();
  const members = useGroupMembers(groupId);
  const rules = useRules();
  const allMembers = useAllMembers((groups.data ?? []).map((g) => g.id));
  const batch = useRunBatch();
  const update = useUpdateGroup();
  const remove = useDeleteGroup();
  const refresh = useRefreshAll();
  const undoNotice = useUndoNotice();

  const [tab, setTab] = useQueryState('tab', 'composition');
  const [adding, setAdding] = useState(false);
  const [kind, setKind] = useState<Kind>('person');
  const [person, setPerson] = useState('');
  const [nested, setNested] = useState<string | undefined>();
  const [conds, setConds] = useState<Cond[]>([]);
  const [until, setUntil] = useState<string | undefined>();
  const [error, setError] = useState<unknown>(null);

  const group = groups.data?.find((g) => g.id === groupId);
  const canWrite = access.whole('group_member', 'write') || access.any('group_member', 'write');
  const canEditGroup = access.any('group', 'write');

  const own = useMemo(
    () =>
      (rules.data ?? []).filter(
        (r) => r.member_of_group_id === groupId || r.subject === `group:${groupId}`,
      ),
    [rules.data, groupId],
  );
  const nestedIn = useMemo(
    () =>
      [...allMembers.byGroup.entries()]
        .filter(([, ms]) => ms.some((m) => m.member_group_id === groupId))
        .map(([id]) => id),
    [allMembers.byGroup, groupId],
  );

  if (groups.isPending)
    return (
      <div className="page">
        <LoadingBlock rows={5} />
      </div>
    );
  if (groups.isError)
    return (
      <div className="page">
        <ErrorBlock error={groups.error} onRetry={() => void groups.refetch()} />
      </div>
    );
  if (!group)
    return (
      <div className="page">
        <EmptyBlock
          title={t('group.notFound.title')}
          hint={t('group.notFound.hint')}
          action={<Link href="/people">{t('group.notFound.back')}</Link>}
        />
      </div>
    );

  const memberOps = async (
    operations: Parameters<typeof batch.mutateAsync>[0]['data']['operations'],
  ) => {
    await batch.mutateAsync({ data: { operations } });
    await refresh();
  };

  const bodyOf = (m: GroupMember, patch: Partial<GroupMemberInput> = {}): GroupMemberInput => ({
    enabled: m.enabled,
    valid_from: m.valid_from,
    expires_at: m.expires_at,
    ...(m.member_group_id
      ? { member_group_id: m.member_group_id }
      : { predicate: m.predicate ?? [] }),
    ...patch,
  });

  const readyToAdd =
    kind === 'person'
      ? !!person.trim()
      : kind === 'nested'
        ? !!nested
        : conds.length > 0 && conds.every(condReady);

  const add = async () => {
    setError(null);
    const body: GroupMemberInput = {
      enabled: true,
      expires_at: until ? endOfDayISO(until) : null,
      ...(kind === 'person'
        ? { predicate: personPredicate(person) }
        : kind === 'nested'
          ? { member_group_id: nested }
          : { predicate: conds.map(toPredicate) }),
    };
    const id = crypto.randomUUID();
    try {
      await memberOps([{ op: 'create', entity: 'group_member', parent: groupId, id, body }]);
      undoNotice({
        message: t('group.added'),
        undo: () => memberOps([{ op: 'delete', entity: 'group_member', parent: groupId, id }]),
      });
      setAdding(false);
      setPerson('');
      setNested(undefined);
      setConds([]);
      setUntil(undefined);
    } catch (e) {
      setError(e);
    }
  };

  const removeMember = async (m: GroupMember) => {
    try {
      await memberOps([{ op: 'delete', entity: 'group_member', parent: groupId, id: m.id }]);
      undoNotice({
        message: t('group.removed'),
        undo: () =>
          memberOps([
            { op: 'create', entity: 'group_member', parent: groupId, id: m.id, body: bodyOf(m) },
          ]),
      });
    } catch (e) {
      Modal.error({ title: t('common.saveFailed'), content: errorDetail(e) });
    }
  };

  const toggle = async (m: GroupMember) => {
    try {
      await memberOps([
        {
          op: 'update',
          entity: 'group_member',
          parent: groupId,
          id: m.id,
          body: bodyOf(m, { enabled: !m.enabled }),
        },
      ]);
    } catch (e) {
      Modal.error({ title: t('common.saveFailed'), content: errorDetail(e) });
    }
  };

  const rename = async (name: string) => {
    if (!name.trim() || name.trim() === group.name) return;
    try {
      await update.mutateAsync({ groupId, data: { name: name.trim() } });
      await refresh();
    } catch (e) {
      Modal.error({ title: t('common.saveFailed'), content: errorDetail(e) });
    }
  };

  const askDelete = () =>
    Modal.confirm({
      title: t('group.delete.title', { name: group.name }),
      content: t('group.delete.body'),
      okText: t('common.delete'),
      okButtonProps: { danger: true },
      cancelText: t('common.cancel'),
      onOk: async () => {
        try {
          await remove.mutateAsync({ groupId });
          await refresh();
          router.push('/people');
        } catch (e) {
          const extra = errorExtra(e);
          Modal.error({
            title: t('group.delete.blocked'),
            content:
              extra?.type === 'blocked_by'
                ? t('group.delete.blockedBy', {
                    what: (extra.blocked_by ?? [])
                      .map((k) => t(`history.entity.${k}`, { defaultValue: k }))
                      .join(', '),
                  })
                : errorDetail(e),
          });
          throw e;
        }
      },
    });

  const list = members.data ?? [];

  return (
    <div className="page">
      <PageHeader
        crumbs={[{ label: t('nav.people'), href: '/people' }, { label: group.name }]}
        title={
          canEditGroup ? (
            <EditableTitle
              value={group.name}
              label={t('group.rename')}
              onSave={(v) => void rename(v)}
            />
          ) : (
            group.name
          )
        }
        subtitle={t('group.subtitle')}
        extra={
          <>
            {access.whole('assignment_rule', 'write') && (
              <Button
                type="primary"
                icon={<Plus size={16} aria-hidden />}
                onClick={() => grant.open({ who: { kind: 'group', id: groupId } })}
              >
                {t('grant.open')}
              </Button>
            )}
            {canEditGroup && (
              <Dropdown
                trigger={['click']}
                menu={{
                  items: [{ key: 'delete', danger: true, label: t('group.delete.action') }],
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
          { key: 'composition', label: t('group.tab.composition', { count: list.length }) },
          { key: 'access', label: t('group.tab.access', { count: own.length }) },
          { key: 'nested', label: t('group.tab.nested', { count: nestedIn.length }) },
        ]}
      />

      {tab === 'composition' && (
        <>
          {canWrite && (
            <Flex justify="flex-end" style={{ marginBottom: 12 }}>
              <Button
                type={adding ? 'default' : 'primary'}
                icon={adding ? undefined : <Plus size={16} aria-hidden />}
                onClick={() => setAdding((v) => !v)}
              >
                {adding ? t('group.hideForm') : t('group.addMember')}
              </Button>
            </Flex>
          )}
          {adding && (
            <div
              style={{
                background: 'var(--licetum-surface)',
                border: '1px solid var(--licetum-border)',
                borderRadius: 8,
                padding: 16,
                marginBottom: 16,
              }}
            >
              <Flex vertical gap={12}>
                {error != null && (
                  <Alert
                    type="error"
                    showIcon
                    title={errorDetail(error) ?? t('common.saveFailed')}
                  />
                )}
                <Segmented
                  aria-label={t('group.kindLabel')}
                  value={kind}
                  onChange={(v) => setKind(v as Kind)}
                  options={[
                    { label: t('group.kind.person'), value: 'person' },
                    { label: t('group.kind.nested'), value: 'nested' },
                    { label: t('group.kind.condition'), value: 'condition' },
                  ]}
                />
                {kind === 'person' && (
                  <Input
                    aria-label={t('group.kind.person')}
                    placeholder={t('group.personPlaceholder')}
                    value={person}
                    onChange={(e) => setPerson(e.target.value)}
                    className="mono"
                  />
                )}
                {kind === 'nested' && (
                  <Select
                    showSearch
                    aria-label={t('group.kind.nested')}
                    placeholder={t('group.nestedPlaceholder')}
                    value={nested}
                    onChange={setNested}
                    optionFilterProp="label"
                    options={(groups.data ?? [])
                      .filter((g) => g.id !== groupId)
                      .map((g) => ({ value: g.id, label: g.name }))}
                  />
                )}
                {kind === 'condition' && (
                  <>
                    <CondEditor
                      value={conds}
                      onChange={setConds}
                      addLabel={t('cond.add')}
                      label={t('group.kind.condition')}
                    />
                    <Typography.Text type="secondary" style={{ fontSize: 12 }}>
                      {t('group.conditionHint')}
                    </Typography.Text>
                  </>
                )}
                <DatePicker
                  aria-label={t('grant.when.until')}
                  placeholder={t('grant.when.until')}
                  value={until ? dayjs(until) : null}
                  onChange={(d) => setUntil(d ? d.format('YYYY-MM-DD') : undefined)}
                  style={{ maxWidth: 220 }}
                />
                <Flex gap={8} justify="flex-end">
                  <Button onClick={() => setAdding(false)}>{t('common.cancel')}</Button>
                  <Button
                    type="primary"
                    disabled={!readyToAdd}
                    loading={batch.isPending}
                    onClick={() => void add()}
                  >
                    {t('group.add')}
                  </Button>
                </Flex>
              </Flex>
            </div>
          )}

          {members.isPending ? (
            <LoadingBlock rows={3} />
          ) : members.isError ? (
            <ErrorBlock error={members.error} onRetry={() => void members.refetch()} />
          ) : list.length === 0 ? (
            <EmptyBlock title={t('group.empty.title')} hint={t('group.empty.hint')} />
          ) : (
            <Table<GroupMember>
              rowKey="id"
              size="middle"
              pagination={false}
              dataSource={list}
              columns={[
                {
                  title: t('group.col.who'),
                  key: 'who',
                  render: (_, m) => {
                    const v = describeMember(m, groupNames.name, t);
                    return (
                      <span>
                        {v.kind === 'person' ? (
                          <Link
                            href={`/people/${encodeURIComponent(v.username ?? '')}`}
                            className="mono"
                          >
                            @{v.label}
                          </Link>
                        ) : v.kind === 'nested' && v.groupId ? (
                          <Link href={`/people/groups/${v.groupId}`}>{v.label}</Link>
                        ) : (
                          <span className="mono">{v.label}</span>
                        )}{' '}
                        <span style={{ color: 'var(--licetum-muted)', fontSize: 12 }}>
                          ({t(`group.kind.${v.kind}Short`)})
                        </span>
                      </span>
                    );
                  },
                },
                {
                  title: t('group.col.term'),
                  key: 'term',
                  width: 190,
                  render: (_, m) =>
                    m.expires_at
                      ? t('rule.until', { date: formatDate(m.expires_at, lang) })
                      : t('grantDetail.forever'),
                },
                {
                  title: t('group.col.status'),
                  key: 'status',
                  width: 160,
                  render: (_, m) =>
                    m.expires_at && Date.parse(m.expires_at) <= Date.now() ? (
                      <Tag>{t('rule.status.expired')}</Tag>
                    ) : (
                      <Switch
                        checked={m.enabled}
                        disabled={!canWrite || batch.isPending}
                        onChange={() => void toggle(m)}
                        aria-label={t('rule.status.active')}
                      />
                    ),
                },
                {
                  title: '',
                  key: 'actions',
                  width: 120,
                  render: (_, m) =>
                    canWrite ? (
                      <Button
                        size="small"
                        danger
                        disabled={batch.isPending}
                        onClick={() => void removeMember(m)}
                      >
                        {t('group.remove')}
                      </Button>
                    ) : null,
                },
              ]}
            />
          )}
          <Typography.Paragraph type="secondary" style={{ fontSize: 12, marginTop: 12 }}>
            {t('group.nestedNote')}
          </Typography.Paragraph>
        </>
      )}

      {tab === 'access' && (
        <>
          {own.length === 0 ? (
            <EmptyBlock
              title={t('group.noAccess.title')}
              hint={t('group.noAccess.hint')}
              action={
                access.whole('assignment_rule', 'write') ? (
                  <Button
                    type="primary"
                    onClick={() => grant.open({ who: { kind: 'group', id: groupId } })}
                  >
                    {t('grant.open')}
                  </Button>
                ) : undefined
              }
            />
          ) : (
            <Table
              rowKey="id"
              size="middle"
              pagination={false}
              dataSource={own}
              onRow={(r) => ({
                onClick: () => router.push(`/access/${r.id}`),
                style: { cursor: 'pointer' },
              })}
              columns={[
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
                  title: '',
                  key: 'open',
                  width: 100,
                  render: (_, r) => (
                    <Link href={`/access/${r.id}`} onClick={(e) => e.stopPropagation()}>
                      {t('common.open')}
                    </Link>
                  ),
                },
              ]}
            />
          )}
          <Typography.Paragraph style={{ marginTop: 12 }}>
            <Link href={`/check?mode=list`}>{t('group.checkMember')}</Link>
          </Typography.Paragraph>
        </>
      )}

      {tab === 'nested' && (
        <div
          style={{
            background: 'var(--licetum-surface)',
            border: '1px solid var(--licetum-border)',
            borderRadius: 8,
            padding: 16,
          }}
        >
          {nestedIn.length === 0 ? (
            <Typography.Text>{t('group.nestedNone')}</Typography.Text>
          ) : (
            <Flex vertical gap={6}>
              <Typography.Text>{t('group.nestedIn')}</Typography.Text>
              {nestedIn.map((id) => (
                <Link key={id} href={`/people/groups/${id}`}>
                  {groupNames.name(id) ?? id}
                </Link>
              ))}
            </Flex>
          )}
        </div>
      )}
    </div>
  );
}
