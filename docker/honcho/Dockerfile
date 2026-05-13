# Honcho FastAPI server — built from upstream plastic-labs/honcho. (cache-bust: 2026-05-13 diagnostic-entrypoint)
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
#
# Diagnostics: shell uses `set -e -u -x` so any silent failure surfaces
# (every step echoed, undefined var aborts, non-zero exit propagates).
# Print env-summary up front so a future log-only debug session can
# tell which vars made it into the container without exposing values.
# BuildKit's COPY <<EOF parses ${VAR%%:*} as a Dockerfile substitution
# and chokes on bash-only modifiers (% # //). Writing via RUN heredoc
# pushes the body through the shell layer instead, where bash semantics
# rule and our diagnostic script lands verbatim.
RUN cat > /app/entrypoint.sh <<'EOF' && chmod +x /app/entrypoint.sh
#!/bin/bash
set -e -u -x
echo "honcho-boot: ===================================================="
echo "honcho-boot: PWD=$(pwd)"
echo "honcho-boot: PYTHON=$(command -v python || true)  ($(python --version 2>&1 || true))"
echo "honcho-boot: UVICORN=$(command -v uvicorn || true)"
echo "honcho-boot: FASTAPI=$(command -v fastapi || true)"
echo "honcho-boot: ALEMBIC=$(command -v alembic || true)"
echo "honcho-boot: PATH=$PATH"
echo "honcho-boot: DB_CONNECTION_URI scheme = ${DB_CONNECTION_URI%%:*}://"
echo "honcho-boot: LLM_OPENAI_API_KEY set = $([ -n "${LLM_OPENAI_API_KEY:-}" ] && echo yes || echo no)"
echo "honcho-boot: OPENAI_API_KEY set = $([ -n "${OPENAI_API_KEY:-}" ] && echo yes || echo no)"
echo "honcho-boot: CACHE_URL scheme = ${CACHE_URL%%:*}://"
echo "honcho-boot: PORT=${PORT:-8000}"

# Scheme rewrite: postgresql:// -> postgresql+psycopg://
case "${DB_CONNECTION_URI:-}" in
  postgresql+*) echo "honcho-boot: DB URI already has psycopg prefix" ;;
  postgresql://*) DB_CONNECTION_URI="postgresql+psycopg://${DB_CONNECTION_URI#postgresql://}"; echo "honcho-boot: rewrote scheme -> postgresql+psycopg://" ;;
  postgres://*)   DB_CONNECTION_URI="postgresql+psycopg://${DB_CONNECTION_URI#postgres://}";   echo "honcho-boot: rewrote scheme -> postgresql+psycopg://" ;;
  *) echo "honcho-boot: WARNING DB_CONNECTION_URI unrecognised or empty" ;;
esac
export DB_CONNECTION_URI

echo "honcho-boot: probing DB connectivity via python..."
python - <<'PY'
import os, sys
url = os.environ["DB_CONNECTION_URI"].replace("postgresql+psycopg://", "postgresql://", 1)
print("honcho-boot: probe url scheme =", url.split(":",1)[0])
try:
    import psycopg
    print("honcho-boot: psycopg version =", psycopg.__version__)
    with psycopg.connect(url, connect_timeout=8) as conn:
        with conn.cursor() as cur:
            cur.execute("SELECT 1")
            print("honcho-boot: DB select ->", cur.fetchone())
    print("honcho-boot: DB probe OK")
except Exception as e:
    print("honcho-boot: DB probe FAILED:", type(e).__name__, str(e)[:240])
    sys.exit(2)
PY

echo "honcho-boot: alembic upgrade head"
alembic upgrade head

echo "honcho-boot: launching uvicorn on port ${PORT:-8000}"
exec python -m uvicorn src.main:app --host 0.0.0.0 --port "${PORT:-8000}" --log-level info
EOF

CMD ["/app/entrypoint.sh"]
