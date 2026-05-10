# Honcho FastAPI server — built from upstream plastic-labs/honcho.
#
# We pin to a branch and rebuild from source on each image push. The
# upstream repo's own Dockerfile assumes its source layout is at the build
# context root; we don't have that locally so we clone it during the build
# and run the equivalent steps single-stage.
#
# Build context = docker/honcho/  (Railway service root)

FROM python:3.13-slim-bookworm

ARG HONCHO_REF=main

ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    UV_COMPILE_BYTECODE=1 \
    UV_LINK_MODE=copy \
    PATH="/app/.venv/bin:/root/.local/bin:$PATH"

RUN apt-get update \
 && apt-get install -y --no-install-recommends git ca-certificates curl \
 && rm -rf /var/lib/apt/lists/*

# Install uv (Honcho's package manager).
COPY --from=ghcr.io/astral-sh/uv:0.5.11 /uv /usr/local/bin/uv

WORKDIR /app
RUN git clone --depth 1 --branch "$HONCHO_REF" https://github.com/plastic-labs/honcho.git /app

# Install runtime deps into the venv.
RUN uv sync --frozen --no-group dev

# Make sure all installed scripts are executable. uv occasionally leaves
# entry-points without the exec bit on multi-arch builds; this is cheap
# insurance.
RUN chmod -R a+rX /app /app/.venv && find /app/.venv/bin -type f -exec chmod a+x {} \;

# Run as root inside the container — Railway already sandboxes per-service
# and the container has no persistent volumes, so the non-root pattern
# isn't load-bearing here. (Upstream uses a non-root user; we drop that
# only so installed venv binaries stay executable without further work.)
EXPOSE 8000

# Run uvicorn directly via python -m so we don't depend on the fastapi
# shell-script wrapper having +x. Honcho's app object is src.main:app.
ENV PORT=8000

# Honcho expects an async SQLAlchemy URL (postgresql+psycopg://) but
# Railway/Supabase emit plain postgresql:// URLs. We rewrite the prefix
# at container startup so the secret never has to be edited by hand and
# Railway reference variables (${{core.DATABASE_URL}}) keep working.
CMD ["sh", "-c", "\
  if [ -n \"$DB_CONNECTION_URI\" ]; then \
    case \"$DB_CONNECTION_URI\" in \
      postgresql+*) ;; \
      postgresql://*) DB_CONNECTION_URI=\"postgresql+psycopg://${DB_CONNECTION_URI#postgresql://}\" ;; \
      postgres://*)   DB_CONNECTION_URI=\"postgresql+psycopg://${DB_CONNECTION_URI#postgres://}\" ;; \
    esac; \
    export DB_CONNECTION_URI; \
  fi; \
  exec python -m uvicorn src.main:app --host 0.0.0.0 --port ${PORT:-8000} \
"]
