# syntax=docker/dockerfile:1.7
# Self-contained Dockerfile for the Studio Next.js service.
# Build context = studio/.

FROM node:22-alpine AS deps
WORKDIR /app
RUN corepack enable && corepack prepare pnpm@11 --activate
COPY package.json pnpm-lock.yaml pnpm-workspace.yaml ./
RUN pnpm install --frozen-lockfile

FROM node:22-alpine AS build
WORKDIR /app
RUN corepack enable && corepack prepare pnpm@11 --activate
COPY --from=deps /app/node_modules ./node_modules
COPY . .
ARG NEXT_PUBLIC_CORE_URL
ARG NEXT_PUBLIC_CORE_WS_URL
ARG NEXT_PUBLIC_BUILD=dev
ARG NEXT_PUBLIC_SUPABASE_URL
ARG NEXT_PUBLIC_SUPABASE_ANON_KEY
ENV NEXT_PUBLIC_CORE_URL=$NEXT_PUBLIC_CORE_URL \
    NEXT_PUBLIC_CORE_WS_URL=$NEXT_PUBLIC_CORE_WS_URL \
    NEXT_PUBLIC_BUILD=$NEXT_PUBLIC_BUILD \
    NEXT_PUBLIC_SUPABASE_URL=$NEXT_PUBLIC_SUPABASE_URL \
    NEXT_PUBLIC_SUPABASE_ANON_KEY=$NEXT_PUBLIC_SUPABASE_ANON_KEY \
    NEXT_TELEMETRY_DISABLED=1 \
    CI=true
RUN pnpm build

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
