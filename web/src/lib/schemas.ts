import { z } from 'zod';

/*
 * Everything that crosses a border this application does not control is parsed here instead of cast: the answers of the
 * admin API's token endpoints, the discovery document of an identity provider, what the browser posts back and what a
 * person uploads or finds in their local storage. A value that does not fit is rejected where it enters.
 */

const numeric = z.union([z.number(), z.string()]);

/** What the backend returns at sign-in and refresh (RFC 6749 §5.1), reduced to what a session keeps. */
export const tokenAnswerSchema = z.object({
  access_token: z.string().min(1),
  expires_in: numeric.optional(),
  refresh_token: z.string().optional(),
  refresh_expires_in: numeric.optional(),
});
export type TokenAnswer = z.infer<typeof tokenAnswerSchema>;

/** The tokens the callback page posts to open a session: the access token must at least look like a JWT. */
export const sessionHandoffSchema = tokenAnswerSchema.extend({
  access_token: z.string().refine((token) => token.split('.').length === 3, 'not a JWT'),
});

/** `GET /platform` as far as the sign-in needs it. */
export const platformAnswerSchema = z.object({ service_id: z.uuid() });

/** The claims of a JWT, for display. */
export const jwtClaimsSchema = z.record(z.string(), z.unknown());

/** The answer of `/auth/session`. */
export const sessionHandoffAnswerSchema = z.object({ returnTo: z.string() });

/** `?issuer=` of `/api/discover`: an http(s) address. */
export const discoverQuerySchema = z.object({
  issuer: z
    .string()
    .trim()
    .pipe(z.url({ protocol: /^https?$/ })),
});

const optionalText = z.string().optional().catch(undefined);

/** The fields of an OpenID Connect discovery document that the form needs; a field of another type is ignored. */
export const discoveryDocumentSchema = z
  .object({
    issuer: optionalText,
    authorization_endpoint: optionalText,
    token_endpoint: optionalText,
    jwks_uri: optionalText,
  })
  .transform((doc) => ({
    issuer: doc.issuer,
    authorization_url: doc.authorization_endpoint,
    token_url: doc.token_endpoint,
    jwks_url: doc.jwks_uri,
  }));

/** The answer of `/api/discover`. */
export const discoveredEndpointsSchema = z.object({
  issuer: optionalText,
  authorization_url: optionalText,
  token_url: optionalText,
  jwks_url: optionalText,
});

const policyTestCheckSchema = z.object({
  type: z.string().min(1),
  action: z.string().min(1),
  resource_id: z.string().optional(),
  expect: z.enum(['allow', 'deny']),
});

/** A scenario of the policy tests (`PolicyTestCase` of the API). */
export const policyTestCaseSchema = z.object({
  name: z.string().min(1).max(200),
  subject: z.string().min(1),
  claims: z.record(z.string(), z.unknown()).optional(),
  context: z.record(z.string(), z.unknown()).optional(),
  checks: z.array(policyTestCheckSchema).min(1).max(100),
});
export type PolicyTestCaseValue = z.infer<typeof policyTestCaseSchema>;

/** A file of scenarios: `{ "cases": [...] }`. */
export const policyTestFileSchema = z.object({ cases: z.array(policyTestCaseSchema) });

/** A policy document (`licetum.policy/v1`) as far as the browser checks it; the server validates the rest on import. */
export const policyDocumentShapeSchema = z
  .object({ format: z.string(), service: z.string() })
  .loose();
