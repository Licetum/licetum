import { afterEach, beforeAll, beforeEach, describe, expect, it, jest } from '@jest/globals';
import { NextRequest } from 'next/server';

const APP = 'https://licetum.example';

beforeAll(() => {
  process.env.SESSION_SECRET = 'x'.repeat(40);
  process.env.APP_BASE_URL = APP;
  process.env.OIDC_AUTHORIZATION_URL = 'https://idp.example/dex/auth';
  process.env.OIDC_TOKEN_URL = 'http://dex:5556/dex/token';
  process.env.OIDC_CLIENT_ID = 'licetum-web';
});

const b64 = (value: unknown) => Buffer.from(JSON.stringify(value)).toString('base64url');
/** A JWT-shaped token; the signature is not looked at here, the admin API verifies it. */
const jwt = (claims: Record<string, unknown>) => `${b64({ alg: 'RS256' })}.${b64(claims)}.sig`;

const tokenResponse = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { 'content-type': 'application/json' } });

describe('the sealed state of a sign-in', () => {
  it('TestContract_WEB01_TheStateTheNonceAndTheVerifierLiveInASealedShortLivedCookie', async () => {
    const { newFlow, sealFlow, unsealFlow, pkceChallenge, authorizationUrl } =
      await import('@/lib/oidc');
    const { seal } = await import('@/lib/session');
    const now = Date.now();
    const flow = newFlow('/services', now);

    // Entropy: a state and a nonce of 256 bits, a verifier within the 43..128 characters of RFC 7636, all different.
    expect(flow.state).toHaveLength(43);
    expect(flow.nonce).toHaveLength(43);
    expect(flow.verifier.length).toBeGreaterThanOrEqual(43);
    expect(flow.verifier.length).toBeLessThanOrEqual(128);
    expect(new Set([flow.state, flow.nonce, flow.verifier]).size).toBe(3);
    expect(newFlow('/', now).state).not.toBe(flow.state);
    expect(flow.exp - now).toBe(10 * 60_000);

    // Sealed: the cookie shows none of it, and reads back only with the secret.
    const sealed = await sealFlow(flow);
    for (const secret of [flow.state, flow.nonce, flow.verifier])
      expect(sealed).not.toContain(secret);
    expect(await unsealFlow(sealed, now)).toEqual(flow);
    process.env.SESSION_SECRET = 'y'.repeat(40);
    expect(await unsealFlow(sealed, now)).toBeNull();
    process.env.SESSION_SECRET = 'x'.repeat(40);

    // Short-lived: a sign-in that has run out is no sign-in.
    expect(await unsealFlow(sealed, flow.exp + 1)).toBeNull();
    expect(await unsealFlow(undefined)).toBeNull();
    expect(await unsealFlow('garbage')).toBeNull();

    // Not interchangeable with the session cookie, in either direction.
    const session = await seal({ accessToken: 'a.b.c', accessExpiresAt: now + 60_000 });
    expect(await unsealFlow(session, now)).toBeNull();
    const { unseal } = await import('@/lib/session');
    expect(await unseal(sealed)).toBeNull();

    // The authorization request: the code flow with a S256 challenge, the state and the nonce; the verifier stays home.
    const url = new URL(authorizationUrl(flow));
    expect(url.origin + url.pathname).toBe('https://idp.example/dex/auth');
    expect(Object.fromEntries(url.searchParams)).toMatchObject({
      response_type: 'code',
      client_id: 'licetum-web',
      redirect_uri: `${APP}/auth/callback`,
      state: flow.state,
      nonce: flow.nonce,
      code_challenge: pkceChallenge(flow.verifier),
      code_challenge_method: 'S256',
    });
    expect(url.searchParams.get('scope')).toContain('offline_access');
    expect(url.toString()).not.toContain(flow.verifier);
  });

  it('computes the challenge as RFC 7636 appendix B does', async () => {
    const { pkceChallenge } = await import('@/lib/oidc');
    expect(pkceChallenge('dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk')).toBe(
      'E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM',
    );
  });
});

describe('the login route', () => {
  it('TestContract_WEB01_TheLoginRouteSealsTheFlowForThisBrowserAndSendsOnlyAPathOfOursBack', async () => {
    const { GET } = await import('@/app/auth/login/route');
    const { unsealFlow, FLOW_COOKIE } = await import('@/lib/oidc');

    const response = await GET(
      new NextRequest(`${APP}/auth/login?returnTo=${encodeURIComponent('//evil.example/x')}`),
    );
    expect(response.status).toBe(307);
    const location = new URL(response.headers.get('location')!);
    expect(location.origin + location.pathname).toBe('https://idp.example/dex/auth');

    const cookie = response.cookies.get(FLOW_COOKIE)!;
    expect(cookie).toMatchObject({
      httpOnly: true,
      sameSite: 'lax',
      secure: true,
      path: '/auth',
      maxAge: 600,
    });
    const flow = (await unsealFlow(cookie.value))!;
    expect(flow.returnTo).toBe('/'); // an address outside this application is no place to return to
    expect(location.searchParams.get('state')).toBe(flow.state);
    expect(location.searchParams.get('nonce')).toBe(flow.nonce);

    const inside = await GET(
      new NextRequest(`${APP}/auth/login?returnTo=${encodeURIComponent('/services?x=1')}`),
    );
    expect((await unsealFlow(inside.cookies.get(FLOW_COOKIE)!.value))!.returnTo).toBe(
      '/services?x=1',
    );
  });

  it('says the sign-in is not configured when the OIDC settings are missing', async () => {
    const { GET } = await import('@/app/auth/login/route');
    const saved = process.env.OIDC_CLIENT_ID;
    process.env.OIDC_CLIENT_ID = '';
    try {
      const response = await GET(new NextRequest(`${APP}/auth/login`));
      expect(response.headers.get('location')).toBe(`${APP}/login/error?code=not_configured`);
      expect(response.cookies.get('licetum_flow')).toBeUndefined();
    } finally {
      process.env.OIDC_CLIENT_ID = saved;
    }
  });
});

describe('the callback route', () => {
  let fetchMock: jest.SpiedFunction<typeof fetch>;
  beforeEach(() => {
    fetchMock = jest.spyOn(globalThis, 'fetch');
  });
  afterEach(() => {
    fetchMock.mockRestore();
    delete process.env.OIDC_CLIENT_SECRET;
  });

  /** A sign-in under way: the flow of a browser and its cookie. */
  async function started(returnTo = '/groups') {
    const { newFlow, sealFlow, FLOW_COOKIE } = await import('@/lib/oidc');
    const flow = newFlow(returnTo);
    return { flow, cookie: `${FLOW_COOKIE}=${await sealFlow(flow)}` };
  }
  const callback = async (query: string, cookie?: string) => {
    const { GET } = await import('@/app/auth/callback/route');
    return GET(
      new NextRequest(
        `${APP}/auth/callback?${query}`,
        cookie ? { headers: { cookie } } : undefined,
      ),
    );
  };
  const failedWith = (response: Response, code: string) => {
    expect(response.status).toBe(307);
    expect(response.headers.get('location')).toBe(`${APP}/login/error?code=${code}`);
  };

  it('TestContract_WEB01_TheCallbackNeedsTheFlowOfThisBrowserAndItsState', async () => {
    const { flow, cookie } = await started();
    failedWith(await callback(`code=c&state=${flow.state}`), 'state'); // no cookie: not started here
    failedWith(await callback(`code=c&state=${'x'.repeat(43)}`, cookie), 'state'); // not the state that was sent
    failedWith(await callback('code=c', cookie), 'state'); // no state
    failedWith(await callback(`state=${flow.state}`, cookie), 'state'); // no code
    failedWith(await callback('code=c&state=s', 'licetum_flow=garbage'), 'state');
    failedWith(await callback(`error=access_denied&state=${flow.state}`, cookie), 'denied');
    failedWith(await callback(`error=server_error&state=${flow.state}`, cookie), 'idp_error');
    expect(fetchMock).not.toHaveBeenCalled(); // nothing was exchanged for any of them
  });

  it('exchanges the code with the verifier, opens the session and returns to the page', async () => {
    const { flow, cookie } = await started('/groups');
    process.env.OIDC_CLIENT_SECRET = 'p@ss:word';
    const access = jwt({ sub: 'u', preferred_username: 'admin' });
    fetchMock.mockResolvedValueOnce(
      tokenResponse({
        access_token: access,
        token_type: 'bearer',
        expires_in: 300,
        refresh_token: 'refresh-1',
        id_token: jwt({ nonce: flow.nonce }),
      }),
    );

    const response = await callback(`code=the-code&state=${flow.state}`, cookie);
    expect(response.status).toBe(307);
    expect(response.headers.get('location')).toBe(`${APP}/groups`);

    const [url, init] = fetchMock.mock.calls[0]!;
    expect(String(url)).toBe('http://dex:5556/dex/token');
    const body = new URLSearchParams(String((init as RequestInit).body));
    expect(Object.fromEntries(body)).toMatchObject({
      grant_type: 'authorization_code',
      code: 'the-code',
      redirect_uri: `${APP}/auth/callback`,
      client_id: 'licetum-web',
      code_verifier: flow.verifier,
    });
    const headers = (init as RequestInit).headers as Record<string, string>;
    expect(headers.authorization).toBe(
      `Basic ${Buffer.from('licetum-web:p%40ss%3Aword').toString('base64')}`,
    );

    // The session is a sealed cookie, and the flow is over.
    const { readSessionFrom } = await import('@/lib/session');
    const session = await readSessionFrom({ get: (name) => response.cookies.get(name) });
    expect(session).toMatchObject({ accessToken: access, refreshToken: 'refresh-1' });
    expect(response.cookies.get('licetum_flow')?.value ?? '').toBe('');
    expect(
      response.cookies
        .getAll()
        .map((c) => c.value)
        .join(''),
    ).not.toContain(access);
  });

  it('is a public client without a secret: PKCE alone, no Authorization header', async () => {
    const { flow, cookie } = await started();
    fetchMock.mockResolvedValueOnce(
      tokenResponse({ access_token: jwt({ sub: 'u' }), expires_in: 60 }),
    );
    const response = await callback(`code=c&state=${flow.state}`, cookie);
    expect(response.headers.get('location')).toBe(`${APP}/groups`);
    const headers = (fetchMock.mock.calls[0]![1] as RequestInit).headers as Record<string, string>;
    expect(headers.authorization).toBeUndefined();
  });

  it('does not open a session for an answer it cannot use', async () => {
    const answers: Array<[() => Promise<Response> | Response, string]> = [
      [() => Promise.reject(new Error('down')), 'idp_unreachable'], // an identity provider that does not answer
      [() => tokenResponse({ error: 'invalid_grant' }, 400), 'invalid_response'], // an error answer
      [() => tokenResponse({ access_token: 'opaque' }), 'invalid_response'], // an opaque access token
      [() => tokenResponse({ access_token: jwt({}), token_type: 'mac' }), 'invalid_response'], // another type
      [() => new Response('<html>', { status: 200 }), 'invalid_response'], // not JSON
    ];
    for (const [answer, code] of answers) {
      const { flow, cookie } = await started();
      fetchMock.mockImplementationOnce(async () => answer());
      const response = await callback(`code=c&state=${flow.state}`, cookie);
      failedWith(response, code);
      expect(response.cookies.get('licetum_session.0')).toBeUndefined();
    }
  });

  it('refuses an ID token whose nonce is not the one that was sent', async () => {
    const { flow, cookie } = await started();
    fetchMock.mockResolvedValueOnce(
      tokenResponse({ access_token: jwt({ sub: 'u' }), id_token: jwt({ nonce: 'someone-else' }) }),
    );
    const response = await callback(`code=c&state=${flow.state}`, cookie);
    failedWith(response, 'invalid_response');
    expect(response.cookies.get('licetum_session.0')).toBeUndefined();
  });

  it('keeps the sign-in of one browser from finishing in another: the flow cookie is the only way back', async () => {
    const first = await started();
    const second = await started();
    // The state of the first with the cookie of the second.
    failedWith(await callback(`code=c&state=${first.flow.state}`, second.cookie), 'state');
    expect(fetchMock).not.toHaveBeenCalled();
  });
});

describe('renewing the tokens', () => {
  it('tells a spent refresh token from an identity provider that does not answer', async () => {
    const { refreshTokens } = await import('@/lib/oidc');
    const answer = (response: Response | Error) =>
      jest.fn<typeof fetch>().mockImplementation(async () => {
        if (response instanceof Error) throw response;
        return response;
      });
    const access = jwt({ sub: 'u' });

    const renewed = await refreshTokens(
      'r1',
      answer(tokenResponse({ access_token: access, refresh_token: 'r2', expires_in: 300 })),
    );
    expect(renewed).toMatchObject({
      kind: 'renewed',
      tokens: { access_token: access, refresh_token: 'r2' },
    });

    expect(
      await refreshTokens('r1', answer(tokenResponse({ error: 'invalid_grant' }, 400))),
    ).toEqual({ kind: 'invalid_grant' });
    expect(
      await refreshTokens('r1', answer(tokenResponse({ error: 'invalid_client' }, 401))),
    ).toEqual({ kind: 'unavailable' });
    expect(await refreshTokens('r1', answer(tokenResponse({}, 503)))).toEqual({
      kind: 'unavailable',
    });
    expect(await refreshTokens('r1', answer(new Error('down')))).toEqual({ kind: 'unavailable' });
    expect(await refreshTokens('r1', answer(tokenResponse({ access_token: 'opaque' })))).toEqual({
      kind: 'unavailable',
    });

    const fetchMock = answer(tokenResponse({ access_token: access }));
    await refreshTokens('r1', fetchMock);
    const body = new URLSearchParams(String((fetchMock.mock.calls[0]![1] as RequestInit).body));
    expect(Object.fromEntries(body)).toEqual({
      grant_type: 'refresh_token',
      refresh_token: 'r1',
      client_id: 'licetum-web',
    });
  });
});

describe('the routes that act for the signed-in person', () => {
  it('TestContract_WEB01_TheLogoutAndTheProxyRefuseOtherOriginsAndBadPaths', async () => {
    const { POST: logout } = await import('@/app/auth/logout/route');
    const { POST: proxy, GET: proxyGet } = await import('@/app/api/admin/[...path]/route');

    // Logout is a state change: only this application's own pages may ask for it.
    const foreign = await logout(
      new NextRequest(`${APP}/auth/logout`, {
        method: 'POST',
        headers: { origin: 'https://evil.example' },
      }),
    );
    expect(foreign.status).toBe(403);
    const own = await logout(
      new NextRequest(`${APP}/auth/logout`, { method: 'POST', headers: { origin: APP } }),
    );
    expect(own.status).toBe(200);

    const call = (method: 'GET' | 'POST', path: string[], origin?: string) => {
      const request = new NextRequest(`${APP}/api/admin/${path.join('/')}`, {
        method,
        headers: origin ? { origin } : {},
      });
      return (method === 'GET' ? proxyGet : proxy)(request, { params: Promise.resolve({ path }) });
    };
    // The path is appended to the admin API's address: nothing can climb out of it or reach the sign-in endpoints.
    for (const path of [['..', 'x'], ['.'], ['a', '..', 'b'], ['a/b'], ['a\\b'], ['']]) {
      expect((await call('GET', path)).status).toBe(400);
    }
    expect((await call('GET', ['service', 'x', 'oauth', 'login'])).status).toBe(403);
    // A state-changing request from another origin, or from none, is refused before the session is looked at.
    expect((await call('POST', ['service'], 'https://evil.example')).status).toBe(403);
    expect((await call('POST', ['service'])).status).toBe(403);
  });
});
