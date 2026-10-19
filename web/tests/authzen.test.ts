import { describe, expect, it } from '@jest/globals';

import {
  AUTHZEN_OPERATIONS,
  AUTHZEN_PATHS,
  checkResultOf,
  evaluationError,
  evaluationRequest,
  evaluationsRequest,
  parseValue,
  pdpAddress,
  pdpUrl,
  propertiesOf,
  requestOf,
  searchActionRequest,
  searchResourceRequest,
  searchSubjectRequest,
  snippet,
  type AuthzenOperation,
  type Lang,
  type Sample,
} from '@/lib/authzen';

/*
 * The oracle of these tests is the specification: the OpenID AuthZEN Authorization API 1.0 (sections 6 to 8) and the limits of the
 * PDP of Licetum (core/schema/pdp.yaml). `problems` states the rules once, apart from the builders, and every request the
 * builders make is held to it.
 */

type Json = Record<string, unknown>;
const isObject = (v: unknown): v is Json =>
  typeof v === 'object' && v !== null && !Array.isArray(v);
const bytes = (s: string) => new TextEncoder().encode(s).length;
// eslint-disable-next-line no-control-regex -- the rule is about control characters
const CONTROL = /[\u0000-\u001f\u007f]/;

function entity(
  value: unknown,
  name: string,
  needs: { id: boolean; forbidID?: boolean },
): string[] {
  if (!isObject(value)) return [`${name} is missing`];
  const out: string[] = [];
  const { type, id, properties } = value;
  if (typeof type !== 'string' || bytes(type) < 1 || bytes(type) > 64 || type === 'group')
    out.push(`${name}.type must be 1 to 64 bytes and not "group"`);
  if (needs.forbidID) {
    if ('id' in value) out.push(`${name}.id must be absent`);
  } else if (needs.id) {
    if (
      typeof id !== 'string' ||
      bytes(id) < 1 ||
      bytes(id) > 256 ||
      CONTROL.test(id) ||
      id.startsWith('group:')
    )
      out.push(`${name}.id must be 1 to 256 bytes, no control characters, not a group`);
  }
  if (properties !== undefined) {
    if (!isObject(properties) || Object.keys(properties).length > 100)
      out.push(`${name}.properties must be an object of at most 100 claims`);
    else
      for (const [k, v] of Object.entries(properties))
        if (!(typeof v === 'string' || (Array.isArray(v) && v.every((x) => typeof x === 'string'))))
          out.push(`${name}.properties.${k} must be a string or a list of strings`);
  }
  return out;
}

function action(value: unknown): string[] {
  return isObject(value) && typeof value.name === 'string' && value.name.length > 0
    ? []
    : ['action.name is missing'];
}

function context(value: unknown): string[] {
  if (value === undefined) return [];
  if (!isObject(value)) return ['context must be an object'];
  return Object.entries(value).flatMap(([k, v]) =>
    /^[A-Za-z0-9_.-]{1,64}$/.test(k) &&
    (typeof v === 'string' || (Array.isArray(v) && v.every((x) => typeof x === 'string')))
      ? []
      : [`context.${k} is not an attribute of a request`],
  );
}

function page(value: unknown): string[] {
  if (value === undefined) return [];
  if (!isObject(value)) return ['page must be an object'];
  const { limit, token } = value;
  const out: string[] = [];
  if (
    limit !== undefined &&
    !(Number.isInteger(limit) && (limit as number) >= 1 && (limit as number) <= 1000)
  )
    out.push('page.limit must be an integer from 1 to 1000');
  if (token !== undefined && typeof token !== 'string') out.push('page.token must be a string');
  return out;
}

/** Everything that is wrong with the body of the operation, by the specification; empty when the PDP accepts it. */
function problems(operation: AuthzenOperation, body: unknown): string[] {
  if (!isObject(body)) return ['the body must be an object'];
  switch (operation) {
    case 'evaluation':
      return [
        ...entity(body.subject, 'subject', { id: true }),
        ...entity(body.resource, 'resource', { id: true }),
        ...action(body.action),
        ...context(body.context),
        ...('evaluations' in body ? ['evaluations belongs to the batch'] : []),
      ];
    case 'evaluations': {
      const list = body.evaluations;
      if (!Array.isArray(list) || list.length < 1 || list.length > 100)
        return ['evaluations must hold 1 to 100 items'];
      const out = [...context(body.context)];
      const semantic = isObject(body.options) ? body.options.evaluations_semantic : undefined;
      if (
        semantic !== undefined &&
        !['execute_all', 'deny_on_first_deny', 'permit_on_first_permit'].includes(String(semantic))
      )
        out.push('options.evaluations_semantic is not one of the three');
      // An evaluation takes what it does not name from the top level of the request.
      list.forEach((item, i) => {
        if (!isObject(item)) return out.push(`evaluations[${i}] must be an object`);
        const merged = { ...body, ...item };
        delete merged.evaluations;
        delete merged.options;
        out.push(...problems('evaluation', merged).map((p) => `evaluations[${i}]: ${p}`));
      });
      return out;
    }
    case 'search_resource':
      return [
        ...entity(body.subject, 'subject', { id: true }),
        ...entity(body.resource, 'resource', { id: false, forbidID: true }),
        ...action(body.action),
        ...context(body.context),
        ...page(body.page),
      ];
    case 'search_action':
      return [
        ...entity(body.subject, 'subject', { id: true }),
        ...entity(body.resource, 'resource', { id: true }),
        ...('action' in body ? ['action must be absent from the action search'] : []),
        ...context(body.context),
        ...page(body.page),
      ];
    case 'search_subject':
      return [
        ...entity(body.subject, 'subject', { id: false, forbidID: true }),
        ...entity(body.resource, 'resource', { id: true }),
        ...action(body.action),
        ...context(body.context),
        ...page(body.page),
      ];
  }
}

const sample: Sample = { person: 'alice', type: 'branch', resource: 'B1', action: 'read' };
const rich: Sample = {
  ...sample,
  properties: { department: 'sales', teams: ['a', 'b'] },
  context: { ip: '10.0.0.1' },
};

describe('the AuthZEN request builders of the integration and simulation pages', () => {
  it('TestContract_WEB03_EveryOperationBuildsARequestTheSpecificationAccepts', () => {
    for (const operation of AUTHZEN_OPERATIONS) {
      for (const s of [sample, rich, { ...sample, resource: '' }, { ...sample, resource: '  ' }]) {
        expect(problems(operation, requestOf(operation, s))).toEqual([]);
      }
    }
  });

  it('TestContract_WEB03_ATypeAsAWholeIsAskedWithAStarAndNotWithAnEmptyId', () => {
    const request = evaluationRequest({ ...sample, resource: '' });
    expect(request.resource).toEqual({ type: 'branch', id: '*' });
    expect(searchActionRequest({ ...sample, resource: '' }).resource).toEqual({
      type: 'branch',
      id: '*',
    });
  });

  it('TestContract_WEB03_TheSearchesLeaveOutWhatTheSpecificationLeavesOut', () => {
    // The resource search names no resource id, the subject search no subject id, the action search no action.
    expect(searchResourceRequest(sample).resource).toEqual({ type: 'branch' });
    expect(searchSubjectRequest(sample).subject).toEqual({ type: 'user' });
    expect('action' in searchActionRequest(sample)).toBe(false);
    for (const [name, body] of [
      ['resource', searchResourceRequest(sample, { limit: 2, token: 'next' })],
      ['action', searchActionRequest(sample, { limit: 2, token: 'next' })],
      ['subject', searchSubjectRequest(sample, { limit: 2, token: 'next' })],
    ] as const) {
      expect(body.page).toEqual({ limit: 2, token: 'next' });
      expect(problems(`search_${name}`, body)).toEqual([]);
    }
  });

  it('TestContract_WEB03_ThePropertiesAreTheClaimsAndTheContextIsOnlyWhatWasGiven', () => {
    const request = evaluationRequest(rich);
    expect(request.subject).toEqual({
      type: 'user',
      id: 'alice',
      properties: { department: 'sales', teams: ['a', 'b'] },
    });
    expect(request.context).toEqual({ ip: '10.0.0.1' });
    // Nothing is sent that nobody typed: no empty properties, no empty context.
    const plain = evaluationRequest({ ...sample, properties: {}, context: {} });
    expect('properties' in (plain.subject ?? {})).toBe(false);
    expect('context' in plain).toBe(false);
  });

  it('TestContract_WEB03_ABatchHoldsTheSubjectOnceAndEachQuestionItsResourceAndAction', () => {
    const request = evaluationsRequest(
      { person: 'alice', properties: { department: 'sales' }, context: { ip: '10.0.0.1' } },
      [
        { type: 'branch', resource: 'B1', action: 'read' },
        { type: 'rrs', resource: '', action: 'write' },
      ],
      'deny_on_first_deny',
    );
    expect(request.evaluations).toEqual([
      { resource: { type: 'branch', id: 'B1' }, action: { name: 'read' } },
      { resource: { type: 'rrs', id: '*' }, action: { name: 'write' } },
    ]);
    expect(request.options).toEqual({ evaluations_semantic: 'deny_on_first_deny' });
    expect(problems('evaluations', request)).toEqual([]);
    expect(problems('evaluations', evaluationsRequest({ person: 'alice' }, []))).not.toEqual([]);
    // A question without an action would not be a valid evaluation: the check is not vacuous.
    expect(
      problems('evaluations', {
        subject: { type: 'user', id: 'alice' },
        evaluations: [{ resource: { type: 'a', id: 'b' } }],
      }),
    ).not.toEqual([]);
  });

  it('TestContract_WEB03_TheOracleRefusesWhatTheSpecificationRefuses', () => {
    const bad: [AuthzenOperation, unknown][] = [
      [
        'evaluation',
        {
          subject: { type: 'user', id: '' },
          resource: { type: 'a', id: 'b' },
          action: { name: 'read' },
        },
      ],
      [
        'evaluation',
        {
          subject: { type: 'user', id: 'group:x' },
          resource: { type: 'a', id: 'b' },
          action: { name: 'read' },
        },
      ],
      ['evaluation', { subject: { type: 'user', id: 'a' }, resource: { type: 'a', id: 'b' } }],
      [
        'search_resource',
        {
          subject: { type: 'user', id: 'a' },
          resource: { type: 'a', id: 'b' },
          action: { name: 'read' },
        },
      ],
      [
        'search_subject',
        {
          subject: { type: 'user', id: 'a' },
          resource: { type: 'a', id: 'b' },
          action: { name: 'read' },
        },
      ],
      [
        'search_action',
        {
          subject: { type: 'user', id: 'a' },
          resource: { type: 'a', id: 'b' },
          action: { name: 'read' },
        },
      ],
      [
        'search_resource',
        {
          subject: { type: 'user', id: 'a' },
          resource: { type: 'a' },
          action: { name: 'read' },
          page: { limit: 1001 },
        },
      ],
    ];
    for (const [operation, body] of bad) expect(problems(operation, body)).not.toEqual([]);
  });

  it('TestContract_WEB03_TheAddressesAreTheOnesOfTheDiscoveryDocument', () => {
    expect(pdpAddress('https://pdp.example/', 'shop')).toBe('https://pdp.example/service/shop');
    expect(pdpAddress('https://pdp.example', 'a b')).toBe('https://pdp.example/service/a%20b');
    // The same endpoints core/server/authzen_routes.go (authzenMetadata) publishes.
    expect(AUTHZEN_OPERATIONS.map((op) => pdpUrl('https://pdp.example', 'shop', op))).toEqual([
      'https://pdp.example/service/shop/access/v1/evaluation',
      'https://pdp.example/service/shop/access/v1/evaluations',
      'https://pdp.example/service/shop/access/v1/search/resource',
      'https://pdp.example/service/shop/access/v1/search/action',
      'https://pdp.example/service/shop/access/v1/search/subject',
    ]);
    expect(Object.keys(AUTHZEN_PATHS).sort()).toEqual([...AUTHZEN_OPERATIONS].sort());
  });

  it('TestContract_WEB03_EverySnippetCarriesTheUrlTheCredentialAndTheRequestItShows', () => {
    const langs: Lang[] = ['curl', 'go', 'ts', 'python'];
    for (const operation of AUTHZEN_OPERATIONS) {
      const url = pdpUrl('https://pdp.example', 'shop', operation);
      const body = requestOf(operation, rich);
      for (const lang of langs) {
        const code = snippet(lang, url, body);
        expect(code).toContain(url);
        expect(code.toLowerCase()).toContain('bearer');
        expect(code).not.toContain('permission');
        // The body in the code is the request, whichever way the language spells it.
        const found =
          lang === 'curl' || lang === 'go'
            ? JSON.stringify(body)
            : JSON.stringify(body, null, 2).replace(/\n/g, '\n' + (lang === 'ts' ? '  ' : '    '));
        expect(code).toContain(found);
      }
      // The command line is a valid shell string: the JSON of the curl body has no single quote to break it.
      expect(snippet('curl', url, body).match(/-d '(.*)'$/)?.[1]).toBe(JSON.stringify(body));
    }
  });
});

describe('the answers of the simulation', () => {
  const rule = {
    access: ['read'],
    anchor: { resource_id: 'D1', type: 'division' },
    applies_to: 'subject alice',
    effect: 'allow',
    exception: false,
    rule_id: '3d175b5c-2b95-43e1-a3f2-2665724264e0',
  };

  it('turns an evaluation into the decision and its explanation', () => {
    const result = checkResultOf({
      decision: true,
      context: { reason_admin: { decision: 'allowed', denials: [], granted_by: [rule] } },
    });
    expect(result.allowed).toBe(true);
    expect(result.explanation?.decision).toBe('allowed');
    expect(result.explanation?.granted_by).toHaveLength(1);
  });

  it('keeps the decision when the explanation is missing or is not one', () => {
    expect(checkResultOf({ decision: false })).toEqual({ allowed: false });
    expect(checkResultOf({ decision: false, context: { reason_admin: 'nonsense' } })).toEqual({
      allowed: false,
    });
    expect(
      checkResultOf({ decision: false, context: { reason_admin: { decision: 'denied' } } }),
    ).toEqual({ allowed: false });
  });

  it('tells an evaluation that could not be decided from one that was denied', () => {
    expect(
      evaluationError({ decision: false, context: { error: { status: 400, message: 'bad' } } }),
    ).toBe('bad');
    expect(evaluationError({ decision: false })).toBeUndefined();
    expect(evaluationError({ decision: false, context: { error: 'x' } })).toBeUndefined();
  });
});

describe('the claims and the attributes a person types', () => {
  it('reads [a, b] as a list and anything else as text', () => {
    expect(parseValue(' sales ')).toBe('sales');
    expect(parseValue('[a, b]')).toEqual(['a', 'b']);
    expect(parseValue('["a","b"]')).toEqual(['a', 'b']);
    expect(parseValue('[1, 2]')).toEqual(['1', '2']);
    expect(parseValue('[]')).toEqual([]);
  });

  it('drops the pairs without a name', () => {
    expect(
      propertiesOf([
        { key: 'department', value: 'sales' },
        { key: ' ', value: 'x' },
        { key: 'teams', value: '[a, b]' },
      ]),
    ).toEqual({ department: 'sales', teams: ['a', 'b'] });
  });
});
