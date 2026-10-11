import 'server-only';
import { createHash } from 'node:crypto';

import { EncryptJWT, jwtDecrypt } from 'jose';
import { cookies } from 'next/headers';

import { env } from './env';

/**
 * The sign-in session lives in an encrypted cookie (a JWE), like the session of the Auth0 Next.js SDK: the tokens
 * never reach the browser's JavaScript. A token can be longer than one cookie holds, so the value is split into
 * chunks: licetum_session.0, licetum_session.1, ...
 */
export type Session = {
  /** The access token of the platform (the admin API verifies it). */
  accessToken: string;
  /** When the access token stops being accepted, ms since the epoch by this server's clock. */
  accessExpiresAt: number;
  refreshToken?: string;
  refreshExpiresAt?: number;
};

const COOKIE = 'licetum_session';
const CHUNK = 3600;
const MAX_CHUNKS = 6;
/** A session without a refresh token lasts as long as the access token, but not longer than this. */
const MAX_AGE_S = 60 * 60 * 24 * 7;

type CookieJar = {
  get(name: string): { value: string } | undefined;
};

type CookieSetter = {
  set(name: string, value: string, options: Record<string, unknown>): unknown;
  delete(name: string): unknown;
};

function key(): Uint8Array {
  return createHash('sha256').update(env.sessionSecret()).digest();
}

function baseOptions(maxAge: number) {
  return {
    httpOnly: true,
    sameSite: 'lax' as const,
    secure: env.secureCookies(),
    path: '/',
    maxAge,
  };
}

export async function seal(session: Session): Promise<string> {
  return new EncryptJWT({ s: session })
    .setProtectedHeader({ alg: 'dir', enc: 'A256GCM' })
    .setIssuedAt()
    .encrypt(key());
}

export async function unseal(value: string): Promise<Session | null> {
  try {
    const { payload } = await jwtDecrypt(value, key());
    const s = payload.s as Session | undefined;
    return s && typeof s.accessToken === 'string' ? s : null;
  } catch {
    // A wrong or rotated secret, a damaged cookie: the same as no session.
    return null;
  }
}

function join(jar: CookieJar): string | null {
  const parts: string[] = [];
  for (let i = 0; i < MAX_CHUNKS; i++) {
    const part = jar.get(`${COOKIE}.${i}`)?.value;
    if (part === undefined) break;
    parts.push(part);
  }
  return parts.length ? parts.join('') : null;
}

/** The session of the current request, or null. For server components, layouts and route handlers. */
export async function readSession(): Promise<Session | null> {
  const value = join(await cookies());
  return value ? unseal(value) : null;
}

/** The session from the cookies of a Proxy/route request. */
export async function readSessionFrom(jar: CookieJar): Promise<Session | null> {
  const value = join(jar);
  return value ? unseal(value) : null;
}

export async function writeSession(target: CookieSetter, session: Session): Promise<void> {
  const sealed = await seal(session);
  const chunks = sealed.match(new RegExp(`.{1,${CHUNK}}`, 'g')) ?? [];
  if (chunks.length > MAX_CHUNKS) throw new Error('the session does not fit into the cookies');

  const until = session.refreshExpiresAt ?? session.accessExpiresAt;
  const maxAge = Math.max(60, Math.min(MAX_AGE_S, Math.floor((until - Date.now()) / 1000) + 60));
  chunks.forEach((chunk, i) => target.set(`${COOKIE}.${i}`, chunk, baseOptions(maxAge)));
  // A shorter session than the previous one must not leave the old tail behind.
  for (let i = chunks.length; i < MAX_CHUNKS; i++) target.delete(`${COOKIE}.${i}`);
}

export function clearSession(target: CookieSetter): void {
  for (let i = 0; i < MAX_CHUNKS; i++) target.delete(`${COOKIE}.${i}`);
}
