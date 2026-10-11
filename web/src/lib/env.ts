import 'server-only';
import { z } from 'zod';

/** An empty variable (as Compose passes `${VAR:-}`) counts as not set. */
const unsetWhenBlank = (value: unknown) => (value === '' ? undefined : value);
const address = z.preprocess(unsetWhenBlank, z.url({ protocol: /^https?$/ }).optional());

const addressesSchema = z.object({
  ADMIN_API_URL: address,
  ADMIN_API_PUBLIC_URL: address,
  CHECK_API_URL: address,
  CHECK_API_PUBLIC_URL: address,
  APP_BASE_URL: address,
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
    adminApiPublic: withoutTrailingSlash(e.ADMIN_API_PUBLIC_URL ?? adminApi),
    checkApi,
    checkApiPublic: withoutTrailingSlash(e.CHECK_API_PUBLIC_URL ?? checkApi),
    appBase: withoutTrailingSlash(e.APP_BASE_URL ?? 'http://localhost:3000'),
  };
}

/** Server-side settings, checked when they are read. Nothing here reaches the browser. */
export const env = {
  /** Where this server calls the admin API. */
  adminApiUrl: () => addresses().adminApi,
  /** The admin API as the browser reaches it: the sign-in redirect goes through it. */
  adminApiPublicUrl: () => addresses().adminApiPublic,
  checkApiUrl: () => addresses().checkApi,
  /** The check API as the services that call it see it: the address shown in the code examples. */
  checkApiPublicUrl: () => addresses().checkApiPublic,
  /** Public URL of this application. */
  appBaseUrl: () => addresses().appBase,
  sessionSecret: () => read(secretSchema).SESSION_SECRET,
  /** Cookies carry the Secure flag when the app is served over HTTPS. */
  secureCookies: () => addresses().appBase.startsWith('https://'),
};
