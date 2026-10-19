import type {
  PEPClient,
  PEPClientInput,
  PEPClientInputOperationsItem,
  PEPClientPatch,
  PEPIssuer,
  PEPIssuerAlgsItem,
  PEPKey,
} from '@/api/admin/model';

/*
 * The forms of the PEP screens in the words of the API: a PEP is the application that asks the PDP of a service (a policy
 * enforcement point). A client is how it is known — by an API key Licetum issues, or by the access tokens of a registered issuer.
 */

/** What a client may call, in the order the form lists them: evaluate by default, the rest has to be asked for. */
export const PEP_OPERATIONS: PEPClientInputOperationsItem[] = [
  'evaluate',
  'search_resource',
  'search_action',
  'search_subject',
  'explain',
];

export type PepKind = PEPClient['kind'];

export interface ClientForm {
  name: string;
  kind: PepKind;
  operations: PEPClientInputOperationsItem[];
  /** jwt only. */
  issuer: string;
  /** jwt only: the `azp`, or `client_id`, of the client's tokens. */
  clientId: string;
  enabled: boolean;
}

export const emptyClientForm = (): ClientForm => ({
  name: '',
  kind: 'api_key',
  operations: ['evaluate'],
  issuer: '',
  clientId: '',
  enabled: true,
});

export const formOfClient = (client: PEPClient): ClientForm => ({
  name: client.name,
  kind: client.kind,
  operations: PEP_OPERATIONS.filter((op) => client.operations.includes(op)),
  issuer: client.issuer ?? '',
  clientId: client.client_id ?? '',
  enabled: client.enabled,
});

/** What stops the form from being saved, as a translation key; null — nothing. */
export function clientProblem(f: ClientForm): string | null {
  if (f.name.trim() === '') return 'pep.problem.name';
  if (f.operations.length === 0) return 'pep.problem.operations';
  if (f.kind === 'jwt' && f.issuer.trim() === '') return 'pep.problem.issuer';
  if (f.kind === 'jwt' && f.clientId.trim() === '') return 'pep.problem.clientId';
  return null;
}

/** The identity applies to a jwt client only; an api_key client is known by its keys. */
const identityOf = (f: ClientForm) =>
  f.kind === 'jwt' ? { issuer: f.issuer.trim(), client_id: f.clientId.trim() } : {};

export const createClientBody = (f: ClientForm): PEPClientInput => ({
  name: f.name.trim(),
  kind: f.kind,
  operations: f.operations,
  enabled: f.enabled,
  ...identityOf(f),
});

/** The kind does not change, so it is not sent; the operations replace the list. */
export const patchClientBody = (f: ClientForm): PEPClientPatch => ({
  name: f.name.trim(),
  operations: f.operations,
  enabled: f.enabled,
  ...identityOf(f),
});

export type KeyState = 'active' | 'revoked' | 'expired';

/** A key is revoked, or expired, or works. */
export const keyState = (key: PEPKey): KeyState =>
  key.revoked_at ? 'revoked' : key.active ? 'active' : 'expired';

/** A client may hold two active keys at most (a key is rotated by issuing the new one before revoking the old one). */
export const MAX_ACTIVE_KEYS = 2;

export const activeKeys = (keys: PEPKey[]) => keys.filter((k) => keyState(k) === 'active').length;

export interface IssuerForm {
  issuer: string;
  jwksUrl: string;
  audiences: string[];
  algs: PEPIssuerAlgsItem[];
  requireTyp: boolean;
}

export const emptyIssuerForm = (): IssuerForm => ({
  issuer: '',
  jwksUrl: '',
  audiences: [],
  algs: [],
  requireTyp: false,
});

export const formOfIssuer = (issuer: PEPIssuer): IssuerForm => ({
  issuer: issuer.issuer,
  jwksUrl: issuer.jwks_url,
  audiences: issuer.audiences ?? [],
  algs: issuer.algs ?? [],
  requireTyp: issuer.require_typ ?? false,
});

export function issuerProblem(f: IssuerForm): string | null {
  if (f.issuer.trim() === '') return 'issuers.problem.issuer';
  if (!/^https?:\/\//i.test(f.jwksUrl.trim())) return 'issuers.problem.jwks';
  return null;
}

/** The audiences and the algorithms are left out when none are chosen: the issuer then has the defaults of the PDP. */
export const issuerBody = (f: IssuerForm): PEPIssuer => ({
  issuer: f.issuer.trim(),
  jwks_url: f.jwksUrl.trim(),
  require_typ: f.requireTyp,
  ...(f.audiences.length > 0
    ? { audiences: f.audiences.map((a) => a.trim()).filter(Boolean) }
    : {}),
  ...(f.algs.length > 0 ? { algs: f.algs } : {}),
});
