'use client';

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
  Typography,
} from 'antd';
import Link from 'next/link';
import { useRouter } from 'next/navigation';
import { useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { useRunBatch } from '@/api/admin/batch/batch';
import { useGetDirectoryEntry } from '@/api/admin/subject/subject';
import { EmptyBlock, errorDetail, LoadingBlock, PageHeader } from '@/components/kit';
import { usePreferences } from '@/components/providers';
import { formatDateTime } from '@/domain/format';
import { personOf } from '@/domain/members';
import { ruleToInput } from '@/domain/rule';
import { useAccess } from '@/lib/access';
import { propertiesOf } from '@/lib/authzen';
import { useUndoNotice } from '@/lib/undo';
import { useQueryState } from '@/lib/use-query-state';

import { WhatCell, WhereCell } from '../access/rule-cells';
import { VisibleAccess } from '../check/visible-access';
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

  const [tab, setTab] = useQueryState('tab', 'effective');
  const [serviceSlug, setServiceSlug] = useQueryState('service');
  const [attrs, setAttrs] = useState<Pair[]>([]);
  // The attributes the questions are asked with: those typed, once they have rested (each change starts new requests).
  const [applied, setApplied] = useState<Pair[]>([]);
  const [revoking, setRevoking] = useState(false);

  const clientServices = (services.data ?? []).filter((s) => s.slug !== 'licetum');
  const named = clientServices.find((s) =>
    (rules.data ?? []).some((r) => r.service_id === s.id && r.subject === username),
  );
  const service = clientServices.find((s) => s.slug === serviceSlug) ?? named ?? clientServices[0];

  // What the directory of the service holds of the person: the name, the properties as the application sent them, the groups
  // they are in as of those properties. A person the application never sent is not there (404): the rules still name them.
  const known = useGetDirectoryEntry(service?.id ?? '', 'user', username, {
    query: {
      enabled: !!service && access.whole('group_member', 'read'),
      retry: false,
      select: (r) => (r.status === 200 ? r.data : null),
    },
  });

  // The properties the application sent for the person start the "what if" form, so that the effective access is the real one.
  const [seeded, setSeeded] = useState(false);
  useEffect(() => {
    const claimsOfPerson = known.data?.properties;
    if (seeded || !claimsOfPerson) return;
    setSeeded(true);
    const pairs = Object.entries(claimsOfPerson).map(([key, value]) => ({
      id: ++seq,
      key,
      value: Array.isArray(value) ? `[${value.join(', ')}]` : String(value),
    }));
    if (pairs.length > 0) {
      setAttrs(pairs);
      setApplied(pairs);
    }
  }, [known.data, seeded]);
  useEffect(() => {
    const timer = setTimeout(() => setApplied(attrs), 500);
    return () => clearTimeout(timer);
  }, [attrs]);

  // The properties of the person the simulation is asked with (the PDP takes the username as preferred_username by itself).
  const who = useMemo(
    () => ({ person: username, properties: propertiesOf(applied), context: {} }),
    [username, applied],
  );
  // The questions wait for the properties the application sent, so that the first ones are the real ones.
  const waiting = known.isLoading || (!!known.data?.properties && !seeded);

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
              {t('person.lastSent', { at: formatDateTime(known.data.sent_at, lang) })}
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
          ) : waiting ? (
            <LoadingBlock rows={3} />
          ) : (
            <VisibleAccess
              serviceId={service.id}
              serviceSlug={service.slug}
              who={who}
              emptyAction={
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
