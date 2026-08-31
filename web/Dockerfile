# syntax=docker/dockerfile:1
# Builds the Converge web app (Next.js, standalone output).
# Build context: repository root (pnpm workspace).

FROM node:20-alpine AS base
RUN npm install -g corepack && corepack enable && apk add --no-cache dumb-init
WORKDIR /app
ENV COREPACK_ENABLE_DOWNLOAD_PROMPT=0

# Workspace manifests (lockfile enables a warm install cache layer)
COPY pnpm-lock.yaml pnpm-workspace.yaml package.json turbo.json ./
COPY web/package.json web/
COPY packages/ui/package.json packages/ui/
COPY packages/types/package.json packages/types/
COPY packages/services/package.json packages/services/
COPY tooling/eslint-config/package.json tooling/eslint-config/
COPY tooling/typescript-config/package.json tooling/typescript-config/

FROM base AS deps
RUN pnpm install --frozen-lockfile

FROM deps AS build
# NEXT_PUBLIC_* values are baked into the client bundle at build time. The
# browser connects to NEXT_PUBLIC_BACKEND_HOST directly for the SSE stream,
# so it must be browser-reachable (localhost for local setups, the public
# origin behind a reverse proxy). The server-side /api proxy targets
# NEXT_PUBLIC_BACKEND_HOST unless API_PROXY_TARGET is set at runtime
# (web/src/pages/api/[...path].js) — that is how a single-domain
# deployment proxies to the internal API service.
ARG NEXT_PUBLIC_BACKEND_HOST=http://localhost:3001
ARG NEXT_PUBLIC_BASE_HOST=http://localhost:3000
ARG NEXT_PUBLIC_VERSION=0.1.0
ARG NEXT_PUBLIC_NODE_ENV=production
ENV NEXT_PUBLIC_BACKEND_HOST=$NEXT_PUBLIC_BACKEND_HOST \
    NEXT_PUBLIC_BASE_HOST=$NEXT_PUBLIC_BASE_HOST \
    NEXT_PUBLIC_VERSION=$NEXT_PUBLIC_VERSION \
    NEXT_PUBLIC_NODE_ENV=$NEXT_PUBLIC_NODE_ENV
COPY . .
RUN pnpm turbo run build --filter=web

# Minimal runtime image: the traced standalone server plus the client
# static assets it serves (Next's canonical standalone layout).
FROM node:20-alpine AS runner
RUN apk add --no-cache dumb-init
ENV NODE_ENV=production
WORKDIR /app
# The standalone output mirrors the monorepo root with the app nested
# under web/ (Next's monorepo layout), so the app root is /app/web.
COPY --from=build /app/web/.next/standalone ./
COPY --from=build /app/web/.next/static /app/web/.next/static
COPY --from=build /app/web/public /app/web/public
EXPOSE 3000
CMD ["dumb-init", "node", "web/server.js"]
