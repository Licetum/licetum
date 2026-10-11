# Licetum web interface

Next.js 16 (App Router) + Ant Design 6 + TanStack Query. The visual system is in `src/theme/tokens.ts`.

## How it works

- **Sign-in is on the server**, in the way of the Next.js SDK of Auth0: `/auth/login` redirects to the identity provider
  of the platform service (through the admin API, which runs the OAuth2 flow, PKCE for a public client), `/auth/callback`
  receives the tokens in the URL fragment and posts them once to `/auth/session`, which keeps them in an **encrypted
  httpOnly cookie** (`licetum_session.0…n`, a JWE, split into chunks). The tokens never reach the page's JavaScript.
  The SDK of Auth0 itself is Auth0-only; this works with any OpenID Connect provider, Auth0 included.
- **The browser talks to the API only through `/api/admin/*`** (`src/app/api/admin/[...path]/route.ts`): the server adds the
  bearer token, renews it with the refresh token when it is about to expire, and refuses sign-in endpoints and
  cross-origin writes. `/api/check/*` is the same for "try it with your token" and `/api/discover` reads the OIDC discovery
  document of an issuer.
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
tests/              vitest unit tests (domain helpers, grant form model, session cookie)
e2e/                Playwright tests against a running stack
```

## Run

```
cp .env.example .env.local        # ADMIN_API_URL, ADMIN_API_PUBLIC_URL, APP_BASE_URL, SESSION_SECRET
pnpm install && pnpm orval
pnpm dev                          # http://localhost:3000
```

Or with the whole stack: `docker compose up` (from the repository root) serves this app on `:3000`.
The admin API must list the app's origin in `CORS__ALLOW_ORIGINS` (the sign-in redirect is checked against it), and the
identity provider must know the redirect URI of the admin API (`{admin API}/service/{platform id}/oauth/callback`).

## Checks

```
pnpm typecheck        # tsc (unused locals and parameters are errors)
pnpm knip             # unused files, exports and dependencies
pnpm check:i18n       # same keys and {{variables}} in en, ru and zh; every key the code uses exists
pnpm format:check     # oxfmt
pnpm build            # production build (also runs check:i18n)
E2E_BASE_URL=http://localhost:3000 pnpm e2e
```

`task web:lint` runs all linters, `task web:test` the unit tests; neither runs the build or the e2e tests. The e2e tests sign in with `E2E_USER` / `E2E_PASSWORD` (default:
the local development account of `infra/dex/config.yaml`).

## Conventions that bite

- A translation key is either text or a group of keys, never both: `x.y` and `x.y.z` can't coexist. Plural forms use the
  CLDR suffixes (`key_one`, `key_few`, `key_many`, `key_other`) and are compared by their base key.
- Custom queries need their own `queryKey`: the generated hooks cache the whole response under the URL, so a key built from
  the same URL with another shape poisons the cache.
- Several URL parameters changed in one go go through `useQueryState`, which batches them; separate `router.replace` calls
  would each start from the old address.
- Ant Design 6: `Drawer size` (not `width`), `Alert title` (not `message`), notification `title`/`actions`, `Descriptions
  styles.label`. The editable `Typography.Text` hangs in a page title — use `components/editable-title.tsx`.
