import 'server-only';
import { createHash, randomBytes, timingSafeEqual } from 'node:crypto';

import { EncryptJWT, jwtDecrypt } from 'jose';

import { env } from './env';
import { flowSchema, tokenAnswerSchema, tokenErrorSchema, type TokenAnswer } from './schemas';

/**
 * This server is the OpenID Connect client of the administrator sign-in (ADR 0001, 8.12): the authorization code flow
 * with PKCE (RFC 7636, S256), a state, and a nonce. What the sign-in needs to remember between the redirect to the
 * identity provider and the return (the state, the nonce and the PKCE verifier) is sealed into a short-lived cookie of
 * the browser that started it, not kept on a server: any replica of this application can finish a sign-in that another
 * started, and nothing is shared between them.
 */

export const FLOW_COOKIE = 'licetum_flow';
/** How long a sign-in may take, from the redirect to the return. */
const FLOW_TTL_MS = 10 * 60_000;
/** The cookie is only sent to the sign-in routes. */
export const FLOW_PATH = '/auth';

export type Flow = {
  state: string;
  nonce: string;
  /** The PKCE code_verifier, which never leaves this server and the browser's cookie jar. */
  verifier: string;
  /** Where to go after the sign-in: a path of this application (see safeReturnTo). */
  returnTo: string;
  exp: number;
};

/** Why a sign-in or a renewal of the tokens failed: the code the error screen shows. */
export type OidcFailure = 'idp_unreachable' | 'invalid_response';

export class OidcError extends Error {
  constructor(
    readonly failure: OidcFailure,
    message: string,
  ) {
    super(message);
  }
}

const random = (bytes: number) => randomBytes(bytes).toString('base64url');

/** A new sign-in: a state and a nonce (256 bits each) and a verifier of 86 characters, within the 43 to 128 of RFC 7636. */
export function newFlow(returnTo: string, now = Date.now()): Flow {
  return {
    state: random(32),
    nonce: random(32),
    verifier: random(64),
    returnTo,
    exp: now + FLOW_TTL_MS,
  };
}

/** The S256 code challenge of a verifier. */
export function pkceChallenge(verifier: string): string {
  return createHash('sha256').update(verifier).digest('base64url');
}

/** Where the identity provider sends the browser back to: the callback route of this application. */
function redirectUri(): string {
  return `${env.appBaseUrl()}/auth/callback`;
}

/** The URL the browser is sent to, to sign in at the identity provider. */
export function authorizationUrl(flow: Flow): string {
  const oidc = env.oidc();
  const url = new URL(oidc.authorizationUrl);
  url.searchParams.set('response_type', 'code');
  url.searchParams.set('client_id', oidc.clientId);
  url.searchParams.set('redirect_uri', redirectUri());
  url.searchParams.set('scope', oidc.scope);
  url.searchParams.set('state', flow.state);
  url.searchParams.set('nonce', flow.nonce);
  url.searchParams.set('code_challenge', pkceChallenge(flow.verifier));
  url.searchParams.set('code_challenge_method', 'S256');
  return url.toString();
}

/** The key of the flow cookie: derived apart from the session's, so the one cookie is never taken for the other. */
function flowKey(): Uint8Array {
  return createHash('sha256').update(`licetum-flow:${env.sessionSecret()}`).digest();
}

export async function sealFlow(flow: Flow): Promise<string> {
  return new EncryptJWT({ f: flow })
    .setProtectedHeader({ alg: 'dir', enc: 'A256GCM' })
    .setIssuedAt()
    .encrypt(flowKey());
}

/** The flow in a cookie, or null: no cookie, a damaged or foreign one, one that has run out. */
export async function unsealFlow(
  value: string | undefined,
  now = Date.now(),
): Promise<Flow | null> {
  if (!value) return null;
  try {
    const { payload } = await jwtDecrypt(value, flowKey());
    const parsed = flowSchema.safeParse(payload.f);
    return parsed.success && parsed.data.exp > now ? parsed.data : null;
  } catch {
    return null;
  }
}

/** Two strings are the same, compared in constant time. */
export function sameSecret(a: string, b: string): boolean {
  const left = Buffer.from(a);
  const right = Buffer.from(b);
  return left.length === right.length && timingSafeEqual(left, right);
}

type Fetch = typeof fetch;

/** A request to the token endpoint, with the client authenticated as the identity provider expects (RFC 6749 §2.3.1). */
async function tokenRequest(params: Record<string, string>, fetchImpl: Fetch): Promise<Response> {
  const oidc = env.oidc();
  const headers: Record<string, string> = {
    'content-type': 'application/x-www-form-urlencoded',
    accept: 'application/json',
  };
  const body = new URLSearchParams({ ...params, client_id: oidc.clientId });
  if (oidc.clientSecret) {
    const credentials = `${encodeURIComponent(oidc.clientId)}:${encodeURIComponent(oidc.clientSecret)}`;
    headers.authorization = `Basic ${Buffer.from(credentials).toString('base64')}`;
  }
  try {
    return await fetchImpl(oidc.tokenUrl, {
      method: 'POST',
      headers,
      body,
      cache: 'no-store',
      signal: AbortSignal.timeout(10_000),
    });
  } catch {
    throw new OidcError('idp_unreachable', 'the token endpoint does not answer');
  }
}

/** The claims of a JWT, read without verifying it. */
function unverifiedClaims(token: string): Record<string, unknown> | null {
  try {
    const part = token.split('.')[1];
    return part
      ? (JSON.parse(Buffer.from(part, 'base64url').toString('utf8')) as Record<string, unknown>)
      : null;
  } catch {
    return null;
  }
}

/**
 * The nonce of an ID token is the one the sign-in sent. The ID token came straight from the token endpoint over TLS, which
 * authenticates it (OpenID Connect Core 3.1.3.7): its signature is not checked here, the access token is checked by the
 * admin API on every request. An answer without an ID token has nothing to compare.
 */
function nonceMatches(idToken: string | undefined, nonce: string): boolean {
  if (!idToken) return true;
  const claim = unverifiedClaims(idToken)?.nonce;
  return typeof claim === 'string' && sameSecret(claim, nonce);
}

/** Parses an answer of the token endpoint: the tokens, or the error it names. */
async function readTokens(response: Response): Promise<TokenAnswer> {
  const json: unknown = await response.json().catch(() => null);
  const tokens = tokenAnswerSchema.safeParse(json);
  if (!response.ok || !tokens.success) {
    throw new OidcError('invalid_response', `the token endpoint answered ${response.status}`);
  }
  if (tokens.data.token_type && tokens.data.token_type.toLowerCase() !== 'bearer') {
    throw new OidcError('invalid_response', 'the tokens are not bearer tokens');
  }
  return tokens.data;
}

/** Exchanges the authorization code for the tokens (RFC 6749 §4.1.3), proving the PKCE verifier. */
export async function exchangeCode(
  code: string,
  flow: Flow,
  fetchImpl: Fetch = fetch,
): Promise<TokenAnswer> {
  const response = await tokenRequest(
    {
      grant_type: 'authorization_code',
      code,
      redirect_uri: redirectUri(),
      code_verifier: flow.verifier,
    },
    fetchImpl,
  );
  const tokens = await readTokens(response);
  if (!nonceMatches(tokens.id_token, flow.nonce)) {
    throw new OidcError(
      'invalid_response',
      'the nonce of the ID token is not the one that was sent',
    );
  }
  return tokens;
}

export type Renewal =
  | { kind: 'renewed'; tokens: TokenAnswer }
  /** The refresh token is spent, revoked or expired (invalid_grant): the person has to sign in again. */
  | { kind: 'invalid_grant' }
  /** Nothing is known: the identity provider did not answer or answered something unusable. */
  | { kind: 'unavailable' };

/** Renews the tokens with the refresh token (RFC 6749 §6). */
export async function refreshTokens(
  refreshToken: string,
  fetchImpl: Fetch = fetch,
): Promise<Renewal> {
  let response: Response;
  try {
    response = await tokenRequest(
      { grant_type: 'refresh_token', refresh_token: refreshToken },
      fetchImpl,
    );
  } catch {
    return { kind: 'unavailable' };
  }
  if (!response.ok) {
    const error = tokenErrorSchema.safeParse(await response.json().catch(() => null));
    return error.success && error.data.error === 'invalid_grant'
      ? { kind: 'invalid_grant' }
      : { kind: 'unavailable' };
  }
  try {
    return { kind: 'renewed', tokens: await readTokens(response) };
  } catch {
    return { kind: 'unavailable' };
  }
}
