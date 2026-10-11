import type { TFunction } from 'i18next';

import type {
  AssignmentRuleInput,
  PredicateCondition,
  TypeResourceWithService,
} from '@/api/admin/model';
import { endOfDayISO } from '@/domain/format';

export type CondSource = 'claim' | 'context' | 'now';
export type CondOp =
  | 'eq'
  | 'ne'
  | 'in'
  | 'not_in'
  | 'regex'
  | 'gt'
  | 'ge'
  | 'lt'
  | 'le'
  | 'in_cidr';

/** One condition row of the form. A list value is typed as comma-separated text. */
export type Cond = { id: string; source: CondSource; attr: string; op: CondOp; value: string };

export type WhoMode = 'group' | 'person' | 'condition' | 'everyone';
type WhatMode = 'levels' | 'role';
export type ScopeMode = 'all' | 'resources' | 'where' | 'claim';

export type GrantForm = {
  who: WhoMode;
  groupId?: string;
  person: string;
  whoConds: Cond[];
  everyoneConfirmed: boolean;

  what: WhatMode;
  levels: string[];
  accessGroups: string[];
  roleId?: string;

  serviceId?: string;
  typeId?: string;
  scope: ScopeMode;
  resourceIds: string[];
  where: Cond[];
  claim: string;
  matchBy: string;

  validFrom?: string;
  expiresAt?: string;
  requires: Cond[];
  requiresGroupId?: string;

  name: string;
  description: string;
  deny: boolean;
  exception: boolean;
  enabled: boolean;
  onMissing: 'skip' | 'apply';
};

export const emptyForm = (): GrantForm => ({
  who: 'group',
  person: '',
  whoConds: [],
  everyoneConfirmed: false,
  what: 'levels',
  levels: ['read'],
  accessGroups: [],
  scope: 'all',
  resourceIds: [],
  where: [],
  claim: '',
  matchBy: 'name',
  requires: [],
  name: '',
  description: '',
  deny: false,
  exception: false,
  enabled: true,
  onMissing: 'apply',
});

let counter = 0;
export const newCond = (source: CondSource = 'claim'): Cond => ({
  id: `c${++counter}`,
  source,
  attr: '',
  op: 'eq',
  value: '',
});

const LIST_OPS: CondOp[] = ['in', 'not_in', 'in_cidr'];
const isListOp = (op: CondOp) => LIST_OPS.includes(op);

export function toPredicate(c: Cond): PredicateCondition {
  const value = isListOp(c.op)
    ? c.value
        .split(',')
        .map((v) => v.trim())
        .filter(Boolean)
    : c.value.trim();
  return { attr: c.attr.trim(), op: c.op, source: c.source, value };
}

/** A condition row is ready when it names an attribute and has a value. */
export const condReady = (c: Cond) => c.attr.trim() !== '' && c.value.trim() !== '';

/** The levels a type works with: the ones its hierarchy names, plus read and write. */
export function levelsOf(type: TypeResourceWithService | undefined): string[] {
  const set = new Set<string>(['read', 'write']);
  for (const [level, implied] of Object.entries(type?.access_implies ?? {})) {
    set.add(level);
    for (const i of implied ?? []) set.add(i);
  }
  return [...set];
}

export type Problem = { key: string; values?: Record<string, string> };

/** What stops the form from being submitted, in the order a person fills it in. null — nothing. */
export function problemOf(f: GrantForm, ctx: { hasSource: boolean }): Problem | null {
  if (f.who === 'group' && !f.groupId) return { key: 'grant.problem.group' };
  if (f.who === 'person' && !f.person.trim()) return { key: 'grant.problem.person' };
  if (f.who === 'condition' && (f.whoConds.length === 0 || !f.whoConds.every(condReady)))
    return { key: 'grant.problem.condition' };
  if (f.who === 'everyone' && !f.everyoneConfirmed) return { key: 'grant.problem.everyone' };
  if (f.what === 'role') {
    if (!f.roleId) return { key: 'grant.problem.role' };
  } else {
    if (!f.typeId) return { key: 'grant.problem.type' };
    if (f.levels.length + f.accessGroups.length === 0) return { key: 'grant.problem.levels' };
    if (f.scope === 'resources' && f.resourceIds.length === 0)
      return { key: 'grant.problem.resources' };
    if (f.scope === 'where' && (f.where.length === 0 || !f.where.every(condReady)))
      return { key: 'grant.problem.where' };
    if (f.scope === 'claim' && !f.claim.trim()) return { key: 'grant.problem.claim' };
    if (f.scope !== 'all' && !ctx.hasSource) return { key: 'grant.problem.source' };
  }
  if (f.validFrom && f.expiresAt && f.validFrom > f.expiresAt)
    return { key: 'grant.problem.dates' };
  if (f.requires.some((c) => !condReady(c))) return { key: 'grant.problem.guard' };
  return null;
}

/** The request bodies for the form: one rule, or one per chosen resource. */
export function buildInputs(f: GrantForm, lookupTypeId?: string): AssignmentRuleInput[] {
  const base: AssignmentRuleInput = {
    effect: f.deny ? 'deny' : 'allow',
    exception: !f.deny && f.exception,
    enabled: f.enabled,
    name: f.name.trim() || null,
    description: f.description.trim() || null,
    valid_from: f.validFrom ? new Date(`${f.validFrom}T00:00:00`).toISOString() : null,
    expires_at: f.expiresAt ? endOfDayISO(f.expiresAt) : null,
  };
  if (f.deny) base.on_missing_attribute = f.onMissing;
  if (f.requires.length) base.requires_predicate = f.requires.map(toPredicate);
  if (f.requiresGroupId) base.requires_group_id = f.requiresGroupId;

  if (f.who === 'group') base.member_of_group_id = f.groupId;
  else if (f.who === 'person') {
    base.subject = f.person.trim();
    base.subject_kind = 'user';
  } else if (f.who === 'condition') base.predicate = f.whoConds.map(toPredicate);

  if (f.what === 'role') return [{ ...base, role_id: f.roleId }];

  const access = [...f.levels, ...f.accessGroups.map((g) => `@${g}`)];
  const target: AssignmentRuleInput = { ...base, type_resource_id: f.typeId, access };
  if (f.scope === 'resources') return f.resourceIds.map((id) => ({ ...target, resource_id: id }));
  if (f.scope === 'where') return [{ ...target, resource_where: f.where.map(toPredicate) }];
  if (f.scope === 'claim') {
    return [
      {
        ...target,
        resource_from_claim: {
          claim: f.claim.trim(),
          lookup_type_resource_id: lookupTypeId ?? f.typeId ?? '',
          match_by: f.matchBy || 'name',
        },
      },
    ];
  }
  return [target];
}

export type SentenceParts = {
  groupName?: string;
  roleName?: string;
  typeName?: string;
  serviceName?: string;
  resourceNames: string[];
  whoConds: string;
};

/** "Group «sales» will be able to read all resources of the type «Document» of the service «Docs». No end date." */
export function sentence(f: GrantForm, p: SentenceParts, t: TFunction): string {
  const who =
    f.who === 'group'
      ? t('grant.sentence.whoGroup', { name: p.groupName ?? '…' })
      : f.who === 'person'
        ? t('grant.sentence.whoPerson', { name: f.person.trim() || '…' })
        : f.who === 'condition'
          ? t('grant.sentence.whoCondition', { cond: p.whoConds || '…' })
          : t('grant.sentence.whoEveryone');

  const term = f.expiresAt
    ? t('grant.sentence.until', { date: f.expiresAt })
    : f.validFrom
      ? t('grant.sentence.from', { date: f.validFrom })
      : t('grant.sentence.forever');

  if (f.what === 'role') {
    return t('grant.sentence.role', { who, role: p.roleName ?? '…', term });
  }

  const levels = [...f.levels, ...f.accessGroups.map((g) => `@${g}`)].join(', ') || '…';
  const place =
    f.scope === 'all'
      ? t('grant.sentence.placeAll', { type: p.typeName ?? '…', service: p.serviceName ?? '…' })
      : f.scope === 'resources'
        ? t('grant.sentence.placeResources', {
            count: p.resourceNames.length,
            names: p.resourceNames.slice(0, 3).join(', '),
            type: p.typeName ?? '…',
          })
        : f.scope === 'where'
          ? t('grant.sentence.placeWhere', {
              type: p.typeName ?? '…',
              cond: f.where.map((c) => `${c.attr} ${c.value}`).join(' · '),
            })
          : t('grant.sentence.placeClaim', { type: p.typeName ?? '…', claim: f.claim || '…' });

  const verb = f.deny ? 'deny' : f.exception ? 'except' : 'allow';
  return t(`grant.sentence.${verb}`, { who, levels, place, term });
}
