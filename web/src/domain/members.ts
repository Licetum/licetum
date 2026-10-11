import type { TFunction } from 'i18next';

import type { GroupMember, PredicateCondition } from '@/api/admin/model';

import { describeConditions, hasId } from './rule';

type MemberKind = 'person' | 'nested' | 'condition';
export type MemberView = { kind: MemberKind; label: string; username?: string; groupId?: string };

/** A person is a membership whose only condition is "preferred_username = name" — there is no separate person record. */
export const personPredicate = (username: string): PredicateCondition[] => [
  { attr: 'preferred_username', op: 'eq', source: 'claim', value: username.trim() },
];

export function personOf(predicate: PredicateCondition[] | null | undefined): string | null {
  if (predicate?.length === 1) {
    const p = predicate[0];
    if (
      p &&
      p.attr === 'preferred_username' &&
      p.op === 'eq' &&
      (p.source ?? 'claim') === 'claim' &&
      typeof p.value === 'string'
    )
      return p.value;
  }
  return null;
}

export function describeMember(
  m: GroupMember,
  groupName: (id: string) => string | undefined,
  t: TFunction,
): MemberView {
  if (hasId(m.member_group_id))
    return {
      kind: 'nested',
      groupId: m.member_group_id,
      label: groupName(m.member_group_id) ?? t('rule.unknownGroup'),
    };
  const person = personOf(m.predicate);
  if (person) return { kind: 'person', username: person, label: person };
  return { kind: 'condition', label: describeConditions(m.predicate) || t('members.everyone') };
}
