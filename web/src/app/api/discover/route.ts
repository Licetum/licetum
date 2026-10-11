import { NextResponse, type NextRequest } from 'next/server';

import { discoveryDocumentSchema, discoverQuerySchema } from '@/lib/schemas';
import { readSession } from '@/lib/session';

export const dynamic = 'force-dynamic';

const BLOCKED_HOSTS = new Set([
  '169.254.169.254',
  'metadata.google.internal',
  'metadata.azure.com',
  '100.100.100.200',
]);

/**
 * "Detect automatically" in the connect-a-service wizard: reads the OpenID Connect discovery document of an issuer and
 * returns only the four values the form needs. Only a signed-in administrator may call it, the answer is cut down to
 * those fields, and the well-known cloud metadata addresses are refused; it still reaches whatever this server can, so
 * run it where an administrator is trusted to name an address.
 */
export async function GET(request: NextRequest) {
  if (!(await readSession())) return NextResponse.json({ detail: 'no session' }, { status: 401 });

  const query = discoverQuerySchema.safeParse({
    issuer: request.nextUrl.searchParams.get('issuer') ?? '',
  });
  if (!query.success) {
    return NextResponse.json({ detail: 'the issuer is not an http(s) URL' }, { status: 400 });
  }
  const raw = query.data.issuer;
  if (BLOCKED_HOSTS.has(new URL(raw).hostname)) {
    return NextResponse.json({ detail: 'the address is not allowed' }, { status: 400 });
  }

  const target = `${raw.replace(/\/+$/, '')}/.well-known/openid-configuration`;
  try {
    const response = await fetch(target, {
      redirect: 'error',
      cache: 'no-store',
      signal: AbortSignal.timeout(5000),
      headers: { accept: 'application/json' },
    });
    if (!response.ok)
      return NextResponse.json(
        { detail: `the provider answered ${response.status}` },
        { status: 502 },
      );
    const text = await response.text();
    if (text.length > 262_144)
      return NextResponse.json({ detail: 'the answer is too large' }, { status: 502 });
    return NextResponse.json(discoveryDocumentSchema.parse(JSON.parse(text)));
  } catch {
    return NextResponse.json(
      { detail: 'the provider could not be reached or did not answer with a discovery document' },
      { status: 502 },
    );
  }
}
