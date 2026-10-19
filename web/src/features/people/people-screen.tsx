'use client';

import { Alert, Button, Flex, Input, Modal, Select, Table, Tabs, Tag, Typography } from 'antd';
import { Plus, Search } from 'lucide-react';
import Link from 'next/link';
import { useRouter } from 'next/navigation';
import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { useCreateGroup } from '@/api/admin/group/group';
import { useListDirectory } from '@/api/admin/subject/subject';
import { EmptyBlock, errorDetail, ErrorBlock, LoadingBlock, PageHeader } from '@/components/kit';
import { usePreferences } from '@/components/providers';
import { formatDateTime } from '@/domain/format';
import { describeMember, personOf } from '@/domain/members';
import { useAccess } from '@/lib/access';
import { useQueryState } from '@/lib/use-query-state';

import { AccessTabs } from '../access/access-tabs';
import {
  useAllMembers,
  useChangeLog,
  useGroupNames,
  useGroups,
  useRefreshAll,
  useRules,
  useServices,
} from '../data/queries';

/**
 * The people the policy names (in rules, memberships and the history of changes) and the people the application of the chosen
 * service sent to its directory, with their names. A person of the directory who no rule names is listed too.
 */
function useKnownPeople(serviceId: string | undefined) {
  const access = useAccess();
  const directory = useListDirectory(
    serviceId ?? '',
    { limit: 200 },
    {
      query: {
        enabled: !!serviceId && access.whole('group_member', 'read'),
        select: (r) => (r.status === 200 ? r.data : null),
      },
    },
  );
  const rules = useRules();
  const groups = useGroups();
  const members = useAllMembers((groups.data ?? []).map((g) => g.id));
  const log = useChangeLog({ entity: 'assignment_rule' });

  const list = useMemo(() => {
    const map = new Map<
      string,
      { username: string; name?: string; lastSeen?: string; groups: Set<string>; rules: number }
    >();
    const touch = (username: string) => {
      const found = map.get(username) ?? { username, groups: new Set<string>(), rules: 0 };
      map.set(username, found);
      return found;
    };
    for (const r of rules.data ?? []) {
      if (r.subject && !r.subject.startsWith('group:')) touch(r.subject).rules++;
    }
    for (const [groupId, list] of members.byGroup) {
      for (const m of list) {
        const p = personOf(m.predicate);
        if (p) touch(p).groups.add(groupId);
      }
    }
    for (const rec of log.data ?? []) {
      if (rec.actor) {
        const person = touch(rec.actor);
        if (!person.lastSeen || person.lastSeen < rec.at) person.lastSeen = rec.at;
      }
    }
    // The directory has everyone the application sent, not only those the rules name.
    for (const s of directory.data?.items ?? []) {
      const person = touch(s.id);
      if (s.name) person.name = s.name;
      if (!person.lastSeen || person.lastSeen < s.sent_at) person.lastSeen = s.sent_at;
    }
    return [...map.values()].sort((a, b) => a.username.localeCompare(b.username));
  }, [rules.data, members.byGroup, log.data, directory.data]);
  return { list, directoryEmpty: directory.isSuccess && (directory.data?.total ?? 0) === 0 };
}

export function PeopleScreen() {
  const { t } = useTranslation();
  const { lang } = usePreferences();
  const router = useRouter();
  const access = useAccess();
  const groups = useGroups();
  const groupNames = useGroupNames();
  const rules = useRules();
  const members = useAllMembers((groups.data ?? []).map((g) => g.id));
  const services = useServices();
  const [serviceSlug, setServiceSlug] = useQueryState('service');
  // The people of a service are the ones its application sent: the first service of the installation is the default.
  const clientServices = (services.data ?? []).filter((s) => s.slug !== 'licetum');
  const service = clientServices.find((s) => s.slug === serviceSlug) ?? clientServices[0];
  const { list: people, directoryEmpty } = useKnownPeople(service?.id);
  const create = useCreateGroup();
  const refresh = useRefreshAll();

  const [tab, setTab] = useQueryState('tab', 'groups');
  const [search, setSearch] = useQueryState('q');
  const [creating, setCreating] = useState(false);
  const [name, setName] = useState('');
  const [error, setError] = useState<unknown>(null);

  const canCreate = access.whole('group', 'write');
  const q = search.trim().toLowerCase();

  const rows = useMemo(() => {
    return (groups.data ?? [])
      .map((g) => {
        const list = members.byGroup.get(g.id) ?? [];
        const grants = (rules.data ?? []).filter(
          (r) => r.member_of_group_id === g.id || r.subject === `group:${g.id}`,
        ).length;
        const nestedIn = [...members.byGroup.entries()]
          .filter(([, ms]) => ms.some((m) => m.member_group_id === g.id))
          .map(([id]) => id);
        const composition = list.map((m) => describeMember(m, groupNames.name, t));
        return { group: g, members: list, grants, nestedIn, composition };
      })
      .filter(
        (row) =>
          !q ||
          `${row.group.name} ${row.composition.map((c) => c.label).join(' ')}`
            .toLowerCase()
            .includes(q),
      )
      .sort((a, b) => a.group.name.localeCompare(b.group.name));
  }, [groups.data, members.byGroup, rules.data, groupNames, q, t]);

  const submit = async () => {
    setError(null);
    try {
      const res = await create.mutateAsync({ data: { name: name.trim() } });
      await refresh();
      setCreating(false);
      setName('');
      router.push(`/people/groups/${(res.data as { id: string }).id}`);
    } catch (e) {
      setError(e);
    }
  };

  const filteredPeople = people.filter(
    (p) => !q || `${p.username} ${p.name ?? ''}`.toLowerCase().includes(q),
  );

  return (
    <div className="page">
      <PageHeader
        title={t('nav.people')}
        extra={
          canCreate && (
            <Button
              type="primary"
              icon={<Plus size={16} aria-hidden />}
              onClick={() => setCreating(true)}
            >
              {t('people.create')}
            </Button>
          )
        }
      />
      <AccessTabs />

      <Tabs
        activeKey={tab}
        onChange={setTab}
        items={[
          { key: 'groups', label: t('people.tab.groups') },
          { key: 'people', label: t('people.tab.people') },
        ]}
      />

      <Input
        allowClear
        prefix={<Search size={14} aria-hidden />}
        aria-label={t('people.search')}
        placeholder={tab === 'groups' ? t('people.search') : t('people.searchPeople')}
        value={search}
        onChange={(e) => setSearch(e.target.value)}
        style={{ width: 360, maxWidth: '100%', marginBottom: 12 }}
      />

      {groups.isPending ? (
        <LoadingBlock rows={5} />
      ) : groups.isError ? (
        <ErrorBlock error={groups.error} onRetry={() => void groups.refetch()} />
      ) : tab === 'groups' ? (
        (groups.data ?? []).length === 0 ? (
          <EmptyBlock
            title={t('people.empty.title')}
            hint={t('people.empty.hint')}
            action={
              canCreate ? (
                <Button type="primary" onClick={() => setCreating(true)}>
                  {t('people.create')}
                </Button>
              ) : undefined
            }
          />
        ) : (
          <Table
            rowKey={(r) => r.group.id}
            size="middle"
            pagination={false}
            dataSource={rows}
            onRow={(r) => ({
              onClick: () => router.push(`/people/groups/${r.group.id}`),
              style: { cursor: 'pointer' },
            })}
            locale={{ emptyText: <EmptyBlock title={t('people.noMatch')} /> }}
            columns={[
              {
                title: t('people.col.group'),
                key: 'name',
                render: (_, r) => (
                  <span>
                    <Link
                      href={`/people/groups/${r.group.id}`}
                      onClick={(e) => e.stopPropagation()}
                      style={{ fontWeight: 500 }}
                    >
                      {r.group.name}
                    </Link>{' '}
                    {r.members.length === 0 && <Tag color="warning">{t('people.empty.tag')}</Tag>}
                  </span>
                ),
              },
              {
                title: t('people.col.composition'),
                key: 'composition',
                render: (_, r) =>
                  r.composition.length === 0
                    ? '—'
                    : r.composition
                        .slice(0, 3)
                        .map((c) => c.label)
                        .join(' · ') +
                      (r.composition.length > 3 ? ` +${r.composition.length - 3}` : ''),
              },
              {
                title: t('people.col.grants'),
                key: 'grants',
                width: 110,
                render: (_, r) => r.grants,
              },
              {
                title: t('people.col.nestedIn'),
                key: 'nested',
                width: 200,
                render: (_, r) =>
                  r.nestedIn.length
                    ? r.nestedIn.map((id) => groupNames.name(id) ?? '').join(', ')
                    : '—',
              },
            ]}
          />
        )
      ) : (
        <>
          {clientServices.length > 0 && (
            <Flex align="center" gap={8} style={{ marginBottom: 12 }}>
              <Typography.Text type="secondary">{t('people.directoryOf')}</Typography.Text>
              <Select
                aria-label={t('people.directoryOf')}
                value={service?.slug}
                onChange={setServiceSlug}
                options={clientServices.map((s) => ({ value: s.slug, label: s.name }))}
                style={{ minWidth: 240 }}
                showSearch={{ optionFilterProp: 'label' }}
              />
            </Flex>
          )}
          <Table
            rowKey="username"
            size="middle"
            pagination={false}
            dataSource={filteredPeople}
            onRow={(r) => ({
              onClick: () =>
                router.push(
                  `/people/${encodeURIComponent(r.username)}${service ? `?service=${service.slug}` : ''}`,
                ),
              style: { cursor: 'pointer' },
            })}
            locale={{
              emptyText: (
                <EmptyBlock title={t('people.noPeople')} hint={t('people.noPeopleHint')} />
              ),
            }}
            columns={[
              {
                title: t('people.col.person'),
                key: 'username',
                render: (_, r) => (
                  <div>
                    {r.name && <div style={{ fontWeight: 500 }}>{r.name}</div>}
                    <Link
                      href={`/people/${encodeURIComponent(r.username)}${service ? `?service=${service.slug}` : ''}`}
                      className="mono"
                      onClick={(e) => e.stopPropagation()}
                    >
                      @{r.username}
                    </Link>
                  </div>
                ),
              },
              {
                title: t('people.col.groups'),
                key: 'groups',
                width: 140,
                render: (_, r) => r.groups.size,
              },
              { title: t('people.col.rules'), key: 'rules', width: 140, render: (_, r) => r.rules },
              {
                title: t('people.col.lastSeen'),
                key: 'seen',
                width: 180,
                render: (_, r) => formatDateTime(r.lastSeen, lang),
              },
            ]}
          />
          <Flex style={{ marginTop: 12 }} vertical gap={8}>
            {directoryEmpty && <Alert type="info" showIcon title={t('people.directoryEmpty')} />}
            <Typography.Text type="secondary" style={{ fontSize: 12 }}>
              {t('people.findHint')}
            </Typography.Text>
          </Flex>
        </>
      )}

      <Modal
        open={creating}
        title={t('people.create')}
        onCancel={() => setCreating(false)}
        okText={t('common.create')}
        cancelText={t('common.cancel')}
        okButtonProps={{ disabled: !name.trim(), loading: create.isPending }}
        onOk={() => void submit()}
        destroyOnHidden
      >
        <Flex vertical gap={8}>
          {error != null && (
            <Alert type="error" showIcon title={errorDetail(error) ?? t('common.saveFailed')} />
          )}
          <Input
            autoFocus
            aria-label={t('people.nameLabel')}
            placeholder={t('people.namePlaceholder')}
            value={name}
            onChange={(e) => setName(e.target.value)}
            onPressEnter={() => name.trim() && void submit()}
            className="mono"
          />
          <Typography.Text type="secondary" style={{ fontSize: 13 }}>
            {t('people.nameHelp')}
          </Typography.Text>
        </Flex>
      </Modal>
    </div>
  );
}
