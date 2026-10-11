import { NextResponse, type NextRequest } from 'next/server';

import { env } from '@/lib/env';
import { readSession } from '@/lib/session';

export const dynamic = 'force-dynamic';

type Context = { params: Promise<{ path: string[] }> };

const PASS = [
  'content-type',
  'content-language',
  'www-authenticate',
  'x-authz-revision',
  'x-request-id',
];

/**
 * "Try it with your token" on the Integration tab: the browser hands a token to this route, which calls the check API
 * with it. The token is used for this one request and is not stored. Only a signed-in administrator may use it, so it
 * is not an open relay.
 */
async function handle(request: NextRequest, context: Context): Promise<NextResponse> {
  const { path } = await context.params;
  const problem = (status: number, detail: string) => NextResponse.json({ detail }, { status });

  if (!path.length || path.some((s) => !s || s === '.' || s === '..' || /[\\/]/.test(s)))
    return problem(400, 'bad path');
  if (!(await readSession())) return problem(401, 'no session');
  if (
    request.method !== 'GET' &&
    request.headers.get('origin') !== new URL(env.appBaseUrl()).origin
  ) {
    return problem(403, 'cross-origin request');
  }

  const token = request.headers
    .get('x-test-token')
    ?.replace(/^Bearer\s+/i, '')
    .trim();
  if (!token) return problem(400, 'no token');

  const headers = new Headers({ authorization: `Bearer ${token}` });
  for (const name of ['accept-language', 'content-type']) {
    const value = request.headers.get(name);
    if (value) headers.set(name, value);
  }

  let upstream: Response;
  try {
    upstream = await fetch(
      `${env.checkApiUrl()}/${path.map(encodeURIComponent).join('/')}${request.nextUrl.search}`,
      {
        method: request.method,
        headers,
        body: ['GET', 'HEAD'].includes(request.method) ? undefined : await request.arrayBuffer(),
        redirect: 'manual',
        cache: 'no-store',
      },
    );
  } catch {
    return problem(502, 'the check API does not answer');
  }
  const out = new Headers({ 'cache-control': 'no-store' });
  for (const name of PASS) {
    const value = upstream.headers.get(name);
    if (value) out.set(name, value);
  }
  return new NextResponse(upstream.body, { status: upstream.status, headers: out });
}

export { handle as GET, handle as POST };
