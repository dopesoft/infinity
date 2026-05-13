# syntax=docker/dockerfile:1.7
# Self-contained Dockerfile for the Studio Next.js service.
# Build context = studio/.

FROM node:22-alpine AS deps
WORKDIR /app
RUN corepack enable && corepack prepare pnpm@11 --activate
COPY package.json pnpm-lock.yaml pnpm-workspace.yaml ./
# BuildKit cache mount on the pnpm content-addressable store. Tarballs
# from npmjs.org survive across builds; only the unpacked node_modules/
# is fresh per build. Speeds up rebuilds where deps haven't changed.
RUN --mount=type=cache,id=pnpm-store,target=/root/.local/share/pnpm/store \
    pnpm install --frozen-lockfile

FROM node:22-alpine AS build
WORKDIR /app
RUN corepack enable && corepack prepare pnpm@11 --activate
COPY --from=deps /app/node_modules ./node_modules
COPY . .
# Production defaults for the NEXT_PUBLIC_* baked into the build. Railway
# is supposed to inject these as build args via service env vars, but it
# silently no-ops when the variable name doesn't match a build-arg
# declaration on its end — leaving the ARG empty and the production
# bundle pointed at nothing. These defaults are the safety net so a
# vanilla `docker build` (or a Railway build where the env-→-build-arg
# linkage is misconfigured) still produces a working production bundle.
# Pass `--build-arg NEXT_PUBLIC_CORE_URL=...` to override.
ARG NEXT_PUBLIC_CORE_URL=https://core-production-84d4.up.railway.app
ARG NEXT_PUBLIC_CORE_WS_URL=wss://core-production-84d4.up.railway.app/ws
ARG NEXT_PUBLIC_BUILD=dev
ARG NEXT_PUBLIC_SUPABASE_URL
ARG NEXT_PUBLIC_SUPABASE_ANON_KEY
ARG NEXT_PUBLIC_PREVIEW_URL=https://preview.dopesoft.io
ENV NEXT_PUBLIC_CORE_URL=$NEXT_PUBLIC_CORE_URL \
    NEXT_PUBLIC_CORE_WS_URL=$NEXT_PUBLIC_CORE_WS_URL \
    NEXT_PUBLIC_BUILD=$NEXT_PUBLIC_BUILD \
    NEXT_PUBLIC_SUPABASE_URL=$NEXT_PUBLIC_SUPABASE_URL \
    NEXT_PUBLIC_SUPABASE_ANON_KEY=$NEXT_PUBLIC_SUPABASE_ANON_KEY \
    NEXT_PUBLIC_PREVIEW_URL=$NEXT_PUBLIC_PREVIEW_URL \
    NEXT_TELEMETRY_DISABLED=1 \
    CI=true
# BuildKit cache mount on Next.js's incremental compile cache. Without
# this, every build does a cold webpack pass — Railway logs the
# "No build cache found. Please configure build caching" warning to
# advertise the fix. With the mount, only-changed-routes rebuild and
# the steady-state build drops to ~10s on Studio's surface area.
RUN --mount=type=cache,id=next-cache,target=/app/.next/cache \
    pnpm build

FROM node:22-alpine AS runner
WORKDIR /app
ENV NODE_ENV=production
ENV NEXT_TELEMETRY_DISABLED=1

RUN addgroup --system --gid 1001 nodejs && adduser --system --uid 1001 nextjs

COPY --from=build /app/public ./public
COPY --from=build --chown=nextjs:nodejs /app/.next/standalone ./
COPY --from=build --chown=nextjs:nodejs /app/.next/static ./.next/static

USER nextjs
ENV PORT=3000
EXPOSE 3000

CMD ["node", "server.js"]
