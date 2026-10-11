import 'server-only';
import { env } from './env';
import { platformAnswerSchema, tokenAnswerSchema, type TokenAnswer } from './schemas';
import type { Session } from './session';

/** The id of the service Licetum is registered under within itself: the admin UI signs in through its OAuth2 client. */
let cachedPlatform: { id: string; at: number } | null = null;

export class ApiUnreachable extends Error {}
export class PlatformNotSeeded extends Error {}

export async function platformServiceId(): Promise<string> {
  if (cachedPlatform && Date.now() - cachedPlatform.at < 60_000) return cachedPlatform.id;
  let response: Response;
  try {
    response = await fetch(`${env.adminApiUrl()}/platform`, { cache: 'no-store' });
  } catch {
    throw new ApiUnreachable('the admin API does not answer');
  }
  if (response.status === 404) throw new PlatformNotSeeded('the self-service is not seeded');
  if (!response.ok) throw new ApiUnreachable(`the admin API answered ${response.status}`);
  const body = platformAnswerSchema.safeParse(await response.json().catch(() => null));
  if (!body.success) throw new ApiUnreachable('the admin API did not return the platform service');
  cachedPlatform = { id: body.data.service_id, at: Date.now() };
  return cachedPlatform.id;
}

const num = (v: number | string | undefined): number | undefined => {
  const n = typeof v === 'string' ? Number(v) : v;
  return typeof n === 'number' && Number.isFinite(n) && n > 0 ? n : undefined;
};

/** The session for a token answer. The lifetime comes from expires_in (this server's clock), the exp claim is the fallback. */
export function sessionFromTokens(t: TokenAnswer, previous?: Session): Session {
  const now = Date.now();
  const expiresIn = num(t.expires_in);
  const refreshIn = num(t.refresh_expires_in);
  return {
    accessToken: t.access_token,
    accessExpiresAt: expiresIn ? now + expiresIn * 1000 : now + 5 * 60_000,
    // A new refresh token replaces the old one (RFC 6749 §6); without one the previous stays.
    refreshToken: t.refresh_token ?? previous?.refreshToken,
    refreshExpiresAt: t.refresh_token
      ? refreshIn
        ? now + refreshIn * 1000
        : undefined
      : previous?.refreshExpiresAt,
  };
}

const REFRESH_MARGIN_MS = 60_000;

/**
 * Renews the access token with the refresh token when it is about to expire. Returns the same session when it is
 * still fresh, a new one when it was renewed, and null when the person has to sign in again.
 */
export async function refreshIfNeeded(
  session: Session,
): Promise<{ session: Session | null; renewed: boolean }> {
  const now = Date.now();
  if (now < session.accessExpiresAt - REFRESH_MARGIN_MS) return { session, renewed: false };

  const canRefresh =
    !!session.refreshToken && (!session.refreshExpiresAt || now < session.refreshExpiresAt);
  if (!canRefresh) {
    return { session: now < session.accessExpiresAt ? session : null, renewed: false };
  }

  let response: Response;
  try {
    response = await fetch(
      `${env.adminApiUrl()}/service/${await platformServiceId()}/oauth/refresh`,
      {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({ refresh_token: session.refreshToken }),
        cache: 'no-store',
      },
    );
  } catch {
    // The API is down: the token that still works keeps working.
    return { session: now < session.accessExpiresAt ? session : null, renewed: false };
  }
  // invalid_grant: the refresh token is spent or expired — sign in again.
  if (response.status === 401) return { session: null, renewed: false };
  if (!response.ok)
    return { session: now < session.accessExpiresAt ? session : null, renewed: false };

  const answer = tokenAnswerSchema.safeParse(await response.json().catch(() => null));
  // An answer that is not a token answer is no renewal: the token that still works keeps working.
  if (!answer.success)
    return { session: now < session.accessExpiresAt ? session : null, renewed: false };
  return { session: sessionFromTokens(answer.data, session), renewed: true };
}
