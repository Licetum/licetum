import { describe, expect, it } from '@jest/globals';

import {
  buildInputs,
  emptyForm,
  newCond,
  problemOf,
  type GrantForm,
} from '@/features/grant/grant-model';

const form = (patch: Partial<GrantForm> = {}): GrantForm => ({
  ...emptyForm(),
  groupId: 'g1',
  serviceId: 's1',
  typeId: 't1',
  ...patch,
});

describe('the "give access" form', () => {
  it('says what is missing, in the order the form is filled in', () => {
    expect(problemOf(emptyForm(), { hasSource: true })?.key).toBe('grant.problem.group');
    expect(problemOf(form({ typeId: undefined }), { hasSource: true })?.key).toBe(
      'grant.problem.type',
    );
    expect(problemOf(form({ levels: [] }), { hasSource: true })?.key).toBe('grant.problem.levels');
    expect(problemOf(form({ scope: 'resources' }), { hasSource: true })?.key).toBe(
      'grant.problem.resources',
    );
    expect(
      problemOf(form({ scope: 'resources', resourceIds: ['d1'] }), { hasSource: false })?.key,
    ).toBe('grant.problem.source');
    expect(problemOf(form(), { hasSource: false })).toBeNull();
  });

  it('never gives access to everyone without a confirmation', () => {
    expect(problemOf(form({ who: 'everyone' }), { hasSource: true })?.key).toBe(
      'grant.problem.everyone',
    );
    expect(
      problemOf(form({ who: 'everyone', everyoneConfirmed: true }), { hasSource: true }),
    ).toBeNull();
  });

  it('refuses a start date after the end date', () => {
    expect(
      problemOf(form({ validFrom: '2026-12-01', expiresAt: '2026-11-01' }), { hasSource: true })
        ?.key,
    ).toBe('grant.problem.dates');
  });

  it('builds one rule for the whole type', () => {
    const [input, ...rest] = buildInputs(
      form({ levels: ['read', 'write'], accessGroups: ['editor'] }),
    );
    expect(rest).toHaveLength(0);
    expect(input).toMatchObject({
      member_of_group_id: 'g1',
      type_resource_id: 't1',
      access: ['read', 'write', '@editor'],
      effect: 'allow',
      enabled: true,
    });
    expect(input).not.toHaveProperty('resource_id');
  });

  it('builds one rule per chosen resource', () => {
    const inputs = buildInputs(form({ scope: 'resources', resourceIds: ['d1', 'd2'] }));
    expect(inputs.map((i) => i.resource_id)).toEqual(['d1', 'd2']);
  });

  it('a denial carries the missing-attribute choice and can not be an exception', () => {
    const [input] = buildInputs(
      form({ deny: true, exception: true, onMissing: 'apply', levels: ['*'] }),
    );
    expect(input).toMatchObject({
      effect: 'deny',
      exception: false,
      on_missing_attribute: 'apply',
      access: ['*'],
    });
  });

  it('a person or a condition replaces the group', () => {
    const [person] = buildInputs(form({ who: 'person', person: ' alice ' }));
    expect(person).toMatchObject({ subject: 'alice', subject_kind: 'user' });
    expect(person).not.toHaveProperty('member_of_group_id');
    const cond = { ...newCond(), attr: 'department', op: 'in' as const, value: 'sales, legal' };
    const [byCondition] = buildInputs(form({ who: 'condition', whoConds: [cond] }));
    expect(byCondition?.predicate).toEqual([
      { attr: 'department', op: 'in', source: 'claim', value: ['sales', 'legal'] },
    ]);
  });

  it('a role is granted whole, without levels or a type', () => {
    const [input] = buildInputs(form({ what: 'role', roleId: 'role-1' }));
    expect(input).toMatchObject({ role_id: 'role-1', member_of_group_id: 'g1' });
    expect(input).not.toHaveProperty('access');
    expect(input).not.toHaveProperty('type_resource_id');
  });
});
