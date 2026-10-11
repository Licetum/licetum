'use client';

import { Alert, Flex, Skeleton, Tag, Typography } from 'antd';
import { useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { previewAssignmentRule } from '@/api/admin/assignment-rule/assignment-rule';
import type { AssignmentRuleInput, GrantPreview } from '@/api/admin/model';

const MAX_PREVIEWED = 10;
const DEBOUNCE_MS = 450;

type State =
  | { status: 'idle' }
  | { status: 'loading' }
  | { status: 'failed' }
  | { status: 'ready'; preview: GrantPreview; more: number };

/** The previews of up to MAX_PREVIEWED rules of the form, put into one: counts add up, the lists are merged. */
function merge(previews: GrantPreview[]): GrantPreview {
  const [first, ...rest] = previews;
  if (!first) throw new Error('nothing to merge');
  const types = new Map<string, GrantPreview['types'][number]>();
  const conflicts = new Map<string, GrantPreview['conflicts'][number]>();
  const already = new Map<string, GrantPreview['already_granted'][number]>();
  for (const p of previews) {
    for (const x of p.types) {
      const key = `${x.service}/${x.type}`;
      const seen = types.get(key);
      types.set(key, seen ? { ...seen, count: seen.count + x.count } : x);
    }
    for (const c of p.conflicts) conflicts.set(`${c.rule.rule_id}/${c.position}`, c);
    for (const r of p.already_granted) already.set(r.rule_id, r);
  }
  return {
    ...first,
    resources: previews.reduce((n, p) => n + p.resources, 0),
    types: [...types.values()],
    sample: previews.flatMap((p) => p.sample).slice(0, 10),
    dynamic: previews.some((p) => p.dynamic),
    capability: rest.length === 0 ? first.capability : previews.every((p) => p.capability),
    conflicts: [...conflicts.values()],
    already_granted: [...already.values()],
  };
}

/**
 * What the rule being written would do, asked of the server as the form changes and before anything is saved: how many
 * resources it covers, whom it reaches, which denials and exceptions meet it. Nothing here blocks the form — it is
 * information, and a failed preview just says it could not be computed.
 */
export function GrantPreviewPanel({
  inputs,
  ready,
}: {
  inputs: AssignmentRuleInput[];
  ready: boolean;
}) {
  const { t } = useTranslation();
  const [state, setState] = useState<State>({ status: 'idle' });
  const counter = useRef(0);
  const key = JSON.stringify(inputs);

  useEffect(() => {
    if (!ready || inputs.length === 0) {
      counter.current++;
      setState({ status: 'idle' });
      return;
    }
    const run = ++counter.current;
    setState({ status: 'loading' });
    const timer = setTimeout(async () => {
      try {
        const answers = await Promise.all(
          inputs.slice(0, MAX_PREVIEWED).map((data) => previewAssignmentRule(data)),
        );
        if (run !== counter.current) return;
        const previews = answers.map((a) => a.data as GrantPreview);
        setState({
          status: 'ready',
          preview: merge(previews),
          more: Math.max(0, inputs.length - MAX_PREVIEWED),
        });
      } catch {
        if (run === counter.current) setState({ status: 'failed' });
      }
    }, DEBOUNCE_MS);
    return () => clearTimeout(timer);
    // `key` stands for the content of `inputs`: the array itself is new on every render.
  }, [key, ready]);

  if (state.status === 'idle') return null;
  if (state.status === 'loading') return <Skeleton active paragraph={{ rows: 2 }} title={false} />;
  if (state.status === 'failed')
    return (
      <Typography.Text type="secondary" style={{ fontSize: 13 }}>
        {t('grant.preview.failed')}
      </Typography.Text>
    );

  const { preview: p, more } = state;
  const people = p.people;
  return (
    <Flex vertical gap={8} data-testid="grant-preview" aria-live="polite">
      <Typography.Text strong>{t('grant.preview.title')}</Typography.Text>

      {p.capability ? (
        <Typography.Text>{t('grant.preview.capability')}</Typography.Text>
      ) : p.dynamic ? (
        <Typography.Text>{t('grant.preview.dynamic')}</Typography.Text>
      ) : (
        <Flex vertical gap={4}>
          <Typography.Text>
            {t('grant.preview.resources', { count: p.resources })}
            {more > 0 && ` ${t('grant.preview.partial', { count: more })}`}
          </Typography.Text>
          {p.types.length > 0 && (
            <Flex gap={4} wrap>
              {p.types.map((x) => (
                <Tag key={`${x.service}/${x.type}`} style={{ margin: 0 }}>
                  {x.type} · {x.count}
                </Tag>
              ))}
            </Flex>
          )}
          {p.sample.length > 0 && (
            <Typography.Text type="secondary" style={{ fontSize: 13 }}>
              {p.sample.join(', ')}
              {p.resources > p.sample.length && ' …'}
            </Typography.Text>
          )}
        </Flex>
      )}

      {people.registry > 0 ? (
        <Typography.Text>
          {people.total > 0
            ? t('grant.preview.people', { count: people.total })
            : t('grant.preview.noPeople', { count: people.registry })}
          {people.sample.length > 0 &&
            `: ${people.sample.map((x) => x.name || x.subject).join(', ')}${people.total > people.sample.length ? ' …' : ''}`}
        </Typography.Text>
      ) : (
        <Typography.Text type="secondary" style={{ fontSize: 13 }}>
          {people.enabled ? t('grant.preview.registryEmpty') : t('grant.preview.registryOff')}
        </Typography.Text>
      )}

      {p.conflicts.length > 0 && (
        <Alert
          type="warning"
          showIcon
          title={t('grant.preview.conflicts', { count: p.conflicts.length })}
          description={
            <Flex vertical gap={2}>
              {p.conflicts.map((c) => (
                <span key={`${c.rule.rule_id}/${c.position}`}>
                  {t(`grant.preview.conflict.${c.kind}.${c.position}`)}{' '}
                  <b>{c.name || c.rule.applies_to}</b>
                  {c.rule.anchor.resource_id ? ` (${c.rule.anchor.resource_id})` : ''}
                </span>
              ))}
            </Flex>
          }
        />
      )}
      {p.already_granted.length > 0 && (
        <Alert
          type="info"
          showIcon
          title={t('grant.preview.already', { count: p.already_granted.length })}
        />
      )}
    </Flex>
  );
}
