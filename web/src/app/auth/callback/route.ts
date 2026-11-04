import { NextResponse, type NextRequest } from 'next/server';

import { sessionFromTokens } from '@/lib/admin-api';
import { env } from '@/lib/env';
import {
  exchangeCode,
  FLOW_COOKIE,
  FLOW_PATH,
  OidcError,
  sameSecret,
  unsealFlow,
} from '@/lib/oidc';
import { safeReturnTo } from '@/lib/safe-return';
import { writeSession } from '@/lib/session';

export const dynamic = 'force-dynamic';

/**
 * The identity provider sends the browser back here with an authorization code. The code is exchanged for the tokens
 * on the server (with the PKCE verifier of this sign-in), the tokens go into the encrypted httpOnly session cookie and
 * nowhere else, and the browser returns to the page it came from.
 */
export async function GET(request: NextRequest) {
  const base = env.appBaseUrl();
  const fail = (code: string) => {
    const response = NextResponse.redirect(`${base}/login/error?code=${code}`);
    response.cookies.delete({ name: FLOW_COOKIE, path: FLOW_PATH });
    response.headers.set('cache-control', 'no-store');
    return response;
  };

  // A sign-in that this browser did not start (or that has run out) has no flow: the return is refused.
  const flow = await unsealFlow(request.cookies.get(FLOW_COOKIE)?.value);
  if (!flow) return fail('state');

  const params = request.nextUrl.searchParams;
  const error = params.get('error');
  if (error) return fail(error === 'access_denied' ? 'denied' : 'idp_error');

  const code = params.get('code');
  const state = params.get('state');
  if (!code || !state || !sameSecret(state, flow.state)) return fail('state');

  let session;
  try {
    session = sessionFromTokens(await exchangeCode(code, flow));
  } catch (e) {
    return fail(e instanceof OidcError ? e.failure : 'unknown');
  }

  const response = NextResponse.redirect(new URL(safeReturnTo(flow.returnTo), base));
  response.headers.set('cache-control', 'no-store');
  try {
    await writeSession(response.cookies, session);
  } catch {
    // The tokens are longer than the cookies hold.
    return fail('unknown');
  }
  response.cookies.delete({ name: FLOW_COOKIE, path: FLOW_PATH });
  return response;
}
