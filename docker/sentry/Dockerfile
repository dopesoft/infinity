# docker/sentry — Infinity's EXTERNAL deploy watchdog.
#
# A separate Railway service with ZERO dependency on core's code (its own
# go.mod, stdlib only). It polls core's /readyz from outside and, on a sustained
# OUTAGE, heals autonomously: Railway rollback (instant, best-effort) + git
# revert of the bad commit(s) on main (authoritative) + notify. This is the
# safety net the in-core post-deploy-verify cron CAN'T be — because when core is
# down, the in-core watcher is down too.
#
# Runtime needs git (revert + push) and ca-certificates (HTTPS to GitHub +
# Railway API). Tiny image; no LLM, no DB.

FROM golang:1.22-bookworm AS build
WORKDIR /src
COPY go.mod ./
COPY *.go ./
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/sentry ./...

FROM debian:bookworm-slim
RUN apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
        ca-certificates \
        curl \
        git \
    && rm -rf /var/lib/apt/lists/*

ENV PORT=8080
EXPOSE 8080

COPY --from=build /out/sentry /usr/local/bin/sentry

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD curl -fsS http://localhost:${PORT}/health || exit 1

CMD ["/usr/local/bin/sentry"]
