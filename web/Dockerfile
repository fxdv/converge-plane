# syntax=docker/dockerfile:1
# Builds the Circle web app (Next.js, standalone output).
# Build context: repository root (pnpm workspace).

FROM node:20-alpine AS base
RUN npm install -g corepack && corepack enable && apk add --no-cache dumb-init bash
WORKDIR /app

# Workspace manifests (lockfile enables a warm install cache layer)
COPY pnpm-lock.yaml pnpm-workspace.yaml package.json turbo.json ./
COPY web/package.json web/
COPY packages/ui/package.json packages/ui/
COPY packages/types/package.json packages/types/
COPY packages/services/package.json packages/services/
COPY tooling/eslint-config/package.json tooling/eslint-config/
COPY tooling/typescript-config/package.json tooling/typescript-config/

FROM base AS deps
RUN pnpm install --no-frozen-lockfile

FROM deps AS build
ARG NEXT_PUBLIC_BACKEND_HOST=http://localhost:3001
ARG NEXT_PUBLIC_BASE_HOST=http://localhost:3000
ARG NEXT_PUBLIC_VERSION=0.1.0
ARG NEXT_PUBLIC_NODE_ENV=production
# PROD_ placeholders are substituted at container start (entrypoint.sh)
# for values that must be deployment-specific.
ENV NEXT_PUBLIC_BACKEND_HOST=$NEXT_PUBLIC_BACKEND_HOST \
    NEXT_PUBLIC_BASE_HOST=$NEXT_PUBLIC_BASE_HOST \
    NEXT_PUBLIC_VERSION=$NEXT_PUBLIC_VERSION \
    NEXT_PUBLIC_NODE_ENV=$NEXT_PUBLIC_NODE_ENV \
    NEXT_PUBLIC_SENTRY_DSN= \
    NEXT_PUBLIC_POSTHOG_KEY= \
    NEXT_PUBLIC_POSTHOG_HOST=
COPY . .
RUN pnpm turbo run build --filter=web

FROM node:20-alpine AS runner
RUN apk add --no-cache dumb-init bash
ENV NODE_ENV=production
WORKDIR /app
COPY --from=build /app ./
EXPOSE 3000
CMD ["sh", "web/entrypoint.sh"]
