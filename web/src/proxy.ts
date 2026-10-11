import { NextResponse, type NextRequest } from 'next/server';

/**
 * Sends a visitor without a session to the sign-in page. It only looks at the cookie: whether the session is still
 * good is decided by the first call to the admin API (a 401 there starts the sign-in again).
 */
export function proxy(request: NextRequest) {
  if (request.cookies.has('licetum_session.0')) return NextResponse.next();
  const { pathname, search } = request.nextUrl;
  const target = new URL('/login', request.url);
  target.searchParams.set('returnTo', pathname + search);
  return NextResponse.redirect(target);
}

export const config = {
  // Everything except the sign-in screens, the auth and API routes, and static files.
  matcher: ['/((?!login|no-access|auth|api|_next/static|_next/image|icon.svg|favicon.ico).*)'],
};
