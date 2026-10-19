import 'server-only';
import { refreshTokens } from './oidc';
import type { TokenAnswer } from './schemas';
import type { Session } from './session';

const num = (v: number | string | undefined): number | undefined => {
  const n = typeof v === 'string' ? Number(v) : v;
  return typeof n === 'number' && Number.isFinite(n) && n > 0 ? n : undefined;
};

/** The session for a token answer of the identity provider. The lifetime comes from expires_in (this server's clock), the exp claim is the fallback. */
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
 * Renews the access token with the refresh token (at the identity provider) when it is about to expire. Returns the same session when it is
 * still fresh, a new one when it was renewed, and null when the person has to sign in again.
 */
export async function refreshIfNeeded(
  session: Session,
): Promise<{ session: Session | null; renewed: boolean }> {
  const now = Date.now();
  if (now < session.accessExpiresAt - REFRESH_MARGIN_MS) return { session, renewed: false };

  const { refreshToken } = session;
  const canRefresh =
    !!refreshToken && (!session.refreshExpiresAt || now < session.refreshExpiresAt);
  if (!refreshToken || !canRefresh) {
    return { session: now < session.accessExpiresAt ? session : null, renewed: false };
  }

  const renewal = await refreshTokens(refreshToken);
  // invalid_grant: the refresh token is spent or expired, so the person signs in again.
  if (renewal.kind === 'invalid_grant') return { session: null, renewed: false };
  // The identity provider does not answer: the token that still works keeps working.
  if (renewal.kind === 'unavailable') {
    return { session: now < session.accessExpiresAt ? session : null, renewed: false };
  }
  return { session: sessionFromTokens(renewal.tokens, session), renewed: true };
}
