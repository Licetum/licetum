import type { TFunction } from 'i18next';

import type {
  AssignmentRuleInput,
  AssignmentRuleWithContext,
  PredicateCondition,
} from '@/api/admin/model';

const NIL_UUID = '00000000-0000-0000-0000-000000000000';
/** The API returns the zero UUID where an id does not apply (a role rule has no type, an inline rule has no role). */
export const hasId = (id: string | null | undefined): id is string => !!id && id !== NIL_UUID;

export type Rule = AssignmentRuleWithContext;

const OPERATORS: Record<string, string> = {
  eq: '=',
  ne: '≠',
  in: '∈',
  not_in: '∉',
  regex: '~',
  gt: '>',
  ge: '≥',
  lt: '<',
  le: '≤',
  in_cidr: '⊂',
};

/** "department = sales", "request.ip ⊂ 10.0.0.0/8", "time.hour ≥ 9". */
function describeCondition(c: PredicateCondition): string {
  const source = c.source === 'context' ? 'request.' : c.source === 'now' ? 'time.' : '';
  const value = Array.isArray(c.value) ? `[${c.value.join(', ')}]` : c.value;
  return `${source}${c.attr} ${OPERATORS[c.op] ?? c.op} ${value}`;
}

export const describeConditions = (conditions: PredicateCondition[] | null | undefined): string =>
  (conditions ?? []).map(describeCondition).join(' · ');

type WhoKind = 'group' | 'person' | 'condition' | 'everyone';
export type Who = { kind: WhoKind; label: string; groupId?: string; username?: string };

/** Who a rule applies to: a group (by subject or by membership), one person, anyone matching claims, or everyone. */
export function describeWho(
  rule: Rule,
  groupName: (id: string) => string | undefined,
  t: TFunction,
): Who {
  if (hasId(rule.member_of_group_id)) {
    return {
      kind: 'group',
      groupId: rule.member_of_group_id,
      label: groupName(rule.member_of_group_id) ?? t('rule.unknownGroup'),
    };
  }
  if (rule.subject?.startsWith('group:')) {
    const id = rule.subject.slice('group:'.length);
    return { kind: 'group', groupId: id, label: groupName(id) ?? t('rule.unknownGroup') };
  }
  if (rule.subject) return { kind: 'person', username: rule.subject, label: rule.subject };
  if (rule.predicate?.length)
    return { kind: 'condition', label: describeConditions(rule.predicate) };
  return { kind: 'everyone', label: t('rule.everyone') };
}

export type Decision = 'allow' | 'deny' | 'except';
export const ruleDecision = (rule: Rule): Decision =>
  rule.effect === 'deny' ? 'deny' : rule.exception ? 'except' : 'allow';

/** "read, write", "@editor", or the name of the role. */
export function describeWhat(rule: Rule, t: TFunction): string {
  if (hasId(rule.role_id)) return t('rule.role', { name: rule.role_name ?? '' });
  const levels = (rule.access ?? []).map((a) => (a === '*' ? t('rule.allLevels') : a)).join(', ');
  return levels || '—';
}

/** "Documents / Document · all resources of the type" — the place a rule points to. */
export function describeWhere(
  rule: Rule,
  t: TFunction,
  resourceName?: (typeId: string, resourceId: string) => string | undefined,
) {
  if (hasId(rule.role_id)) {
    return { service: '', type: '', target: t('rule.roleTarget'), line: t('rule.roleTarget') };
  }
  const service = rule.service_name ?? '';
  const type = rule.type_resource_name ?? '';
  let target: string;
  if (rule.resource_id) {
    const name = resourceName?.(rule.type_resource_id, rule.resource_id);
    target = name ? `«${name}»` : rule.resource_id;
  } else if (rule.resource_from_claim) {
    target = t('rule.fromClaim', { claim: rule.resource_from_claim.claim });
  } else if (rule.resource_where?.length) {
    target = t('rule.where', { cond: describeConditions(rule.resource_where) });
  } else {
    target = t('rule.wholeType');
  }
  return { service, type, target, line: `${service ? `${service} / ` : ''}${type} · ${target}` };
}

export type RuleStatus = 'active' | 'disabled' | 'scheduled' | 'expired' | 'expiring' | 'dead';

const DAY = 86_400_000;
const EXPIRING_DAYS = 7;

export function ruleStatus(rule: Rule, now = Date.now()): { status: RuleStatus; days?: number } {
  if (!rule.enabled) return { status: 'disabled' };
  if (!rule.target_valid) return { status: 'dead' };
  const from = rule.valid_from ? Date.parse(rule.valid_from) : null;
  const to = rule.expires_at ? Date.parse(rule.expires_at) : null;
  if (to !== null && to <= now) return { status: 'expired' };
  if (from !== null && from > now) return { status: 'scheduled' };
  if (to !== null && to - now <= EXPIRING_DAYS * DAY)
    return { status: 'expiring', days: Math.max(0, Math.ceil((to - now) / DAY)) };
  return { status: 'active' };
}

/**
 * The request body that writes a rule back as it is. PATCH replaces the whole rule, so every change is a full body;
 * the same body re-creates a deleted rule (with its id) when a deletion is undone.
 */
export function ruleToInput(rule: Rule): AssignmentRuleInput {
  const input: AssignmentRuleInput = {
    description: rule.description,
    effect: rule.effect,
    enabled: rule.enabled,
    exception: rule.exception,
    expires_at: rule.expires_at,
    name: rule.name,
    valid_from: rule.valid_from,
    requires_predicate: rule.requires_predicate ?? [],
  };
  if (rule.effect === 'deny') input.on_missing_attribute = rule.on_missing_attribute;
  if (hasId(rule.member_of_group_id)) input.member_of_group_id = rule.member_of_group_id;
  if (rule.subject) input.subject = rule.subject;
  if (rule.subject_kind)
    input.subject_kind = rule.subject_kind as AssignmentRuleInput['subject_kind'];
  if (rule.predicate?.length) input.predicate = rule.predicate;
  if (hasId(rule.requires_group_id)) input.requires_group_id = rule.requires_group_id;
  if (hasId(rule.role_id)) {
    input.role_id = rule.role_id;
  } else {
    input.type_resource_id = rule.type_resource_id;
    input.access = rule.access ?? [];
    if (rule.resource_id) input.resource_id = rule.resource_id;
    if (rule.resource_from_claim) input.resource_from_claim = rule.resource_from_claim;
    if (rule.resource_where?.length) input.resource_where = rule.resource_where;
  }
  return input;
}
