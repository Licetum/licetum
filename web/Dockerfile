# Build from the repository root: docker build -f web/Dockerfile --target <stage> .
FROM node:24-bookworm-slim AS base

# CI image for the quality checks (task check runs inside it).
FROM base AS ci

ARG TASK_VERSION=3.53.1

RUN npm install -g @go-task/cli@${TASK_VERSION}

# Dependencies + code: used by docker compose (dev, the code is mounted on top).
FROM base AS build-deps

WORKDIR /app

COPY web/package.json web/pnpm-lock.yaml web/pnpm-workspace.yaml ./
RUN corepack enable && pnpm install --frozen-lockfile

COPY web/ .

# The API client is not stored in git: it is generated from the admin API schema
# (orval.config.ts points at ../admin-api/schema).
COPY admin-api/schema /admin-api/schema
RUN pnpm orval

FROM build-deps AS prod-builder

RUN pnpm build

# A small server: Next.js traces what it needs into .next/standalone. Everything is configured at run time by the
# environment (ADMIN_API_URL, ADMIN_API_PUBLIC_URL, CHECK_API_URL, APP_BASE_URL, SESSION_SECRET), nothing is baked in.
FROM node:24-bookworm-slim AS prod

ENV NODE_ENV=production \
    PORT=3000 \
    HOSTNAME=0.0.0.0

WORKDIR /app

COPY --from=prod-builder --chown=node:node /app/.next/standalone ./
COPY --from=prod-builder --chown=node:node /app/.next/static ./.next/static
COPY --from=prod-builder --chown=node:node /app/public ./public

USER node

EXPOSE 3000

CMD ["node", "server.js"]
