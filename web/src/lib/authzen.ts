import type {
  AuthzenActionSearchRequest,
  AuthzenEvaluationRequest,
  AuthzenEvaluationResponse,
  AuthzenEvaluationsRequest,
  AuthzenResourceSearchRequest,
  AuthzenSubjectSearchRequest,
} from '@/api/admin/model';

import { explanationSchema, type Explanation } from './schemas';

/*
 * The requests of the OpenID AuthZEN Authorization API 1.0 that the Integration page shows and tries, and that the simulation of
 * the Check page sends: one place builds them, so the code a developer copies is the request the PDP accepts (contract WEB03).
 * The shapes follow the specification and the operations of the PDP (core/schema/pdp.yaml): an evaluation names the subject, the
 * resource and the action; the resource search leaves the resource id out and the subject search the subject id, as the
 * specification has it.
 */

export const AUTHZEN_OPERATIONS = [
  'evaluation',
  'evaluations',
  'search_resource',
  'search_action',
  'search_subject',
] as const;
export type AuthzenOperation = (typeof AUTHZEN_OPERATIONS)[number];

/** The path of an operation under the address of the PDP of a service (the default paths of the specification). */
export const AUTHZEN_PATHS: Record<AuthzenOperation, string> = {
  evaluation: 'access/v1/evaluation',
  evaluations: 'access/v1/evaluations',
  search_resource: 'access/v1/search/resource',
  search_action: 'access/v1/search/action',
  search_subject: 'access/v1/search/subject',
};

/** The `resource.id` of a question about a type as a whole. */
const WHOLE_TYPE = '*';

/** The kind of subject Licetum knows: a person. */
const SUBJECT_TYPE = 'user';

/** The address of the PDP of a service: `<public url>/service/<id or slug>`. */
export const pdpAddress = (base: string, ref: string) =>
  `${base.replace(/\/+$/, '')}/service/${encodeURIComponent(ref)}`;

export const pdpUrl = (base: string, ref: string, operation: AuthzenOperation) =>
  `${pdpAddress(base, ref)}/${AUTHZEN_PATHS[operation]}`;

/** A claim or an attribute of the request: a string, or a list of strings (the rules compare claims as text). */
export type Value = string | string[];
export type Properties = Record<string, Value>;

/** What the person asks about: who (with the claims the rules read), the type, a resource of it and the access level. */
export interface Sample {
  person: string;
  /** The claims of the person, taken as they are given. */
  properties?: Properties;
  /** The attributes of the request that the rules may read. */
  context?: Properties;
  type: string;
  /** Empty: the type as a whole. */
  resource: string;
  action: string;
}

const subjectOf = (s: Pick<Sample, 'person' | 'properties'>) => ({
  type: SUBJECT_TYPE,
  id: s.person,
  ...(s.properties && Object.keys(s.properties).length > 0 ? { properties: s.properties } : {}),
});
const resourceOf = (s: Pick<Sample, 'type' | 'resource'>) => ({
  type: s.type,
  id: s.resource.trim() || WHOLE_TYPE,
});
const contextOf = (s: Pick<Sample, 'context'>) =>
  s.context && Object.keys(s.context).length > 0 ? { context: s.context } : {};

export function evaluationRequest(s: Sample): AuthzenEvaluationRequest {
  return {
    subject: subjectOf(s),
    resource: resourceOf(s),
    action: { name: s.action },
    ...contextOf(s),
  };
}

/** Questions of one person decided together: the subject and the context are the defaults of the request, the rest is per question. */
export function evaluationsRequest(
  s: Pick<Sample, 'person' | 'properties' | 'context'>,
  questions: Pick<Sample, 'type' | 'resource' | 'action'>[],
  semantic?: 'execute_all' | 'deny_on_first_deny' | 'permit_on_first_permit',
): AuthzenEvaluationsRequest {
  return {
    subject: subjectOf(s),
    ...contextOf(s),
    evaluations: questions.map((q) => ({ resource: resourceOf(q), action: { name: q.action } })),
    ...(semantic ? { options: { evaluations_semantic: semantic } } : {}),
  };
}

export interface Paging {
  limit?: number;
  token?: string;
}
const pageOf = (p?: Paging) => {
  const page = {
    ...(p?.limit !== undefined ? { limit: p.limit } : {}),
    // The first page has no token: an empty one is left out.
    ...(p?.token ? { token: p.token } : {}),
  };
  return Object.keys(page).length > 0 ? { page } : {};
};

/** The resources of the type that the person may do the action with: no `resource.id`. */
export function searchResourceRequest(s: Sample, p?: Paging): AuthzenResourceSearchRequest {
  return {
    subject: subjectOf(s),
    resource: { type: s.type },
    action: { name: s.action },
    ...contextOf(s),
    ...pageOf(p),
  };
}

/** The actions the person may do on the resource (the type as a whole when it names none): no `action`. */
export function searchActionRequest(s: Sample, p?: Paging): AuthzenActionSearchRequest {
  return { subject: subjectOf(s), resource: resourceOf(s), ...contextOf(s), ...pageOf(p) };
}

/** The people who may do the action on the resource: the subject names only its type. */
export function searchSubjectRequest(s: Sample, p?: Paging): AuthzenSubjectSearchRequest {
  return {
    subject: { type: SUBJECT_TYPE },
    resource: resourceOf(s),
    action: { name: s.action },
    ...contextOf(s),
    ...pageOf(p),
  };
}

export type AuthzenRequest =
  | AuthzenEvaluationRequest
  | AuthzenEvaluationsRequest
  | AuthzenResourceSearchRequest
  | AuthzenActionSearchRequest
  | AuthzenSubjectSearchRequest;

/** The request of an operation for a sample; the batch asks the sample and a second resource of the type. */
export function requestOf(operation: AuthzenOperation, s: Sample): AuthzenRequest {
  switch (operation) {
    case 'evaluation':
      return evaluationRequest(s);
    case 'evaluations':
      return evaluationsRequest(s, [s, { ...s, resource: '' }]);
    case 'search_resource':
      return searchResourceRequest(s, { limit: 50 });
    case 'search_action':
      return searchActionRequest(s);
    case 'search_subject':
      return searchSubjectRequest(s, { limit: 50 });
  }
}

/** What the Integration page shows before the person has typed anything. */
export const PLACEHOLDERS = { person: 'USERNAME', type: 'TYPE', resource: 'RESOURCE_ID' } as const;

export type Lang = 'curl' | 'go' | 'ts' | 'python';

/** A ready-made call: the credential of the PEP client is read from `API_KEY` (an API key, or the token of a registered issuer). */
export function snippet(lang: Lang, url: string, body: AuthzenRequest): string {
  const compact = JSON.stringify(body);
  const pretty = JSON.stringify(body, null, 2);
  switch (lang) {
    case 'curl':
      return `curl -X POST '${url}' \\\n  -H "Authorization: Bearer $API_KEY" \\\n  -H 'Content-Type: application/json' \\\n  -d '${compact}'`;
    case 'go':
      return `body := strings.NewReader(\`${compact}\`)\nreq, _ := http.NewRequest(http.MethodPost, "${url}", body)\nreq.Header.Set("Authorization", "Bearer "+apiKey)\nreq.Header.Set("Content-Type", "application/json")\nres, err := http.DefaultClient.Do(req)`;
    case 'ts':
      return `const res = await fetch('${url}', {\n  method: 'POST',\n  headers: { Authorization: \`Bearer \${apiKey}\`, 'Content-Type': 'application/json' },\n  body: JSON.stringify(${pretty.replace(/\n/g, '\n  ')}),\n});\nconst answer = await res.json();`;
    case 'python':
      return `import requests\n\nres = requests.post(\n    "${url}",\n    headers={"Authorization": f"Bearer {api_key}"},\n    json=${pretty.replace(/\n/g, '\n    ')},\n)\nanswer = res.json()`;
  }
}

/** What the verdict of the Check page is drawn from: the decision and, when the simulation gave one, its explanation. */
export interface CheckResult {
  allowed: boolean;
  explanation?: Explanation;
}

/** The answer of an evaluation of the simulation in the form the verdict is drawn from: the decision and its explanation. */
export function checkResultOf(answer: AuthzenEvaluationResponse): CheckResult {
  const parsed = explanationSchema.safeParse(answer.context?.reason_admin);
  return {
    allowed: answer.decision,
    ...(parsed.success ? { explanation: parsed.data } : {}),
  };
}

/** The error an evaluation of a batch could not be decided with (the decision is then false), if any. */
export function evaluationError(answer: AuthzenEvaluationResponse): string | undefined {
  const error = answer.context?.error;
  if (error && typeof error === 'object' && 'message' in error && typeof error.message === 'string')
    return error.message;
  return undefined;
}

/** Turns what a person typed into the properties of a request; a value written as `[a, b]` is a list, the rest is text. */
export function parseValue(value: string): Value {
  const v = value.trim();
  if (v.startsWith('[') && v.endsWith(']')) {
    try {
      const parsed: unknown = JSON.parse(v);
      if (Array.isArray(parsed)) return parsed.map(String);
    } catch {
      return v
        .slice(1, -1)
        .split(',')
        .map((x) => x.trim())
        .filter(Boolean);
    }
  }
  return v;
}

export const propertiesOf = (pairs: { key: string; value: string }[]): Properties =>
  Object.fromEntries(
    pairs.filter((p) => p.key.trim()).map((p) => [p.key.trim(), parseValue(p.value)]),
  );
