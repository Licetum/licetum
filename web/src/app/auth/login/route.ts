import { NextResponse, type NextRequest } from 'next/server';

import { ApiUnreachable, PlatformNotSeeded, platformServiceId } from '@/lib/admin-api';
import { env } from '@/lib/env';
import { safeReturnTo } from '@/lib/safe-return';

export const dynamic = 'force-dynamic';

/** Starts the sign-in: the admin API redirects to the identity provider of the platform service and brings the browser back to /auth/callback. */
export async function GET(request: NextRequest) {
  const base = env.appBaseUrl();
  let serviceId: string;
  try {
    serviceId = await platformServiceId();
  } catch (error) {
    const code =
      error instanceof PlatformNotSeeded
        ? 'not_seeded'
        : error instanceof ApiUnreachable
          ? 'api_unreachable'
          : 'unknown';
    return NextResponse.redirect(`${base}/login/error?code=${code}`);
  }

  const target = new URL(`${env.adminApiPublicUrl()}/service/${serviceId}/oauth/login`);
  target.searchParams.set('redirect_to', `${base}/auth/callback`);

  const response = NextResponse.redirect(target);
  response.cookies.set(
    'licetum_return',
    safeReturnTo(request.nextUrl.searchParams.get('returnTo')),
    {
      httpOnly: true,
      sameSite: 'lax',
      secure: env.secureCookies(),
      path: '/',
      maxAge: 600,
    },
  );
  return response;
}
