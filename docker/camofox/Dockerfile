# docker/camofox — Jarvis's anti-detect browser bridge (Camoufox / Firefox).
#
# We do NOT fork or rebuild redf0x1/camofox-browser — they publish a
# production image to GHCR and keep a backward-compatible REST contract. So
# this Dockerfile is a thin pin over their image: the ONLY thing we add is
# binding to 0.0.0.0 (their default 127.0.0.1 is unreachable on Railway's
# private network). Everything else — Firefox runtime, Camoufox binary,
# healthcheck on 9377, CMD, the /tabs · /navigate · /snapshot · /act ·
# /evaluate · /screenshot · /health routes our Go adapter
# (core/internal/browser/camofox.go) speaks — comes from the base image.
#
# UPDATING (the whole point of pinning this way): bump CAMOFOX_VERSION below
# to the new upstream tag, redeploy. That's it — our Go adapter targets their
# stable wire contract, not their internals, so a version bump touches no Go.
# Watch their CHANGELOG / RELEASE_NOTES for any wire-shape change; they ship
# backward-compatible aliases for moved endpoints, so breakage is rare and,
# when it happens, isolated to camofox.go.
#
# Tags: https://github.com/redf0x1/camofox-browser/pkgs/container/camofox-browser

ARG CAMOFOX_VERSION=2.4.3
FROM ghcr.io/redf0x1/camofox-browser:${CAMOFOX_VERSION}

# Bind to all interfaces so Core can reach it at camofox.railway.internal:9377.
# CAMOFOX_API_KEY (required for a non-loopback bind) and any PROXY_* residential
# exit are injected as Railway env vars — never baked into the image.
ENV CAMOFOX_HOST=0.0.0.0
ENV CAMOFOX_PORT=9377

# Persistent-volume permissions fix. The upstream image runs as the non-root
# `node` user (CMD ["node","dist/src/server.js"]) and, on boot, creates its
# cookies/profiles dirs under /home/node/.camofox. Railway mounts a persistent
# volume there OWNED BY ROOT, so `node` can't write to it and the container
# crash-loops on `EACCES: mkdir '/home/node/.camofox/cookies'`. Fix: a tiny
# entrypoint that runs as root, chowns the mounted volume to `node` on each
# boot, then DROPS privileges (gosu) and execs the image's real CMD — so the
# browser still runs as `node` (Firefox refuses to run as root). gosu isn't in
# node:22-slim, so install it. Generic: works whether or not a volume is mounted.
USER root
RUN apt-get update \
 && apt-get install -y --no-install-recommends gosu \
 && rm -rf /var/lib/apt/lists/*
COPY entrypoint.sh /usr/local/bin/railway-entrypoint.sh
RUN chmod +x /usr/local/bin/railway-entrypoint.sh

EXPOSE 9377
# Wrap the upstream startup: our root entrypoint fixes volume perms, then hands
# off to the upstream CMD (re-declared so Docker passes it as args to gosu).
# HEALTHCHECK + VOLUME are still inherited from the upstream image.
ENTRYPOINT ["/usr/local/bin/railway-entrypoint.sh"]
CMD ["node", "dist/src/server.js"]
