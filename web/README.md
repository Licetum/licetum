# Licetum web interface

Next.js 16 (App Router) + Ant Design 6 + TanStack Query. The visual system is in `src/theme/tokens.ts`.

## How it works

- **Sign-in is on the server, and this server is the OpenID Connect client** (ADR 0001, 8.12): `/auth/login` sends the browser
  to the identity provider (authorization code flow with PKCE, S256, a state and a nonce), `/auth/callback` checks the
  state, exchanges the code for the tokens at the token endpoint and keeps them in an **encrypted httpOnly cookie**
  (`licetum_session.0…n`, a JWE, split into chunks). The tokens never reach the page's JavaScript. What the sign-in has to
  remember between the redirect and the return (the state, the nonce and the PKCE verifier) lives in a second encrypted
  cookie (`licetum_flow`, ten minutes, only sent to `/auth/*`), so no replica keeps any state of its own. The tokens are
  renewed at the token endpoint with the refresh token. This works with any OpenID Connect provider, Auth0 included.
  The admin API trusts the same provider (`ADMIN_AUTH__ISSUER`, `__AUDIENCE`, `__JWKS_URL`) and verifies the access token
  on every request, so the access token this client gets must be a JWT whose `aud` contains `ADMIN_AUTH__AUDIENCE`.
- **The browser talks to the API only through `/api/admin/*`** (`src/app/api/admin/[...path]/route.ts`): the server adds the
  bearer token, renews it with the refresh token when it is about to expire, and refuses sign-in endpoints and
  cross-origin writes. `/api/check/*` is the same for "try it with a credential" (an API key of a PEP client, or the token of a
  registered issuer) and `/api/discover` reads the OIDC discovery document of an issuer.
- **AuthZEN requests are built in one place**: `src/lib/authzen.ts` writes the evaluation, batch and search requests of the
  PDP for the Check page (which sends them to the simulation, `/service/{id}/simulate/access/v1/*`, and draws the verdict from
  `context.reason_admin`) and for the snippets and the tester of the Integration page. `tests/authzen.test.ts` holds each
  request to the AuthZEN specification (contract WEB03).
- **The API client is generated** from `../admin-api/schema/admin.yaml` by orval (`pnpm orval`, into `src/api`, not
  committed). Server data is read with TanStack Query; a write is followed by `useRefreshAll()`.
- **Data from outside is parsed, not cast**: `src/lib/schemas.ts` (zod) holds the shape of the token answers, the
  discovery document, what the browser posts back and what a person uploads or keeps in local storage; `src/lib/env.ts`
  checks the settings the same way.
- **agentation** (a devDependency) adds a feedback toolbar in `next dev` only (`src/components/dev-tools.tsx`): annotate an
  element and the note reaches a coding agent through its local MCP endpoint (`http://localhost:4747`). It is not part of a
  production build.
- The platform's own permissions (`GET /platform/me`) decide what is shown (`src/lib/access.tsx`); the admin API enforces them.

## Layout

```
src/app/            routes: (public) sign-in screens, (app) the application, auth/ and api/ route handlers
src/features/       one directory per area: access, grant, people, roles, services, check, history, settings, shell, home
src/domain/         rules, members and change records in the words people use (no React)
src/components/     providers, kit (page header, tags, empty/error blocks), brand
src/lib/            session, env, access, undo notice, URL state
src/i18n/           ru, en and zh dictionaries (en is the source); scripts/check-locales.mjs checks them
src/theme/          the visual system as an Ant Design theme
tests/              Jest tests: domain helpers, grant form model, schemas, session cookie, the OIDC sign-in, the AuthZEN builders
                    (oidc.test.ts); tests/ui renders components
                    with mocked data (no browser, no running stack)
```

## Run

```
cp .env.example .env.local        # ADMIN_API_URL, APP_BASE_URL, OIDC_*, SESSION_SECRET
pnpm install && pnpm orval
pnpm dev                          # http://localhost:3000
```

Or with the whole stack: `docker compose up` (from the repository root) serves this app on `:3000`.
The identity provider must know the redirect URI of this app, `{APP_BASE_URL}/auth/callback`, for the client `OIDC_CLIENT_ID`
(the Compose stack registers it at Dex, `infra/dex/config.yaml`). `OIDC_AUTHORIZATION_URL` is where the browser is sent,
`OIDC_TOKEN_URL` is where this server calls (the two may differ: a Compose network name), `OIDC_CLIENT_SECRET` is left
out for a public client, and `OIDC_SCOPE` defaults to `openid profile email offline_access` (`offline_access` asks for the
refresh token that keeps the session going). The admin API needs no CORS setting: the browser talks only to this app.

## Checks

```
pnpm typecheck        # tsc (unused locals and parameters are errors)
pnpm knip             # unused files, exports and dependencies
pnpm check:i18n       # same keys and {{variables}} in en, ru and zh; every key the code uses exists
pnpm format:check     # oxfmt
pnpm build            # production build (also runs check:i18n)
pnpm test             # Jest
```

`task web:lint` runs all linters, `task web:test` the Jest tests. The interface is tested with mocked API responses, not
against a running stack: the backend has its own integration tests (`task go:test:integration`).

## Conventions that bite

- A translation key is either text or a group of keys, never both: `x.y` and `x.y.z` can't coexist. Plural forms use the
  CLDR suffixes (`key_one`, `key_few`, `key_many`, `key_other`) and are compared by their base key.
- Custom queries need their own `queryKey`: the generated hooks cache the whole response under the URL, so a key built from
  the same URL with another shape poisons the cache.
- Several URL parameters changed in one go go through `useQueryState`, which batches them; separate `router.replace` calls
  would each start from the old address.
- Ant Design 6: `Drawer size` (not `width`), `Alert title` (not `message`), notification `title`/`actions`, `Descriptions
  styles.label`. The editable `Typography.Text` hangs in a page title — use `components/editable-title.tsx`.
