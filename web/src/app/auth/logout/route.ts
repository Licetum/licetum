import { NextResponse, type NextRequest } from 'next/server';

import { env } from '@/lib/env';
import { clearSession } from '@/lib/session';

export const dynamic = 'force-dynamic';

/** Ends the session of this application. (The session at the identity provider is not touched.) */
export async function POST(request: NextRequest) {
  const origin = request.headers.get('origin');
  if (origin !== new URL(env.appBaseUrl()).origin) {
    return NextResponse.json({ detail: 'cross-origin request' }, { status: 403 });
  }
  const response = NextResponse.json({ ok: true });
  clearSession(response.cookies);
  return response;
}
