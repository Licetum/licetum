'use client';

import { Tag, Tooltip } from 'antd';
import Link from 'next/link';
import { useTranslation } from 'react-i18next';

import { DecisionTag } from '@/components/kit';
import {
  describeConditions,
  describeWhat,
  describeWhere,
  describeWho,
  hasId,
  ruleDecision,
  ruleStatus,
  type Rule,
} from '@/domain/rule';

import { useGroupNames } from '../data/queries';

/** Who a rule applies to, as a link where there is a page to go to. */
export function WhoCell({ rule }: { rule: Rule }) {
  const { t } = useTranslation();
  const groups = useGroupNames();
  const who = describeWho(rule, groups.name, t);
  const kind = t(`rule.kind.${who.kind}`);
  const label =
    who.kind === 'group' && who.groupId ? (
      <Link href={`/people/groups/${who.groupId}`} onClick={(e) => e.stopPropagation()}>
        {who.label}
      </Link>
    ) : who.kind === 'person' && who.username ? (
      <Link
        href={`/people/${encodeURIComponent(who.username)}`}
        onClick={(e) => e.stopPropagation()}
      >
        {who.label}
      </Link>
    ) : who.kind === 'everyone' ? (
      <Tag color="warning" style={{ margin: 0 }}>
        {who.label}
      </Tag>
    ) : (
      <span>{who.label}</span>
    );
  return (
    <div>
      <div style={{ fontWeight: 500 }}>{label}</div>
      <div style={{ color: 'var(--licetum-muted)', fontSize: 12 }}>{kind}</div>
    </div>
  );
}

/** What a rule gives (or takes away): the decision with its levels, or the role. */
export function WhatCell({ rule }: { rule: Rule }) {
  const { t } = useTranslation();
  const decision = ruleDecision(rule);
  const text = describeWhat(rule, t);
  if (hasId(rule.role_id)) return <DecisionTag decision={decision}>{text}</DecisionTag>;
  const prefix =
    decision === 'deny'
      ? `${t('rule.deny')}: `
      : decision === 'except'
        ? `${t('rule.exception')}: `
        : '';
  return (
    <DecisionTag decision={decision}>
      {prefix}
      <span className="mono">{text}</span>
    </DecisionTag>
  );
}

export function WhereCell({ rule }: { rule: Rule }) {
  const { t } = useTranslation();
  const where = describeWhere(rule, t);
  return (
    <div>
      <div>{hasId(rule.role_id) ? where.target : `${where.type} · ${where.target}`}</div>
      {where.service && (
        <div style={{ color: 'var(--licetum-muted)', fontSize: 12 }}>{where.service}</div>
      )}
    </div>
  );
}

/** "until 31.12.2026", "only if department = sales" — the conditions that narrow a rule. */
export function ConditionsCell({ rule, lang }: { rule: Rule; lang: string }) {
  const { t } = useTranslation();
  const parts: string[] = [];
  if (rule.valid_from)
    parts.push(t('rule.from', { date: new Date(rule.valid_from).toLocaleDateString(lang) }));
  if (rule.expires_at)
    parts.push(t('rule.until', { date: new Date(rule.expires_at).toLocaleDateString(lang) }));
  if (rule.requires_predicate?.length)
    parts.push(t('rule.onlyIf', { cond: describeConditions(rule.requires_predicate) }));
  if (hasId(rule.requires_group_id)) parts.push(t('rule.guardGroup'));
  return <span style={{ fontSize: 13 }}>{parts.length ? parts.join(' · ') : '—'}</span>;
}

export function StatusTag({ rule }: { rule: Rule }) {
  const { t } = useTranslation();
  const { status, days } = ruleStatus(rule);
  switch (status) {
    case 'active':
      return <DecisionTag decision="allow">{t('rule.status.active')}</DecisionTag>;
    case 'expiring':
      return (
        <DecisionTag decision="except">
          {t('rule.status.expiring', { count: days ?? 0 })}
        </DecisionTag>
      );
    case 'dead':
      return (
        <Tooltip title={t('rule.status.deadHint')}>
          <span>
            <DecisionTag decision="deny">{t('rule.status.dead')}</DecisionTag>
          </span>
        </Tooltip>
      );
    default:
      return <DecisionTag decision="inherit">{t(`rule.status.${status}`)}</DecisionTag>;
  }
}

/** The title of a rule: its name if it has one, otherwise a sentence built from the parts. */
export function useRuleTitle() {
  const { t } = useTranslation();
  const groups = useGroupNames();
  return (rule: Rule): string => {
    if (rule.name) return rule.name;
    const who = describeWho(rule, groups.name, t);
    const where = describeWhere(rule, t);
    const what = describeWhat(rule, t);
    return t('rule.title', { who: who.label, what, where: where.line });
  };
}
