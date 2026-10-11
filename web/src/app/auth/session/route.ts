import { NextResponse, type NextRequest } from 'next/server';

import { sessionFromTokens } from '@/lib/admin-api';
import { env } from '@/lib/env';
import { safeReturnTo } from '@/lib/safe-return';
import { sessionHandoffSchema } from '@/lib/schemas';
import { writeSession } from '@/lib/session';

export const dynamic = 'force-dynamic';

/**
 * The page /auth/callback receives the tokens in the URL fragment (the browser never sends a fragment to a server)
 * and hands them over here once; they go into the encrypted httpOnly session cookie and nowhere else.
 */
export async function POST(request: NextRequest) {
  // Only this application's own page may open a session: a cross-site form must not sign someone in as somebody else.
  const origin = request.headers.get('origin');
  if (origin !== new URL(env.appBaseUrl()).origin) {
    return NextResponse.json({ detail: 'cross-origin request' }, { status: 403 });
  }
  if (!request.headers.get('content-type')?.startsWith('application/json')) {
    return NextResponse.json({ detail: 'expected JSON' }, { status: 415 });
  }

  let json: unknown;
  try {
    json = await request.json();
  } catch {
    return NextResponse.json({ detail: 'malformed JSON' }, { status: 400 });
  }
  const parsed = sessionHandoffSchema.safeParse(json);
  if (!parsed.success) return NextResponse.json({ detail: 'no access token' }, { status: 400 });
  const body = parsed.data;

  const returnTo = safeReturnTo(request.cookies.get('licetum_return')?.value);
  const response = NextResponse.json({ returnTo });
  try {
    await writeSession(response.cookies, sessionFromTokens(body));
  } catch {
    return NextResponse.json(
      { detail: 'the session does not fit into the cookies' },
      { status: 413 },
    );
  }
  response.cookies.delete('licetum_return');
  return response;
}
