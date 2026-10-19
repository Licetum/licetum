import { z } from 'zod';

/*
 * Everything that crosses a border this application does not control is parsed here instead of cast: the answers of the
 * token endpoint of the identity provider, the discovery document of an identity provider, what the browser posts back and what a
 * person uploads or finds in their local storage. A value that does not fit is rejected where it enters.
 */

const numeric = z.union([z.number(), z.string()]);

/** What the token endpoint of the identity provider returns (RFC 6749 §5.1), reduced to what a session keeps. */
export const tokenAnswerSchema = z.object({
  // The admin API verifies a JWT: anything else would only fail there, one request later.
  access_token: z.string().refine((token) => token.split('.').length === 3, 'not a JWT'),
  token_type: z.string().optional(),
  expires_in: numeric.optional(),
  refresh_token: z.string().optional(),
  refresh_expires_in: numeric.optional(),
  id_token: z.string().optional(),
});
export type TokenAnswer = z.infer<typeof tokenAnswerSchema>;

/** An error answer of the token endpoint (RFC 6749 §5.2). */
export const tokenErrorSchema = z.object({ error: z.string() });

/** The sealed state of a sign-in that is under way, kept in a short-lived cookie (see lib/oidc.ts). */
export const flowSchema = z.object({
  state: z.string().min(16),
  nonce: z.string().min(16),
  verifier: z.string().min(43).max(128),
  returnTo: z.string(),
  /** When the sign-in stops being accepted, ms since the epoch. */
  exp: z.number(),
});

/** The claims of a JWT, for display. */
export const jwtClaimsSchema = z.record(z.string(), z.unknown());

/** `?issuer=` of `/api/discover`: an http(s) address. */
export const discoverQuerySchema = z.object({
  issuer: z
    .string()
    .trim()
    .pipe(z.url({ protocol: /^https?$/ })),
});

const optionalText = z.string().optional().catch(undefined);

/** The fields of an OpenID Connect discovery document that the issuer form needs; a field of another type is ignored. */
export const discoveryDocumentSchema = z
  .object({
    issuer: optionalText,
    jwks_uri: optionalText,
  })
  .transform((doc) => ({
    issuer: doc.issuer,
    jwks_url: doc.jwks_uri,
  }));

/** The answer of `/api/discover`. */
export const discoveredEndpointsSchema = z.object({
  issuer: optionalText,
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

const anchorSchema = z.object({ resource_id: z.string().nullable(), type: z.string() });
const ruleRefSchema = z.object({
  access: z.array(z.string()),
  anchor: anchorSchema,
  applies_to: z.string(),
  effect: z.enum(['allow', 'deny']),
  exception: z.boolean(),
  rule_id: z.string(),
});

/**
 * The explanation of a decision (`context.reason_admin` of an AuthZEN evaluation of the simulation, the `Explanation` of the
 * admin API). The context of an answer is an open object, so the explanation is parsed from it, not cast.
 */
export const explanationSchema = z.object({
  decision: z.enum(['allowed', 'denied', 'no_access', 'resource_not_found', 'type_not_found']),
  granted_by: z.array(ruleRefSchema),
  denials: z.array(
    z.object({
      level: z.string(),
      lifted_by: z.array(ruleRefSchema),
      missing_attribute: z.boolean(),
      rule: ruleRefSchema,
      unresolved_parent: z.boolean(),
    }),
  ),
});
export type Explanation = z.infer<typeof explanationSchema>;
