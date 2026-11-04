'use client';

import { useInfiniteQuery, useQueries } from '@tanstack/react-query';
import { Button, Flex, Table, Tag, Typography } from 'antd';
import Link from 'next/link';
import { useEffect, useMemo, useState, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';

import type { TypeResourceWithService } from '@/api/admin/model';
import { simulateSearchAction, simulateSearchResource } from '@/api/admin/simulation/simulation';
import { DecisionTag, EmptyBlock, errorDetail, ErrorBlock, LoadingBlock } from '@/components/kit';
import { searchActionRequest, searchResourceRequest, type Sample } from '@/lib/authzen';

import { useAllTypes, useRules } from '../data/queries';
import { useSourceKinds } from '../services/types-tab';
import {
  accumulate,
  hitOf,
  levelsToAsk,
  mergeFeeds,
  type LevelFeed,
  type SeenRow,
} from './seen-model';

/** The person the simulation asks about: the name, the claims their token would carry and the attributes of the request. */
export type Who = Pick<Sample, 'person' | 'properties' | 'context'>;

/** How many resources one page of the search of one level holds. */
const PAGE = 50;

/** The simulation reads everything afresh on each call, so what was asked is kept for a while and not asked again on a focus. */
const KEEP = { staleTime: 60_000, refetchOnWindowFocus: false } as const;

/**
 * What a person sees in a service, drawn from the AuthZEN searches of the simulation. A type is a row: the levels the person has
 * on it as a whole come from one action search per type. The resources of a type that has a source are read when its row is
 * opened, with one resource search per access level of the type and a page at a time (a "load more" button reads the next
 * page; nothing pages by itself), so the number of requests follows what is looked at, not the size of the service. `run`
 * starts the questions over (the button of the Check page).
 */
export function VisibleAccess(props: {
  serviceId: string;
  serviceSlug: string;
  who: Who;
  /** Only this type (its name), opened at once. */
  only?: string;
  run?: number;
  /** What to offer when the person sees nothing at all (give access). */
  emptyAction?: ReactNode;
}) {
  const { t } = useTranslation();
  const types = useAllTypes();
  const rules = useRules();
  const own = useMemo(
    () => (types.data ?? []).filter((x) => x.service_id === props.serviceId),
    [types.data, props.serviceId],
  );
  const shown = useMemo(
    () => (props.only ? own.filter((x) => x.name === props.only) : own),
    [own, props.only],
  );
  const { sources, pending: sourcesPending } = useSourceKinds(shown);
  const hasResources = (type: TypeResourceWithService) => !!sources.get(type.id);

  const whole = useQueries({
    queries: shown.map((type) => ({
      ...KEEP,
      queryKey: ['seen', 'type', props.serviceId, type.name, props.who, props.run],
      queryFn: async ({ signal }: { signal: AbortSignal }) => {
        const res = await simulateSearchAction(
          props.serviceId,
          searchActionRequest({ ...props.who, type: type.name, resource: '', action: '' }),
          { signal },
        );
        if (res.status !== 200) throw new Error(`HTTP ${res.status}`);
        return res.data.results.map((a) => a.name);
      },
    })),
  });

  const only = props.only ? shown[0]?.id : undefined;
  const [open, setOpen] = useState<string[]>(only ? [only] : []);
  useEffect(() => setOpen(only ? [only] : []), [only]);

  if (types.isPending || rules.isPending) return <LoadingBlock rows={3} />;
  if (types.isError) return <ErrorBlock error={types.error} onRetry={() => void types.refetch()} />;
  if (shown.length === 0) {
    return <EmptyBlock title={t('check.list.noTypes')} hint={t('check.list.noTypesHint')} />;
  }

  const settled = whole.every((q) => !q.isPending) && !sourcesPending;
  const nothing =
    settled &&
    whole.every((q) => !q.isError && (q.data ?? []).length === 0) &&
    !shown.some(hasResources);
  if (nothing) {
    return (
      <EmptyBlock
        title={t('check.list.none', { subject: props.who.person })}
        hint={t('check.list.noneHint')}
        action={props.emptyAction}
      />
    );
  }

  return (
    <>
      <Table<TypeResourceWithService>
        rowKey="id"
        size="middle"
        pagination={false}
        data-testid="seen-types"
        dataSource={shown}
        expandable={{
          expandedRowKeys: open,
          onExpandedRowsChange: (keys) => setOpen(keys.map(String)),
          rowExpandable: hasResources,
          expandedRowRender: (type) => (
            <TypeResources
              serviceId={props.serviceId}
              serviceSlug={props.serviceSlug}
              type={type}
              levels={levelsToAsk(type, rules.data ?? [])}
              who={props.who}
              run={props.run}
            />
          ),
        }}
        columns={[
          { title: t('check.type'), key: 'type', render: (_, x) => <b>{x.name}</b> },
          {
            title: t('check.list.resources'),
            key: 'kind',
            width: 220,
            render: (_, x) =>
              sourcesPending ? (
                '…'
              ) : hasResources(x) ? (
                <Typography.Text type="secondary">{t('check.list.openHint')}</Typography.Text>
              ) : (
                t('check.list.wholeType')
              ),
          },
          {
            title: t('check.list.wholeLevels'),
            key: 'levels',
            render: (_, x) => {
              const q = whole[shown.indexOf(x)];
              if (!q || q.isPending) return '…';
              if (q.isError)
                return <DecisionTag decision="deny">{errorDetail(q.error) ?? '?'}</DecisionTag>;
              return q.data && q.data.length > 0 ? (
                q.data.map((level) => (
                  <Tag key={level} className="mono">
                    {level}
                  </Tag>
                ))
              ) : (
                <span style={{ color: 'var(--licetum-muted)' }}>—</span>
              );
            },
          },
        ]}
      />
      {shown.some(hasResources) && (
        <Typography.Paragraph type="secondary" style={{ fontSize: 12, marginTop: 12 }}>
          {t('check.list.note')}
        </Typography.Paragraph>
      )}
    </>
  );
}

/** The resources of one type the person has any level on, read a page at a time (one search per level, together). */
function TypeResources(props: {
  serviceId: string;
  serviceSlug: string;
  type: TypeResourceWithService;
  levels: string[];
  who: Who;
  run?: number;
}) {
  const { t } = useTranslation();
  const { serviceId, type, levels, who } = props;
  const query = useInfiniteQuery({
    ...KEEP,
    queryKey: ['seen', 'resources', serviceId, type.name, who, levels, props.run],
    // The first round asks every level; the next ones only the levels that have a next page, with its token.
    initialPageParam: null as Record<string, string> | null,
    queryFn: async ({ pageParam, signal }): Promise<LevelFeed[]> => {
      const asked = pageParam ? Object.entries(pageParam) : levels.map((l) => [l, ''] as const);
      return Promise.all(
        asked.map(async ([level, token]) => {
          const res = await simulateSearchResource(
            serviceId,
            searchResourceRequest(
              { ...who, type: type.name, resource: '', action: level },
              { limit: PAGE, token },
            ),
            { signal },
          );
          if (res.status !== 200) throw new Error(`HTTP ${res.status}`);
          return {
            level,
            hits: res.data.results.map(hitOf),
            next: res.data.page?.next_token ?? '',
          };
        }),
      );
    },
    getNextPageParam: (_last, rounds) => {
      const open = accumulate(rounds).filter((f) => f.next !== '');
      return open.length > 0 ? Object.fromEntries(open.map((f) => [f.level, f.next])) : undefined;
    },
  });

  const { rows, more } = useMemo(
    () => mergeFeeds(accumulate(query.data?.pages ?? [])),
    [query.data],
  );

  if (query.isPending) return <LoadingBlock rows={2} />;
  if (query.isError) return <ErrorBlock error={query.error} onRetry={() => void query.refetch()} />;

  return (
    <Flex vertical gap={8}>
      {rows.length === 0 ? (
        <Typography.Text type="secondary">
          {more ? t('check.list.noneYet') : t('check.list.noResources')}
        </Typography.Text>
      ) : (
        <Table<SeenRow>
          size="small"
          rowKey="id"
          pagination={{ pageSize: 20, showSizeChanger: false, hideOnSinglePage: true }}
          dataSource={rows}
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
                r.levels.map((level) => (
                  <Tag key={level} className="mono">
                    {level}
                  </Tag>
                )),
            },
            {
              title: '',
              key: 'why',
              width: 110,
              render: (_, r) => (
                <Link
                  href={`/check?mode=one&service=${props.serviceSlug}&subject=${encodeURIComponent(who.person)}&type=${encodeURIComponent(type.name)}&resource=${encodeURIComponent(r.id)}&action=${encodeURIComponent(r.levels[0] ?? 'read')}`}
                >
                  {t('check.why')} ›
                </Link>
              ),
            },
          ]}
        />
      )}
      <Flex gap={12} align="center" wrap>
        <Typography.Text type="secondary" style={{ fontSize: 12 }}>
          {t('check.list.shown', { count: rows.length })}
        </Typography.Text>
        {more && (
          <Button
            size="small"
            loading={query.isFetchingNextPage}
            onClick={() => void query.fetchNextPage()}
          >
            {t('check.list.more')}
          </Button>
        )}
      </Flex>
    </Flex>
  );
}
