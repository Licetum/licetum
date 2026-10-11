'use client';

import {
  Alert,
  Button,
  Flex,
  Input,
  Modal,
  Segmented,
  Select,
  Switch,
  Table,
  Tag,
  Typography,
} from 'antd';
import { Plus, Search } from 'lucide-react';
import Link from 'next/link';
import { useRouter } from 'next/navigation';
import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { useGetPlatform } from '@/api/admin/platform/platform';
import { EmptyBlock, ErrorBlock, LoadingBlock, PageHeader } from '@/components/kit';
import { usePreferences } from '@/components/providers';
import { describeWho, hasId, ruleDecision, ruleStatus, type Rule } from '@/domain/rule';
import { useAccess } from '@/lib/access';
import { useQueryList, useQueryState } from '@/lib/use-query-state';

import { useChangeLog, useGroupNames, useRules } from '../data/queries';
import { useGrant } from '../grant/grant-context';
import { AccessTabs } from './access-tabs';
import { useRuleActions } from './rule-actions';
import { ConditionsCell, StatusTag, WhatCell, WhereCell, WhoCell } from './rule-cells';

type View = 'list' | 'grantees' | 'resources';

/** Who gave a rule: the actor of the record that created it in the change log. */
function useAuthors() {
  const log = useChangeLog({ entity: 'assignment_rule' });
  return useMemo(() => {
    const map = new Map<string, string>();
    for (const record of log.data ?? []) {
      if (record.action === 'create' && record.entity_id && !map.has(record.entity_id))
        map.set(record.entity_id, record.actor);
    }
    return map;
  }, [log.data]);
}

export function AccessList() {
  const { t } = useTranslation();
  const { lang } = usePreferences();
  const router = useRouter();
  const access = useAccess();
  const grant = useGrant();
  const rules = useRules();
  const groups = useGroupNames();
  const authors = useAuthors();
  const actions = useRuleActions();

  const [view, setView] = useQueryState('view', 'list');
  const [search, setSearch] = useQueryState('q');
  const [who, setWho] = useQueryList('who');
  const [service, setService] = useQueryList('service');
  const [effect, setEffect] = useQueryList('effect');
  const [term, setTerm] = useQueryList('term');
  const [author, setAuthor] = useQueryList('author');
  const [system, setSystem] = useQueryState('system');
  const [selected, setSelected] = useState<string[]>([]);
  const platform = useGetPlatform({
    query: { select: (r) => (r.status === 200 ? r.data : undefined) },
  });
  const platformId = platform.data?.service_id;

  const canWrite = access.whole('assignment_rule', 'write');
  // The rules that let the administrators manage Licetum itself are hidden unless asked for.
  const showSystem = system === '1';
  const all = (rules.data ?? []).filter(
    (r) => showSystem || r.service_id !== platformId || hasId(r.role_id),
  );

  const kindOf = (r: Rule) => describeWho(r, groups.name, t).kind;
  const termOf = (r: Rule) => {
    const { status } = ruleStatus(r);
    return status === 'expiring' ? 'expiring' : r.expires_at ? 'dated' : 'forever';
  };

  const filtered = useMemo(() => {
    const q = search.trim().toLowerCase();
    return all.filter((r) => {
      if (who.length && !who.includes(kindOf(r))) return false;
      if (service.length && !service.includes(r.service_name ?? r.role_name ?? '')) return false;
      if (effect.length && !effect.includes(ruleDecision(r))) return false;
      if (term.length && !term.includes(termOf(r))) return false;
      if (author.length && !author.includes(authors.get(r.id) ?? '')) return false;
      if (!q) return true;
      const haystack = [
        r.name,
        r.description,
        describeWho(r, groups.name, t).label,
        r.role_name,
        r.service_name,
        r.type_resource_name,
        r.access?.join(' '),
        authors.get(r.id),
      ]
        .filter(Boolean)
        .join(' ')
        .toLowerCase();
      return haystack.includes(q);
    });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [all, search, who, service, effect, term, author, authors, groups.data]);

  const serviceNames = useMemo(
    () => [...new Set(all.map((r) => r.service_name ?? r.role_name ?? '').filter(Boolean))].sort(),
    [all],
  );
  const authorNames = useMemo(
    () => [...new Set([...authors.values()].filter(Boolean))].sort(),
    [authors],
  );
  const filtersOn = !!(
    search ||
    who.length ||
    service.length ||
    effect.length ||
    term.length ||
    author.length
  );
  const reset = () => {
    setSearch(null);
    setWho([]);
    setService([]);
    setEffect([]);
    setTerm([]);
    setAuthor([]);
  };

  const selectedRules = filtered.filter((r) => selected.includes(r.id));

  const confirmDelete = (list: Rule[]) =>
    Modal.confirm({
      title: t('rule.deleteTitle', { count: list.length }),
      content: t('rule.deleteBody'),
      okText: t('common.delete'),
      okButtonProps: { danger: true },
      cancelText: t('common.cancel'),
      onOk: async () => {
        if (await actions.remove(list)) setSelected([]);
      },
    });

  return (
    <div className="page">
      <PageHeader
        title={t('nav.access')}
        extra={
          canWrite && (
            <Button
              type="primary"
              icon={<Plus size={16} aria-hidden />}
              onClick={() => grant.open()}
            >
              {t('grant.open')}
            </Button>
          )
        }
      />
      <AccessTabs />

      <Flex gap={12} wrap align="center" style={{ marginBottom: 12 }}>
        <Segmented
          aria-label={t('access.viewLabel')}
          value={view}
          onChange={(v) => setView(v as View)}
          options={[
            { label: t('access.view.list'), value: 'list' },
            { label: t('access.view.grantees'), value: 'grantees' },
            { label: t('access.view.resources'), value: 'resources' },
          ]}
        />
        <span style={{ flex: 1 }} />
        <Input
          allowClear
          prefix={<Search size={14} aria-hidden />}
          aria-label={t('access.search')}
          placeholder={t('access.search')}
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          style={{ width: 320, maxWidth: '100%' }}
        />
      </Flex>

      {view === 'list' && (
        <Flex
          gap={8}
          wrap
          align="center"
          style={{ marginBottom: 12 }}
          role="group"
          aria-label={t('access.filters')}
        >
          <FilterSelect
            label={t('access.filter.who')}
            value={who}
            onChange={setWho}
            options={['group', 'person', 'condition', 'everyone'].map((k) => ({
              value: k,
              label: t(`rule.kind.${k}`),
            }))}
          />
          <FilterSelect
            label={t('access.filter.where')}
            value={service}
            onChange={setService}
            options={serviceNames.map((n) => ({ value: n, label: n }))}
          />
          <FilterSelect
            label={t('access.filter.term')}
            value={term}
            onChange={setTerm}
            options={['expiring', 'dated', 'forever'].map((k) => ({
              value: k,
              label: t(`access.term.${k}`),
            }))}
          />
          <FilterSelect
            label={t('access.filter.effect')}
            value={effect}
            onChange={setEffect}
            options={['allow', 'deny', 'except'].map((k) => ({
              value: k,
              label: t(`access.effect.${k}`),
            }))}
          />
          <FilterSelect
            label={t('access.filter.author')}
            value={author}
            onChange={setAuthor}
            options={authorNames.map((n) => ({ value: n, label: n }))}
          />
          {filtersOn && (
            <Button type="link" onClick={reset}>
              {t('common.resetFilters')}
            </Button>
          )}
          <span style={{ flex: 1 }} />
          <Flex align="center" gap={8}>
            <Switch
              size="small"
              checked={showSystem}
              onChange={(v) => setSystem(v ? '1' : null)}
              aria-label={t('access.showSystem')}
            />
            <span style={{ fontSize: 13, color: 'var(--licetum-muted)' }}>
              {t('access.showSystem')}
            </span>
          </Flex>
        </Flex>
      )}

      {rules.isPending ? (
        <LoadingBlock rows={6} />
      ) : rules.isError ? (
        <ErrorBlock error={rules.error} onRetry={() => void rules.refetch()} />
      ) : all.length === 0 ? (
        <EmptyBlock
          title={t('access.empty.title')}
          hint={t('access.empty.hint')}
          action={
            canWrite ? (
              <Button type="primary" onClick={() => grant.open()}>
                {t('grant.open')}
              </Button>
            ) : undefined
          }
        />
      ) : view === 'grantees' ? (
        <Grantees rules={filtered} />
      ) : view === 'resources' ? (
        <ByResources rules={filtered} />
      ) : (
        <>
          <Typography.Text
            type="secondary"
            style={{ display: 'block', marginBottom: 8, fontSize: 12 }}
          >
            {t('access.shown', { shown: filtered.length, total: all.length })}
          </Typography.Text>
          <Table<Rule>
            rowKey="id"
            size="middle"
            dataSource={filtered}
            pagination={filtered.length > 50 ? { pageSize: 50, showSizeChanger: false } : false}
            scroll={{ x: 980 }}
            rowSelection={
              canWrite
                ? { selectedRowKeys: selected, onChange: (keys) => setSelected(keys as string[]) }
                : undefined
            }
            onRow={(r) => ({
              onClick: () => router.push(`/access/${r.id}`),
              style: { cursor: 'pointer' },
            })}
            locale={{
              emptyText: (
                <EmptyBlock
                  title={t('access.noMatch.title')}
                  hint={t('access.noMatch.hint')}
                  action={<Button onClick={reset}>{t('common.resetFilters')}</Button>}
                />
              ),
            }}
            columns={[
              {
                title: t('access.col.who'),
                key: 'who',
                width: 200,
                render: (_, r) => <WhoCell rule={r} />,
              },
              {
                title: t('access.col.what'),
                key: 'what',
                width: 230,
                render: (_, r) => (
                  <div>
                    <WhatCell rule={r} />
                    {r.name && (
                      <div style={{ color: 'var(--licetum-muted)', fontSize: 12, marginTop: 2 }}>
                        {r.name}
                      </div>
                    )}
                  </div>
                ),
              },
              {
                title: t('access.col.where'),
                key: 'where',
                render: (_, r) => <WhereCell rule={r} />,
              },
              {
                title: t('access.col.conditions'),
                key: 'cond',
                width: 190,
                render: (_, r) => <ConditionsCell rule={r} lang={lang} />,
              },
              {
                title: t('access.col.status'),
                key: 'status',
                width: 150,
                render: (_, r) => <StatusTag rule={r} />,
              },
              {
                title: t('access.col.author'),
                key: 'author',
                width: 100,
                render: (_, r) => authors.get(r.id) ?? '—',
              },
              {
                title: '',
                key: 'open',
                width: 90,
                render: (_, r) => (
                  <Link href={`/access/${r.id}`} onClick={(e) => e.stopPropagation()}>
                    {t('common.open')}
                  </Link>
                ),
              },
            ]}
          />
          {canWrite && selectedRules.length > 0 && (
            <Flex
              gap={8}
              align="center"
              wrap
              style={{
                position: 'sticky',
                bottom: 0,
                background: 'var(--licetum-inherit-bg)',
                border: '1px solid var(--licetum-border)',
                borderRadius: 8,
                padding: '10px 14px',
                marginTop: 12,
              }}
            >
              <span>{t('access.selected', { count: selectedRules.length })}</span>
              <span style={{ flex: 1 }} />
              <Button
                disabled={actions.busy}
                onClick={() => void actions.setEnabled(selectedRules, true)}
              >
                {t('rule.enable')}
              </Button>
              <Button
                disabled={actions.busy}
                onClick={() => void actions.setEnabled(selectedRules, false)}
              >
                {t('rule.disable')}
              </Button>
              <Button danger disabled={actions.busy} onClick={() => confirmDelete(selectedRules)}>
                {t('common.delete')}
              </Button>
            </Flex>
          )}
        </>
      )}
    </div>
  );
}

function FilterSelect(props: {
  label: string;
  value: string[];
  onChange: (v: string[]) => void;
  options: { value: string; label: string }[];
}) {
  return (
    <Select
      mode="multiple"
      allowClear
      maxTagCount="responsive"
      aria-label={props.label}
      placeholder={props.label}
      value={props.value}
      onChange={props.onChange}
      options={props.options}
      style={{ minWidth: 150 }}
      popupMatchSelectWidth={false}
    />
  );
}

/** "By people and groups": what each recipient gets, rule by rule. */
function Grantees({ rules }: { rules: Rule[] }) {
  const { t } = useTranslation();
  const groups = useGroupNames();
  const grouped = useMemo(() => {
    const map = new Map<string, { who: ReturnType<typeof describeWho>; rules: Rule[] }>();
    for (const r of rules) {
      const who = describeWho(r, groups.name, t);
      const key = `${who.kind}:${who.groupId ?? who.username ?? who.label}`;
      const entry = map.get(key) ?? { who, rules: [] };
      entry.rules.push(r);
      map.set(key, entry);
    }
    return [...map.values()].sort((a, b) => a.who.label.localeCompare(b.who.label));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [rules, groups.data]);

  return (
    <Table
      rowKey={(x) => `${x.who.kind}:${x.who.groupId ?? x.who.username ?? x.who.label}`}
      size="middle"
      pagination={false}
      dataSource={grouped}
      expandable={{
        expandedRowRender: (x) => (
          <Flex vertical gap={6}>
            {x.rules.map((r) => (
              <Flex key={r.id} gap={12} align="center" wrap>
                <WhatCell rule={r} />
                <Link href={`/access/${r.id}`}>
                  <WhereCell rule={r} />
                </Link>
              </Flex>
            ))}
          </Flex>
        ),
      }}
      columns={[
        {
          title: t('access.col.recipient'),
          key: 'who',
          render: (_, x) =>
            x.who.kind === 'group' && x.who.groupId ? (
              <Link href={`/people/groups/${x.who.groupId}`}>{x.who.label}</Link>
            ) : x.who.kind === 'person' && x.who.username ? (
              <Link href={`/people/${encodeURIComponent(x.who.username)}`}>{x.who.label}</Link>
            ) : x.who.kind === 'everyone' ? (
              <Tag color="warning">{x.who.label}</Tag>
            ) : (
              x.who.label
            ),
        },
        {
          title: t('access.col.kind'),
          key: 'kind',
          width: 160,
          render: (_, x) => t(`rule.kind.${x.who.kind}`),
        },
        {
          title: t('access.col.count'),
          key: 'count',
          width: 120,
          render: (_, x) => x.rules.length,
        },
      ]}
    />
  );
}

/** "By resources": rules grouped by service and type — what the rules themselves say (who sees a resource needs E1). */
function ByResources({ rules }: { rules: Rule[] }) {
  const { t } = useTranslation();
  const grouped = useMemo(() => {
    const map = new Map<string, { service: string; type: string; rules: Rule[] }>();
    for (const r of rules) {
      if (hasId(r.role_id)) continue;
      const key = `${r.service_name}/${r.type_resource_name}`;
      const entry = map.get(key) ?? {
        service: r.service_name ?? '',
        type: r.type_resource_name ?? '',
        rules: [],
      };
      entry.rules.push(r);
      map.set(key, entry);
    }
    return [...map.entries()].sort(([a], [b]) => a.localeCompare(b));
  }, [rules]);

  return (
    <Flex vertical gap={12}>
      {grouped.map(([key, entry]) => (
        <div
          key={key}
          style={{
            background: 'var(--licetum-surface)',
            border: '1px solid var(--licetum-border)',
            borderRadius: 8,
            padding: 16,
          }}
        >
          <Typography.Text strong>
            {entry.service} / {entry.type}
          </Typography.Text>{' '}
          <Typography.Text type="secondary">
            · {t('access.rulesCount', { count: entry.rules.length })}
          </Typography.Text>
          <Flex vertical gap={6} style={{ marginTop: 8 }}>
            {entry.rules.map((r) => (
              <Flex key={r.id} gap={12} align="center" wrap>
                <WhatCell rule={r} />
                <WhoCell rule={r} />
                <Link href={`/access/${r.id}`}>{r.resource_id ?? t('rule.wholeType')}</Link>
              </Flex>
            ))}
          </Flex>
        </div>
      ))}
      <Alert type="info" showIcon title={t('access.byResourcesNote')} />
      {grouped.length === 0 && <EmptyBlock title={t('access.noMatch.title')} />}
    </Flex>
  );
}
