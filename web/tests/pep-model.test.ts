import { describe, expect, it } from '@jest/globals';

import type { PEPClient, PEPIssuer, PEPKey } from '@/api/admin/model';
import {
  activeKeys,
  clientProblem,
  createClientBody,
  emptyClientForm,
  emptyIssuerForm,
  formOfClient,
  formOfIssuer,
  issuerBody,
  issuerProblem,
  keyState,
  patchClientBody,
  PEP_OPERATIONS,
} from '@/features/services/pep-model';
import en from '@/i18n/locales/en.json';

const client = (over: Partial<PEPClient> = {}): PEPClient => ({
  id: 'c1',
  service_id: 's1',
  name: 'checkout',
  kind: 'api_key',
  issuer: null,
  client_id: null,
  operations: ['evaluate'],
  enabled: true,
  created_at: '2026-10-01T10:00:00Z',
  ...over,
});

const key = (over: Partial<PEPKey> = {}): PEPKey => ({
  key_id: 'k1',
  created_at: '2026-10-01T10:00:00Z',
  expires_at: null,
  revoked_at: null,
  active: true,
  ...over,
});

// The screens say why a form cannot be saved with a key they look up; a key without a text would show as the key itself.
const text = (path: string): unknown =>
  path.split('.').reduce<unknown>((node, part) => (node as Record<string, unknown>)?.[part], en);

describe('the form of a PEP client', () => {
  it('registers an API key client with the operations that were ticked and no identity', () => {
    const form = {
      ...emptyClientForm(),
      name: ' checkout ',
      operations: ['evaluate' as const, 'search_resource' as const],
    };
    expect(createClientBody(form)).toEqual({
      name: 'checkout',
      kind: 'api_key',
      operations: ['evaluate', 'search_resource'],
      enabled: true,
    });
  });

  it('registers a jwt client with its issuer and client id, and drops them for an API key client', () => {
    const jwt = {
      ...emptyClientForm(),
      kind: 'jwt' as const,
      name: 'backend',
      issuer: ' https://idp.example/realms/main ',
      clientId: ' checkout-backend ',
    };
    expect(createClientBody(jwt)).toEqual({
      name: 'backend',
      kind: 'jwt',
      operations: ['evaluate'],
      enabled: true,
      issuer: 'https://idp.example/realms/main',
      client_id: 'checkout-backend',
    });
    // Switching the kind back leaves what was typed for the other kind out of the request.
    expect(createClientBody({ ...jwt, kind: 'api_key' })).not.toHaveProperty('issuer');
  });

  it('changes a client without sending its kind', () => {
    const form = {
      ...formOfClient(client({ operations: ['evaluate', 'explain'] })),
      enabled: false,
    };
    const body = patchClientBody(form);
    expect(body).toEqual({ name: 'checkout', operations: ['evaluate', 'explain'], enabled: false });
    expect(body).not.toHaveProperty('kind');
  });

  it('reads a client back in the order the operations are listed in the form', () => {
    const form = formOfClient(client({ operations: ['explain', 'evaluate', 'unknown_one'] }));
    expect(form.operations).toEqual(['evaluate', 'explain']);
    expect(PEP_OPERATIONS).toEqual([
      'evaluate',
      'search_resource',
      'search_action',
      'search_subject',
      'explain',
    ]);
  });

  it('says what is missing, with texts that exist', () => {
    const jwt = { ...emptyClientForm(), kind: 'jwt' as const, name: 'n' };
    expect(clientProblem(emptyClientForm())).toBe('pep.problem.name');
    expect(clientProblem({ ...jwt, operations: [] })).toBe('pep.problem.operations');
    expect(clientProblem(jwt)).toBe('pep.problem.issuer');
    expect(clientProblem({ ...jwt, issuer: 'https://idp.example' })).toBe('pep.problem.clientId');
    expect(clientProblem({ ...jwt, issuer: 'https://idp.example', clientId: 'x' })).toBeNull();
    expect(clientProblem({ ...emptyClientForm(), name: 'n' })).toBeNull();
    for (const k of [
      'pep.problem.name',
      'pep.problem.operations',
      'pep.problem.issuer',
      'pep.problem.clientId',
    ])
      expect(typeof text(k)).toBe('string');
  });
});

describe('API keys', () => {
  it('tells a key that works from a revoked one and an expired one', () => {
    expect(keyState(key())).toBe('active');
    expect(keyState(key({ active: false, revoked_at: '2026-10-02T10:00:00Z' }))).toBe('revoked');
    expect(keyState(key({ active: false, expires_at: '2026-10-02T10:00:00Z' }))).toBe('expired');
    // A key revoked and expired is revoked: the revocation is what someone did.
    expect(keyState(key({ active: false, revoked_at: 'x', expires_at: 'y' }))).toBe('revoked');
  });

  it('counts the active keys only', () => {
    expect(
      activeKeys([
        key(),
        key({ key_id: 'k2', active: false, revoked_at: 'x' }),
        key({ key_id: 'k3' }),
      ]),
    ).toBe(2);
  });
});

describe('the form of a PEP issuer', () => {
  const issuer: PEPIssuer = {
    issuer: 'https://idp.example/realms/main',
    jwks_url: 'https://idp.example/realms/main/protocol/openid-connect/certs',
  };

  it('sends the defaults of the PDP by leaving the audiences and the algorithms out', () => {
    const body = issuerBody({
      ...emptyIssuerForm(),
      issuer: ` ${issuer.issuer} `,
      jwksUrl: issuer.jwks_url,
    });
    expect(body).toEqual({ ...issuer, require_typ: false });
    expect(body).not.toHaveProperty('audiences');
    expect(body).not.toHaveProperty('algs');
  });

  it('sends what was chosen, and reads an issuer back into the form', () => {
    const full: PEPIssuer = {
      ...issuer,
      audiences: ['licetum', ' pdp '],
      algs: ['RS256', 'ES256'],
      require_typ: true,
    };
    expect(issuerBody(formOfIssuer(full))).toEqual({ ...full, audiences: ['licetum', 'pdp'] });
    expect(formOfIssuer({ ...issuer, audiences: null, algs: null }).audiences).toEqual([]);
  });

  it('needs the name and an http(s) address of the keys', () => {
    expect(issuerProblem(emptyIssuerForm())).toBe('issuers.problem.issuer');
    expect(issuerProblem({ ...emptyIssuerForm(), issuer: 'x', jwksUrl: 'ftp://x' })).toBe(
      'issuers.problem.jwks',
    );
    expect(
      issuerProblem({ ...emptyIssuerForm(), issuer: 'x', jwksUrl: 'https://idp.example/keys' }),
    ).toBeNull();
    for (const k of ['issuers.problem.issuer', 'issuers.problem.jwks'])
      expect(typeof text(k)).toBe('string');
  });
});
