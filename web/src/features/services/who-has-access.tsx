'use client';

import { Alert, Collapse, Flex, Table, Tag, Tooltip, Typography } from 'antd';
import Link from 'next/link';
import { useTranslation } from 'react-i18next';

import type { AccessEntry } from '@/api/admin/model';
import { useWhoHasAccess } from '@/api/admin/resource/resource';
import { DecisionTag, ErrorBlock, LoadingBlock } from '@/components/kit';
import { describeConditions, type Decision } from '@/domain/rule';

const decisionOf = (e: AccessEntry): Decision =>
  e.rule.effect === 'deny' ? 'deny' : e.rule.exception ? 'except' : 'allow';

type KeyedEntry = AccessEntry & { key: number };

/**
 * Keys the entries by their position in the answer. The answer has one entry per rule instance, and one rule can make
 * several reached the same way (a role with several items, a rule matched by attributes on several ancestors), so
 * neither the rule nor how it reaches the resource is unique.
 */
const keyed = (entries: AccessEntry[]): KeyedEntry[] => entries.map((e, key) => ({ ...e, key }));

function Who({ entry }: { entry: AccessEntry }) {
  const { t } = useTranslation();
  const { who } = entry;
  const body =
    who.kind === 'group' && who.group ? (
      <Link href={`/people/groups/${who.group.id}`}>{who.group.name || who.group.id}</Link>
    ) : who.kind === 'user' && who.subject ? (
      <Link href={`/people/${encodeURIComponent(who.subject)}`}>{who.subject}</Link>
    ) : who.kind === 'condition' ? (
      <span className="mono">{describeConditions(who.predicate)}</span>
    ) : (
      <Tag color="warning" style={{ margin: 0 }}>
        {t('rule.everyone')}
      </Tag>
    );
  return (
    <div>
      <div style={{ fontWeight: 500 }}>{body}</div>
      <div style={{ color: 'var(--licetum-muted)', fontSize: 12 }}>
        {t(`rule.kind.${who.kind === 'user' ? 'person' : who.kind}`)}
        {entry.role && ` · ${t('whohas.role', { name: entry.role.name })}`}
        {who.guarded && ` · ${t('whohas.guarded')}`}
      </div>
    </div>
  );
}

function People({ entry, known }: { entry: AccessEntry; known: boolean }) {
  const { t } = useTranslation();
  if (!known) return <span style={{ color: 'var(--licetum-muted)' }}>—</span>;
  if (entry.people_total === 0) return <span style={{ color: 'var(--licetum-muted)' }}>0</span>;
  const rest = entry.people_total - entry.people.length;
  return (
    <Flex gap={4} wrap>
      {entry.people.slice(0, 5).map((p) => (
        <Tooltip key={p.subject} title={p.name ? p.subject : undefined}>
          <Link href={`/people/${encodeURIComponent(p.subject)}`}>{p.name || p.subject}</Link>
        </Tooltip>
      ))}
      {entry.people.length > 5 && <span>+{entry.people.length - 5}</span>}
      {rest > 0 && (
        <span style={{ color: 'var(--licetum-muted)' }}>{t('whohas.more', { count: rest })}</span>
      )}
    </Flex>
  );
}

/**
 * Who has access to a resource (or to a type as a whole): the rules in force that cover it, denials first, with the
 * levels that reach it and, where the registry knows them, the people. It is a list of rules, not of decisions — the
 * check says what one person gets.
 */
export function WhoHasAccess({
  serviceId,
  typeId,
  resourceId,
}: {
  serviceId: string;
  typeId: string;
  resourceId?: string;
}) {
  const { t } = useTranslation();
  const query = useWhoHasAccess(
    serviceId,
    typeId,
    resourceId ? { resource_id: resourceId } : undefined,
    { query: { select: (r) => (r.status === 200 ? r.data : null) } },
  );

  if (query.isPending) return <LoadingBlock rows={3} />;
  if (query.isError) return <ErrorBlock error={query.error} onRetry={() => void query.refetch()} />;
  const data = query.data;
  if (!data) return null;
  const known = data.people.registry > 0;
  const levelName = (l: string) => (l === '*' ? t('rule.allLevels') : l);

  const columns = [
    {
      title: t('whohas.col.who'),
      key: 'who',
      render: (_: unknown, e: AccessEntry) => <Who entry={e} />,
    },
    {
      title: t('whohas.col.what'),
      key: 'what',
      render: (_: unknown, e: AccessEntry) => (
        <Flex vertical gap={4} align="flex-start">
          <DecisionTag decision={decisionOf(e)}>
            {t(`whohas.decision.${decisionOf(e)}`)}
          </DecisionTag>
          <span className="mono" style={{ fontSize: 12 }}>
            {e.levels.map(levelName).join(', ')}
          </span>
        </Flex>
      ),
    },
    {
      title: t('whohas.col.via'),
      key: 'via',
      render: (_: unknown, e: AccessEntry) => (
        <span>
          {t(`whohas.via.${e.via}`)}
          {e.via === 'ancestor' && e.rule.anchor.resource_id && (
            <span className="mono"> · {e.rule.anchor.resource_id}</span>
          )}
          {e.assumed ? (
            <Tag color="warning" style={{ marginLeft: 6 }}>
              {t('whohas.assumed')}
            </Tag>
          ) : (
            e.assumed_levels.length > 0 && (
              <Tag color="warning" style={{ marginLeft: 6 }}>
                {t('whohas.assumedLevels', { levels: e.assumed_levels.map(levelName).join(', ') })}
              </Tag>
            )
          )}
        </span>
      ),
    },
    {
      title: t('whohas.col.people'),
      key: 'people',
      render: (_: unknown, e: AccessEntry) => <People entry={e} known={known} />,
    },
    {
      title: '',
      key: 'open',
      width: 90,
      render: (_: unknown, e: AccessEntry) => (
        <Link href={`/access/${e.rule.rule_id}`}>{e.name || t('whohas.open')}</Link>
      ),
    },
  ];

  return (
    <Flex vertical gap={12} data-testid="who-has-access">
      {data.entries.length === 0 ? (
        <Alert type="info" showIcon title={t('whohas.none')} />
      ) : (
        <Table<KeyedEntry>
          rowKey="key"
          size="small"
          pagination={false}
          dataSource={keyed(data.entries)}
          columns={columns}
          scroll={{ x: true }}
        />
      )}
      {data.dynamic.length > 0 && (
        <Collapse
          size="small"
          items={[
            {
              key: 'dynamic',
              label: t('whohas.dynamic', { count: data.dynamic.length }),
              children: (
                <Flex vertical gap={8}>
                  <Typography.Text type="secondary">{t('whohas.dynamicHint')}</Typography.Text>
                  <Table<KeyedEntry>
                    rowKey="key"
                    size="small"
                    pagination={false}
                    dataSource={keyed(data.dynamic)}
                    columns={columns.filter((c) => c.key !== 'via')}
                  />
                </Flex>
              ),
            },
          ]}
        />
      )}
      <Typography.Text type="secondary" style={{ fontSize: 12 }}>
        {known ? t('whohas.registry', { count: data.people.registry }) : t('whohas.registryEmpty')}
        {data.people.limited && ` ${t('whohas.limited')}`} {t('whohas.note')}
      </Typography.Text>
    </Flex>
  );
}
