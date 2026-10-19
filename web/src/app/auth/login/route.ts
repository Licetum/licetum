import { NextResponse, type NextRequest } from 'next/server';

import { env } from '@/lib/env';
import { authorizationUrl, FLOW_COOKIE, FLOW_PATH, newFlow, sealFlow } from '@/lib/oidc';
import { safeReturnTo } from '@/lib/safe-return';

export const dynamic = 'force-dynamic';

/**
 * Starts the sign-in: this server is the OpenID Connect client, so it sends the browser to the identity provider
 * itself (code flow with PKCE). The state, the nonce and the PKCE verifier go into a short-lived encrypted cookie of
 * this browser, which the return to /auth/callback checks.
 */
export async function GET(request: NextRequest) {
  const base = env.appBaseUrl();
  const flow = newFlow(safeReturnTo(request.nextUrl.searchParams.get('returnTo')));
  let target: string;
  try {
    target = authorizationUrl(flow);
  } catch {
    // The OIDC settings of this application are missing or malformed.
    return NextResponse.redirect(`${base}/login/error?code=not_configured`);
  }

  const response = NextResponse.redirect(target);
  response.cookies.set(FLOW_COOKIE, await sealFlow(flow), {
    httpOnly: true,
    // The identity provider returns with a top-level GET, which a lax cookie accompanies.
    sameSite: 'lax',
    secure: env.secureCookies(),
    path: FLOW_PATH,
    maxAge: 600,
  });
  // The page that sent the browser here must not be kept: it carries the start of a sign-in.
  response.headers.set('cache-control', 'no-store');
  return response;
}
