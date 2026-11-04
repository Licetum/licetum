import 'server-only';
import { z } from 'zod';

/** An empty variable (as Compose passes `${VAR:-}`) counts as not set. */
const unsetWhenBlank = (value: unknown) => (value === '' ? undefined : value);
const address = z.preprocess(unsetWhenBlank, z.url({ protocol: /^https?$/ }).optional());

const addressesSchema = z.object({
  ADMIN_API_URL: address,
  CHECK_API_URL: address,
  CHECK_API_PUBLIC_URL: address,
  APP_BASE_URL: address,
});

const blankIsUnset = (schema: z.ZodType<string>) => z.preprocess(unsetWhenBlank, schema.optional());

/**
 * The OpenID Connect client of the administrator sign-in: this server is the client (code flow with PKCE), the identity
 * provider is the one the admin API trusts (ADMIN_AUTH__* of the admin API). The authorization endpoint is where the
 * browser is sent, the token endpoint is where this server calls: they may be different addresses (a Compose network).
 */
const oidcSchema = z.object({
  OIDC_AUTHORIZATION_URL: z.preprocess(
    (v) => unsetWhenBlank(v) ?? undefined,
    z.url({ protocol: /^https?$/, error: 'OIDC_AUTHORIZATION_URL is not set' }),
  ),
  OIDC_TOKEN_URL: z.preprocess(
    (v) => unsetWhenBlank(v) ?? undefined,
    z.url({ protocol: /^https?$/, error: 'OIDC_TOKEN_URL is not set' }),
  ),
  OIDC_CLIENT_ID: z.preprocess(
    (v) => unsetWhenBlank(v) ?? undefined,
    z.string({ error: 'OIDC_CLIENT_ID is not set' }).min(1, 'OIDC_CLIENT_ID is not set'),
  ),
  /** Absent for a public client, which signs in with PKCE alone. */
  OIDC_CLIENT_SECRET: blankIsUnset(z.string()),
  OIDC_SCOPE: blankIsUnset(z.string()),
});

const secretSchema = z.object({
  SESSION_SECRET: z
    .string({ error: 'SESSION_SECRET is not set' })
    .min(32, 'SESSION_SECRET must be at least 32 characters'),
});

function read<S extends z.ZodType>(schema: S): z.output<S> {
  const parsed = schema.safeParse(process.env);
  if (!parsed.success) {
    throw new Error(`Invalid settings (see web/.env.example):\n${z.prettifyError(parsed.error)}`);
  }
  return parsed.data;
}

const withoutTrailingSlash = (url: string) => url.replace(/\/+$/, '');

function addresses() {
  const e = read(addressesSchema);
  const adminApi = withoutTrailingSlash(e.ADMIN_API_URL ?? 'http://localhost:8002');
  const checkApi = withoutTrailingSlash(e.CHECK_API_URL ?? 'http://localhost:8001');
  return {
    adminApi,
    checkApi,
    checkApiPublic: withoutTrailingSlash(e.CHECK_API_PUBLIC_URL ?? checkApi),
    appBase: withoutTrailingSlash(e.APP_BASE_URL ?? 'http://localhost:3000'),
  };
}

/** Server-side settings, checked when they are read. Nothing here reaches the browser. */
export const env = {
  /** Where this server calls the admin API. */
  adminApiUrl: () => addresses().adminApi,
  checkApiUrl: () => addresses().checkApi,
  /** The check API as the services that call it see it: the address shown in the code examples. */
  checkApiPublicUrl: () => addresses().checkApiPublic,
  /** Public URL of this application. */
  appBaseUrl: () => addresses().appBase,
  sessionSecret: () => read(secretSchema).SESSION_SECRET,
  /** The OIDC client settings; throws when the required ones are missing (the sign-in then says it is not configured). */
  oidc: () => {
    const e = read(oidcSchema);
    return {
      authorizationUrl: e.OIDC_AUTHORIZATION_URL,
      tokenUrl: e.OIDC_TOKEN_URL,
      clientId: e.OIDC_CLIENT_ID,
      clientSecret: e.OIDC_CLIENT_SECRET,
      /** offline_access: the identity provider then issues the refresh token that keeps the session going. */
      scope: e.OIDC_SCOPE ?? 'openid profile email offline_access',
    };
  },
  /** Cookies carry the Secure flag when the app is served over HTTPS. */
  secureCookies: () => addresses().appBase.startsWith('https://'),
};
