import { jwtClaimsSchema } from './schemas';

/** The payload of a JWT, without verifying it: the admin API verifies the token, this is for display only. */
function decodeJwtPayload(token: string): Record<string, unknown> | null {
  try {
    const payload = token.split('.')[1];
    if (!payload) return null;
    const base64 = payload.replace(/-/g, '+').replace(/_/g, '/');
    // atob gives one character per UTF-8 byte; decode as UTF-8 so Cyrillic and Chinese names survive.
    const bytes = Uint8Array.from(atob(base64), (c) => c.charCodeAt(0));
    return jwtClaimsSchema.parse(JSON.parse(new TextDecoder('utf-8').decode(bytes)));
  } catch {
    return null;
  }
}

export type Identity = {
  /** Display name: the name claim, the given and family names, or the username. */
  name: string;
  /** preferred_username, or sub — what the rules call the person. */
  username: string;
  sub: string;
};

export function identityFromToken(token: string): Identity | null {
  const claims = decodeJwtPayload(token);
  if (!claims) return null;
  const sub = String(claims.sub ?? '');
  const username = String(claims.preferred_username ?? sub);
  const full =
    (claims.name as string | undefined) ||
    [claims.given_name, claims.family_name].filter(Boolean).join(' ');
  return { name: full || username || sub, username, sub };
}
