import { afterEach, describe, expect, it } from '@jest/globals';

import {
  discoverQuerySchema,
  discoveryDocumentSchema,
  policyDocumentShapeSchema,
  policyTestFileSchema,
  sessionHandoffSchema,
  tokenAnswerSchema,
} from '@/lib/schemas';

describe('token answers', () => {
  it('accepts the numbers as numbers or as text (a URL fragment gives text)', () => {
    const answer = tokenAnswerSchema.parse({
      access_token: 'a',
      expires_in: '300',
      refresh_expires_in: 3600,
    });
    expect(answer).toMatchObject({ expires_in: '300', refresh_expires_in: 3600 });
  });

  it('refuses an answer without an access token', () => {
    expect(tokenAnswerSchema.safeParse({ expires_in: 300 }).success).toBe(false);
    expect(tokenAnswerSchema.safeParse(null).success).toBe(false);
  });

  it('opens a session only for something that looks like a JWT', () => {
    expect(sessionHandoffSchema.safeParse({ access_token: 'a.b.c' }).success).toBe(true);
    expect(sessionHandoffSchema.safeParse({ access_token: 'opaque' }).success).toBe(false);
    expect(sessionHandoffSchema.safeParse({ access_token: 'a.b' }).success).toBe(false);
  });
});

describe('identity provider discovery', () => {
  it('takes only http(s) addresses', () => {
    expect(
      discoverQuerySchema.safeParse({ issuer: ' https://idp.example.com/realm ' }).success,
    ).toBe(true);
    expect(discoverQuerySchema.safeParse({ issuer: 'file:///etc/passwd' }).success).toBe(false);
    expect(discoverQuerySchema.safeParse({ issuer: 'javascript:alert(1)' }).success).toBe(false);
    expect(discoverQuerySchema.safeParse({ issuer: 'not a url' }).success).toBe(false);
    expect(discoverQuerySchema.safeParse({ issuer: '' }).success).toBe(false);
  });

  it('cuts the document down to the four fields and ignores values of the wrong type', () => {
    const found = discoveryDocumentSchema.parse({
      issuer: 'https://idp.example.com',
      authorization_endpoint: 'https://idp.example.com/auth',
      token_endpoint: 42,
      jwks_uri: 'https://idp.example.com/keys',
      userinfo_endpoint: 'https://idp.example.com/me',
    });
    expect(found).toEqual({
      issuer: 'https://idp.example.com',
      authorization_url: 'https://idp.example.com/auth',
      token_url: undefined,
      jwks_url: 'https://idp.example.com/keys',
    });
  });
});

describe('what a person uploads', () => {
  const scenario = {
    name: 'alice reads documents',
    subject: 'alice',
    claims: { preferred_username: 'alice' },
    checks: [{ type: 'Document', action: 'read', expect: 'allow' }],
  };

  it('accepts a file of scenarios', () => {
    expect(policyTestFileSchema.parse({ cases: [scenario] }).cases).toHaveLength(1);
  });

  it('refuses a scenario without checks, with an unknown expectation, or a file that is not a list', () => {
    expect(policyTestFileSchema.safeParse({ cases: [{ ...scenario, checks: [] }] }).success).toBe(
      false,
    );
    expect(
      policyTestFileSchema.safeParse({
        cases: [{ ...scenario, checks: [{ type: 'D', action: 'read', expect: 'maybe' }] }],
      }).success,
    ).toBe(false);
    expect(policyTestFileSchema.safeParse({ cases: 'x' }).success).toBe(false);
    expect(policyTestFileSchema.safeParse([scenario]).success).toBe(false);
  });

  it('checks the envelope of a policy document and leaves the rest to the server', () => {
    expect(
      policyDocumentShapeSchema.safeParse({
        format: 'licetum.policy/v1',
        service: 'docs',
        rules: [],
      }).success,
    ).toBe(true);
    expect(policyDocumentShapeSchema.safeParse({ rules: [] }).success).toBe(false);
    expect(policyDocumentShapeSchema.safeParse([]).success).toBe(false);
  });
});

// Jest has no vi.stubEnv: the environment is replaced for each test and restored afterwards.
const realEnv = process.env;
const stubEnv = (name: string, value: string) => {
  process.env = { ...process.env, [name]: value };
};

describe('settings', () => {
  afterEach(() => {
    process.env = realEnv;
  });

  it('falls back to the local development addresses and cuts trailing slashes', async () => {
    const { env } = await import('@/lib/env');
    stubEnv('ADMIN_API_URL', '');
    stubEnv('ADMIN_API_PUBLIC_URL', '');
    stubEnv('APP_BASE_URL', 'https://licetum.example.com//');
    expect(env.adminApiUrl()).toBe('http://localhost:8002');
    expect(env.adminApiPublicUrl()).toBe('http://localhost:8002');
    expect(env.appBaseUrl()).toBe('https://licetum.example.com');
    expect(env.secureCookies()).toBe(true);
  });

  it('refuses an address that is not http(s) and a short session secret', async () => {
    const { env } = await import('@/lib/env');
    stubEnv('ADMIN_API_URL', 'ftp://admin');
    expect(() => env.adminApiUrl()).toThrow(/ADMIN_API_URL/);
    stubEnv('SESSION_SECRET', 'short');
    expect(() => env.sessionSecret()).toThrow(/at least 32/);
    stubEnv('SESSION_SECRET', '');
    expect(() => env.sessionSecret()).toThrow(/SESSION_SECRET/);
  });
});
