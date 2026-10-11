import { describe, expect, it } from 'vitest';

import { personOf, personPredicate } from '@/domain/members';
import {
  describeConditions,
  hasId,
  ruleDecision,
  ruleStatus,
  ruleToInput,
  type Rule,
} from '@/domain/rule';
import { safeReturnTo } from '@/lib/safe-return';

const DAY = 86_400_000;
const rule = (patch: Partial<Rule> = {}): Rule =>
  ({
    access: ['read'],
    description: null,
    effect: 'allow',
    enabled: true,
    exception: false,
    expires_at: null,
    id: 'r1',
    member_of_group_id: '00000000-0000-0000-0000-000000000000',
    name: null,
    on_missing_attribute: 'skip',
    predicate: null,
    requires_group_id: null,
    requires_predicate: [],
    resource_from_claim: null,
    resource_id: null,
    resource_where: null,
    role_id: null as unknown as string,
    role_name: null,
    service_id: 's1',
    service_name: 'Docs',
    subject: 'alice',
    subject_kind: 'user',
    target_valid: true,
    type_resource_id: 't1',
    type_resource_name: 'Document',
    valid_from: null,
    ...patch,
  }) as Rule;

describe('rules', () => {
  it('treats the zero UUID and null as "not set"', () => {
    expect(hasId(null)).toBe(false);
    expect(hasId('00000000-0000-0000-0000-000000000000')).toBe(false);
    expect(hasId('9a361094-8516-4491-b2de-64ab94eda94b')).toBe(true);
  });

  it('tells the decision of a rule', () => {
    expect(ruleDecision(rule())).toBe('allow');
    expect(ruleDecision(rule({ effect: 'deny' }))).toBe('deny');
    expect(ruleDecision(rule({ exception: true }))).toBe('except');
  });

  it('computes the status from the switch, the dates and the target', () => {
    const now = Date.parse('2026-10-10T12:00:00Z');
    expect(ruleStatus(rule(), now).status).toBe('active');
    expect(ruleStatus(rule({ enabled: false }), now).status).toBe('disabled');
    expect(ruleStatus(rule({ target_valid: false }), now).status).toBe('dead');
    expect(ruleStatus(rule({ expires_at: new Date(now - DAY).toISOString() }), now).status).toBe(
      'expired',
    );
    expect(ruleStatus(rule({ valid_from: new Date(now + DAY).toISOString() }), now).status).toBe(
      'scheduled',
    );
    expect(ruleStatus(rule({ expires_at: new Date(now + 3 * DAY).toISOString() }), now)).toEqual({
      status: 'expiring',
      days: 3,
    });
    expect(
      ruleStatus(rule({ expires_at: new Date(now + 30 * DAY).toISOString() }), now).status,
    ).toBe('active');
  });

  it('writes a condition the way people read it', () => {
    expect(describeConditions([{ attr: 'department', op: 'eq', value: 'sales' }])).toBe(
      'department = sales',
    );
    expect(
      describeConditions([{ attr: 'ip', op: 'in_cidr', source: 'context', value: ['10.0.0.0/8'] }]),
    ).toBe('request.ip ⊂ [10.0.0.0/8]');
  });

  it('turns a rule back into the body that writes it (null ids are left out)', () => {
    const input = ruleToInput(rule({ resource_id: 'd1', name: 'Docs' }));
    expect(input).toMatchObject({
      type_resource_id: 't1',
      access: ['read'],
      resource_id: 'd1',
      subject: 'alice',
      name: 'Docs',
      effect: 'allow',
    });
    expect(input).not.toHaveProperty('member_of_group_id');
    expect(input).not.toHaveProperty('role_id');
    const role = ruleToInput(
      rule({ role_id: 'role-1', type_resource_id: null as unknown as string, access: null }),
    );
    expect(role).toMatchObject({ role_id: 'role-1' });
    expect(role).not.toHaveProperty('access');
    expect(role).not.toHaveProperty('type_resource_id');
  });

  it('keeps the missing-attribute choice only on denials', () => {
    expect(ruleToInput(rule({ effect: 'deny', on_missing_attribute: 'apply' }))).toMatchObject({
      on_missing_attribute: 'apply',
    });
    expect(ruleToInput(rule())).not.toHaveProperty('on_missing_attribute');
  });
});

describe('members', () => {
  it('a person is a membership by preferred_username', () => {
    expect(personOf(personPredicate(' erin '))).toBe('erin');
    expect(personOf([{ attr: 'department', op: 'eq', value: 'sales' }])).toBeNull();
    expect(personOf([{ attr: 'preferred_username', op: 'in', value: ['a', 'b'] }])).toBeNull();
    expect(personOf(null)).toBeNull();
  });
});

describe('safeReturnTo', () => {
  it('only lets the person return to a page of this application', () => {
    expect(safeReturnTo('/access?x=1')).toBe('/access?x=1');
    expect(safeReturnTo('https://evil.example/')).toBe('/');
    expect(safeReturnTo('//evil.example/')).toBe('/');
    expect(safeReturnTo('/\\evil.example')).toBe('/');
    expect(safeReturnTo('/auth/login')).toBe('/');
    expect(safeReturnTo('/login')).toBe('/');
    expect(safeReturnTo('/api/admin/service')).toBe('/');
    expect(safeReturnTo(null)).toBe('/');
  });
});
