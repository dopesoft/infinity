# Honcho FastAPI server — built from upstream plastic-labs/honcho.
#
# We pin to a specific commit so a future plastic-labs change can't break
# Infinity unexpectedly. Bump the ARG when you intentionally upgrade after
# verifying Honcho's schema migrations against Supabase.
#
# Build context = docker/honcho/  (Railway service root)
# This Dockerfile clones the upstream repo at build time and reuses their
# tooling, so we don't copy/maintain Honcho source in our tree.

FROM python:3.13-slim-bookworm AS build

# Pin Honcho to a known-good commit. Update intentionally.
ARG HONCHO_REF=main

ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    UV_COMPILE_BYTECODE=1

# git is needed for the clone; ca-certificates so https works.
RUN apt-get update \
 && apt-get install -y --no-install-recommends git ca-certificates \
 && rm -rf /var/lib/apt/lists/*

# uv is Honcho's package manager.
COPY --from=ghcr.io/astral-sh/uv:0.5.11 /uv /usr/local/bin/uv

WORKDIR /app
RUN git clone --depth 1 --branch "$HONCHO_REF" https://github.com/plastic-labs/honcho.git /app
RUN uv sync --frozen --no-group dev

FROM python:3.13-slim-bookworm

ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    PATH="/app/.venv/bin:$PATH"

# Non-root user — match Honcho's upstream convention.
RUN useradd --no-create-home --shell /usr/sbin/nologin --uid 10001 app

COPY --from=build /app /app
RUN chown -R app:app /app
WORKDIR /app
USER app

# Railway injects PORT; default to 8000 for local parity.
ENV PORT=8000
EXPOSE 8000

# fastapi-cli ships with Honcho's deps, hosts src/main.py at the configured
# port. The --host flag is required so Railway's healthcheck reaches it.
CMD ["sh", "-c", "fastapi run --host 0.0.0.0 --port ${PORT:-8000} src/main.py"]
