import { NextResponse, type NextRequest } from 'next/server';

import { refreshIfNeeded } from '@/lib/admin-api';
import { env } from '@/lib/env';
import { clearSession, readSession, writeSession } from '@/lib/session';

export const dynamic = 'force-dynamic';

type Context = { params: Promise<{ path: string[] }> };

/** Response headers worth passing on to the browser. */
const PASS = [
  'content-type',
  'content-language',
  'www-authenticate',
  'x-authz-revision',
  'x-request-id',
];

const problem = (status: number, detail: string) =>
  NextResponse.json({ detail, extra: { type: 'generic', code: 'proxy_error' } }, { status });

/**
 * The browser talks to the admin API only through this route: the server attaches the access token from the session
 * cookie (renewing it when it is about to expire), so the token never reaches the page's JavaScript.
 */
async function handle(request: NextRequest, context: Context): Promise<NextResponse> {
  const { path } = await context.params;
  // The path is appended to a fixed base URL: no traversal, no sign-in endpoints (they are /auth/*).
  if (!path.length || path.some((s) => !s || s === '.' || s === '..' || /[\\/]/.test(s))) {
    return problem(400, 'bad path');
  }
  if (path.includes('oauth'))
    return problem(403, 'sign-in endpoints are not available through the proxy');

  // A state-changing request must come from this application's own pages.
  if (!['GET', 'HEAD'].includes(request.method)) {
    if (request.headers.get('origin') !== new URL(env.appBaseUrl()).origin) {
      return problem(403, 'cross-origin request');
    }
  }

  const stored = await readSession();
  if (!stored) return problem(401, 'no session');
  const { session, renewed } = await refreshIfNeeded(stored);
  if (!session) {
    const gone = problem(401, 'the session has expired');
    clearSession(gone.cookies);
    return gone;
  }

  const url = `${env.adminApiUrl()}/${path.map(encodeURIComponent).join('/')}${request.nextUrl.search}`;
  const headers = new Headers({ authorization: `Bearer ${session.accessToken}` });
  for (const name of ['accept', 'accept-language', 'content-type', 'x-request-id']) {
    const value = request.headers.get(name);
    if (value) headers.set(name, value);
  }

  let upstream: Response;
  try {
    upstream = await fetch(url, {
      method: request.method,
      headers,
      body: ['GET', 'HEAD'].includes(request.method) ? undefined : await request.arrayBuffer(),
      redirect: 'manual',
      cache: 'no-store',
    });
  } catch {
    return problem(502, 'the admin API does not answer');
  }

  const out = new Headers({ 'cache-control': 'no-store' });
  for (const name of PASS) {
    const value = upstream.headers.get(name);
    if (value) out.set(name, value);
  }
  const response = new NextResponse(upstream.status === 204 ? null : upstream.body, {
    status: upstream.status,
    headers: out,
  });
  if (renewed) await writeSession(response.cookies, session);
  return response;
}

export {
  handle as GET,
  handle as POST,
  handle as PUT,
  handle as PATCH,
  handle as DELETE,
  handle as HEAD,
};
