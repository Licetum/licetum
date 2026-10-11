import type { TFunction } from 'i18next';

import type { ChangeRecord } from '@/api/admin/model';

type Entity = Record<string, unknown>;

const asEntity = (value: unknown): Entity | null =>
  value && typeof value === 'object' ? (value as Entity) : null;

/** The entity as it was after the change, or before it for a delete. */
const snapshot = (record: ChangeRecord): Entity | null =>
  asEntity(record.after) ?? asEntity(record.before);

/** What a person calls the entity: its name, its slug, or a short form of its id. */
export function entityName(record: ChangeRecord, t: TFunction): string {
  const e = snapshot(record);
  const name = e && (e.name ?? e.slug ?? e.title);
  if (typeof name === 'string' && name) return name;
  if (record.entity === 'log_settings') return t('history.entity.log_settings');
  if (record.entity === 'role_permission' && e && Array.isArray(e.access))
    return e.access.join(', ');
  if (record.entity === 'assignment_rule' && e) {
    // A rule without a name is told by whom it is for.
    if (typeof e.subject === 'string' && e.subject)
      return e.subject.startsWith('group:') ? t('rule.kind.group') : e.subject;
  }
  if (record.entity === 'group_member' && e) {
    if (typeof e.member_group_id === 'string' && e.member_group_id)
      return t('history.memberNested');
    if (Array.isArray(e.predicate) && e.predicate.length) return t('history.memberPredicate');
  }
  return record.entity_id ? record.entity_id.slice(0, 8) : '';
}

export const REVERTIBLE = new Set([
  'assignment_rule',
  'group',
  'group_member',
  'role',
  'role_permission',
  'access_group',
]);

/** The change log keeps the "before" and "after" bodies; the differing top-level fields are what changed. */
export function diffFields(
  record: ChangeRecord,
): { field: string; before: unknown; after: unknown }[] {
  const before = asEntity(record.before) ?? {};
  const after = asEntity(record.after) ?? {};
  const fields = [...new Set([...Object.keys(before), ...Object.keys(after)])];
  return fields
    .filter((f) => JSON.stringify(before[f]) !== JSON.stringify(after[f]))
    .map((field) => ({ field, before: before[field], after: after[field] }));
}

export function printValue(value: unknown): string {
  if (value === null || value === undefined) return '—';
  if (typeof value === 'string') return value;
  return JSON.stringify(value);
}
