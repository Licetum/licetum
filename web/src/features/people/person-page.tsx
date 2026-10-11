'use client';

import { useQuery } from '@tanstack/react-query';
import {
  Alert,
  Avatar,
  Button,
  Collapse,
  Flex,
  Input,
  Modal,
  Select,
  Table,
  Tabs,
  Tag,
  Typography,
} from 'antd';
import Link from 'next/link';
import { useRouter } from 'next/navigation';
import { useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { useRunBatch } from '@/api/admin/batch/batch';
import type { SimulatePermission200 } from '@/api/admin/model';
import { simulatePermission } from '@/api/admin/simulation/simulation';
import { useGetSubject } from '@/api/admin/subject/subject';
import { EmptyBlock, errorDetail, ErrorBlock, LoadingBlock, PageHeader } from '@/components/kit';
import { usePreferences } from '@/components/providers';
import { formatDateTime } from '@/domain/format';
import { personOf } from '@/domain/members';
import { ruleToInput } from '@/domain/rule';
import { useAccess } from '@/lib/access';
import { useUndoNotice } from '@/lib/undo';
import { useQueryState } from '@/lib/use-query-state';

import { WhatCell, WhereCell } from '../access/rule-cells';
import {
  useAllMembers,
  useGroupNames,
  useGroups,
  useRefreshAll,
  useRules,
  useServices,
} from '../data/queries';
import { useGrant } from '../grant/grant-context';

type Pair = { id: number; key: string; value: string };
let seq = 0;

function parseValue(value: string): string | string[] {
  const v = value.trim();
  if (v.startsWith('[') && v.endsWith(']')) {
    return v
      .slice(1, -1)
      .split(',')
      .map((x) => x.trim().replace(/^"|"$/g, ''))
      .filter(Boolean);
  }
  return v;
}

export function PersonPage({ username }: { username: string }) {
  const { t } = useTranslation();
  const { lang } = usePreferences();
  const router = useRouter();
  const access = useAccess();
  const grant = useGrant();
  const services = useServices();
  const groups = useGroups();
  const groupNames = useGroupNames();
  const rules = useRules();
  const allMembers = useAllMembers((groups.data ?? []).map((g) => g.id));
  const batch = useRunBatch();
  const refresh = useRefreshAll();
  const undoNotice = useUndoNotice();

  // What the registry knows of the person: the name, the attributes the policy reads, the groups they are in.
  const known = useGetSubject(username, {
    query: {
      enabled: access.whole('group_member', 'read'),
      retry: false,
      select: (r) => (r.status === 200 ? r.data : null),
    },
  });

  const [tab, setTab] = useQueryState('tab', 'effective');
  const [serviceSlug, setServiceSlug] = useQueryState('service');
  const [attrs, setAttrs] = useState<Pair[]>([]);
  const [revoking, setRevoking] = useState(false);

  // The attributes the person's tokens carried start the "what if" form, so that the effective access is the real one.
  const [seeded, setSeeded] = useState(false);
  useEffect(() => {
    const claimsOfPerson = known.data?.claims;
    if (seeded || !claimsOfPerson) return;
    setSeeded(true);
    const pairs = Object.entries(claimsOfPerson).map(([key, value]) => ({
      id: ++seq,
      key,
      value: Array.isArray(value) ? `[${value.join(', ')}]` : String(value),
    }));
    if (pairs.length > 0) setAttrs(pairs);
  }, [known.data, seeded]);

  const clientServices = (services.data ?? []).filter((s) => s.slug !== 'licetum');
  const named = clientServices.find((s) =>
    (rules.data ?? []).some((r) => r.service_id === s.id && r.subject === username),
  );
  const service = clientServices.find((s) => s.slug === serviceSlug) ?? named ?? clientServices[0];

  const claims = useMemo(
    () => ({
      preferred_username: username,
      ...Object.fromEntries(
        attrs.filter((a) => a.key.trim()).map((a) => [a.key.trim(), parseValue(a.value)]),
      ),
    }),
    [attrs, username],
  );
  const effective = useQuery({
    queryKey: ['person-effective', service?.id, username, claims],
    enabled: !!service && tab === 'effective',
    queryFn: async () => {
      const res = await simulatePermission(service!.id, { subject: username, claims, limit: null });
      return res.status === 200 ? (res.data as SimulatePermission200) : {};
    },
  });

  const direct = useMemo(
    () => (rules.data ?? []).filter((r) => r.subject === username),
    [rules.data, username],
  );
  const memberships = useMemo(() => {
    const out: { groupId: string; memberId: string }[] = [];
    for (const [groupId, list] of allMembers.byGroup) {
      for (const m of list)
        if (personOf(m.predicate) === username) out.push({ groupId, memberId: m.id });
    }
    return out;
  }, [allMembers.byGroup, username]);

  const initials = username.slice(0, 2).toUpperCase();
  const canRevoke = access.whole('assignment_rule', 'write');

  const revokeAll = async () => {
    setRevoking(false);
    const ops = [
      ...direct.map((r) => ({
        op: 'delete' as const,
        entity: 'assignment_rule' as const,
        id: r.id,
      })),
      ...memberships.map((m) => ({
        op: 'delete' as const,
        entity: 'group_member' as const,
        parent: m.groupId,
        id: m.memberId,
      })),
    ];
    if (ops.length === 0) return;
    try {
      await batch.mutateAsync({ data: { operations: ops } });
      await refresh();
      undoNotice({
        message: t('person.revoked', { count: ops.length }),
        undo: async () => {
          // Re-created from the rules and memberships read before the deletion.
          const back = [
            ...direct.map((r) => ({
              op: 'create' as const,
              entity: 'assignment_rule' as const,
              id: r.id,
              body: ruleToInput(r),
            })),
            ...memberships.map((m) => {
              const found = allMembers.byGroup.get(m.groupId)?.find((x) => x.id === m.memberId);
              return {
                op: 'create' as const,
                entity: 'group_member' as const,
                parent: m.groupId,
                id: m.memberId,
                body: {
                  enabled: found?.enabled ?? true,
                  predicate: found?.predicate ?? [],
                  valid_from: found?.valid_from,
                  expires_at: found?.expires_at,
                },
              };
            }),
          ];
          await batch.mutateAsync({ data: { operations: back } });
          await refresh();
        },
      });
    } catch (e) {
      Modal.error({ title: t('common.saveFailed'), content: errorDetail(e) });
    }
  };

  return (
    <div className="page">
      <PageHeader
        crumbs={[{ label: t('nav.people'), href: '/people?tab=people' }, { label: `@${username}` }]}
        title={
          <Flex align="center" gap={12}>
            <Avatar
              style={{
                background: 'var(--licetum-inherit-bg)',
                color: 'var(--licetum-accent)',
                fontWeight: 600,
              }}
            >
              {initials}
            </Avatar>
            <span>
              {known.data?.name && <span style={{ marginRight: 8 }}>{known.data.name}</span>}
              <span className="mono">@{username}</span>
            </span>
          </Flex>
        }
        subtitle={
          known.data ? (
            <span>
              {t('person.lastSeen', { at: formatDateTime(known.data.last_seen, lang) })}
              {known.data.groups.length > 0 && (
                <>
                  {' · '}
                  {t('person.inGroups')}{' '}
                  {known.data.groups.map((g, i) => (
                    <span key={g.id}>
                      {i > 0 && ', '}
                      <Link href={`/people/groups/${g.id}`}>{g.name}</Link>
                    </span>
                  ))}
                </>
              )}
            </span>
          ) : undefined
        }
        extra={
          <>
            {access.whole('assignment_rule', 'write') && (
              <Button
                type="primary"
                onClick={() =>
                  grant.open({ who: { kind: 'person', username }, serviceId: service?.id })
                }
              >
                {t('grant.open')}
              </Button>
            )}
            <Button
              href={`/check?subject=${encodeURIComponent(username)}${service ? `&service=${service.slug}` : ''}`}
            >
              {t('person.check')}
            </Button>
          </>
        }
      />

      <Collapse
        ghost
        size="small"
        style={{ marginBottom: 8 }}
        items={[
          {
            key: 'attrs',
            label: t('person.attrs', { count: attrs.length }),
            children: (
              <Flex vertical gap={8}>
                <Typography.Text type="secondary" style={{ fontSize: 12 }}>
                  {t('person.attrsHint')}
                </Typography.Text>
                {attrs.map((a) => (
                  <Flex key={a.id} gap={8}>
                    <Input
                      aria-label={t('cond.attr')}
                      className="mono"
                      style={{ width: 180 }}
                      value={a.key}
                      placeholder="department"
                      onChange={(e) =>
                        setAttrs((l) =>
                          l.map((x) => (x.id === a.id ? { ...x, key: e.target.value } : x)),
                        )
                      }
                    />
                    <Input
                      aria-label={t('cond.value')}
                      style={{ width: 240 }}
                      value={a.value}
                      placeholder="sales"
                      onChange={(e) =>
                        setAttrs((l) =>
                          l.map((x) => (x.id === a.id ? { ...x, value: e.target.value } : x)),
                        )
                      }
                    />
                    <Button
                      type="text"
                      onClick={() => setAttrs((l) => l.filter((x) => x.id !== a.id))}
                    >
                      {t('cond.remove')}
                    </Button>
                  </Flex>
                ))}
                <div>
                  <Button
                    size="small"
                    onClick={() => setAttrs((l) => [...l, { id: ++seq, key: '', value: '' }])}
                  >
                    {t('check.addAttr')}
                  </Button>
                </div>
              </Flex>
            ),
          },
        ]}
      />

      <Tabs
        activeKey={tab}
        onChange={setTab}
        items={[
          { key: 'effective', label: t('person.tab.effective') },
          { key: 'groups', label: t('person.tab.groups', { count: memberships.length }) },
          { key: 'direct', label: t('person.tab.direct', { count: direct.length }) },
        ]}
      />

      {tab === 'effective' && (
        <>
          <Flex gap={8} align="center" wrap style={{ marginBottom: 12 }}>
            <span>{t('person.service')}</span>
            <Select
              aria-label={t('person.service')}
              style={{ minWidth: 220 }}
              value={service?.slug}
              onChange={setServiceSlug}
              options={clientServices.map((s) => ({ value: s.slug, label: s.name }))}
              loading={services.isPending}
            />
          </Flex>
          {!service ? (
            <EmptyBlock title={t('person.noServices')} />
          ) : effective.isPending ? (
            <LoadingBlock rows={3} />
          ) : effective.isError ? (
            <ErrorBlock error={effective.error} onRetry={() => void effective.refetch()} />
          ) : Object.keys(effective.data ?? {}).length === 0 ? (
            <EmptyBlock
              title={t('person.nothing', { service: service.name })}
              hint={t('person.nothingHint')}
              action={
                access.whole('assignment_rule', 'write') ? (
                  <Button
                    type="primary"
                    onClick={() =>
                      grant.open({ who: { kind: 'person', username }, serviceId: service.id })
                    }
                  >
                    {t('grant.open')}
                  </Button>
                ) : undefined
              }
            />
          ) : (
            <Table
              rowKey={([name]) => name}
              size="middle"
              pagination={false}
              dataSource={Object.entries(effective.data ?? {})}
              expandable={{
                rowExpandable: ([, v]) => v.type === 'sync',
                expandedRowRender: ([name, v]) =>
                  v.type === 'sync' ? (
                    <Table
                      size="small"
                      rowKey="id"
                      pagination={{ pageSize: 15, showSizeChanger: false }}
                      dataSource={v.resources}
                      columns={[
                        {
                          title: t('check.resource'),
                          key: 'name',
                          render: (_, r) => (
                            <span>
                              {r.name}{' '}
                              <span className="mono" style={{ color: 'var(--licetum-muted)' }}>
                                {r.id}
                              </span>
                            </span>
                          ),
                        },
                        {
                          title: t('check.level'),
                          key: 'access',
                          width: 200,
                          render: (_, r) =>
                            (r.access ?? []).map((a) => (
                              <Tag key={a} className="mono">
                                {a}
                              </Tag>
                            )),
                        },
                        {
                          title: '',
                          key: 'why',
                          width: 110,
                          render: (_, r) => (
                            <Link
                              href={`/check?mode=one&service=${service.slug}&subject=${encodeURIComponent(username)}&type=${encodeURIComponent(name)}&resource=${encodeURIComponent(r.id)}`}
                            >
                              {t('check.why')} ›
                            </Link>
                          ),
                        },
                      ]}
                    />
                  ) : null,
              }}
              columns={[
                { title: t('check.type'), key: 'type', render: (_, [name]) => name },
                {
                  title: t('check.list.resources'),
                  key: 'count',
                  width: 200,
                  render: (_, [, v]) =>
                    v.type === 'sync'
                      ? `${v.resources.length}${v.next_after ? '+' : ''}`
                      : t('check.list.wholeType'),
                },
                {
                  title: t('check.level'),
                  key: 'levels',
                  render: (_, [, v]) =>
                    v.type === 'not_sync'
                      ? v.access.map((a) => (
                          <Tag key={a} className="mono">
                            {a}
                          </Tag>
                        ))
                      : null,
                },
              ]}
            />
          )}
          <Alert
            type="info"
            showIcon
            style={{ marginTop: 16 }}
            title={t('person.revokeHelpTitle')}
            description={t('person.revokeHelp')}
          />
        </>
      )}

      {tab === 'groups' && (
        <>
          {memberships.length === 0 ? (
            <EmptyBlock title={t('person.noGroups')} hint={t('person.noGroupsHint')} />
          ) : (
            <Table
              rowKey="memberId"
              size="middle"
              pagination={false}
              dataSource={memberships}
              columns={[
                {
                  title: t('people.col.group'),
                  key: 'g',
                  render: (_, m) => (
                    <Link href={`/people/groups/${m.groupId}`}>
                      {groupNames.name(m.groupId) ?? m.groupId}
                    </Link>
                  ),
                },
                { title: t('person.how'), key: 'how', render: () => t('person.howNamed') },
              ]}
            />
          )}
          <Typography.Paragraph type="secondary" style={{ fontSize: 12, marginTop: 12 }}>
            {t('person.groupsNote')}
          </Typography.Paragraph>
        </>
      )}

      {tab === 'direct' && (
        <>
          {direct.length === 0 ? (
            <EmptyBlock title={t('person.noDirect')} />
          ) : (
            <Table
              rowKey="id"
              size="middle"
              pagination={false}
              dataSource={direct}
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
              ]}
            />
          )}
          {canRevoke && (direct.length > 0 || memberships.length > 0) && (
            <Flex gap={8} style={{ marginTop: 16 }}>
              <Button danger loading={batch.isPending} onClick={() => setRevoking(true)}>
                {t('person.revokeAll')}
              </Button>
            </Flex>
          )}
        </>
      )}

      <Modal
        open={revoking}
        title={t('person.revokeTitle')}
        okText={t('person.revokeConfirm')}
        okButtonProps={{ danger: true }}
        cancelText={t('common.cancel')}
        onCancel={() => setRevoking(false)}
        onOk={() => void revokeAll()}
      >
        <Typography.Paragraph>
          {t('person.revokeBody', { direct: direct.length, groups: memberships.length })}
        </Typography.Paragraph>
        <Typography.Text type="secondary">{t('person.revokeHelp')}</Typography.Text>
      </Modal>
    </div>
  );
}
