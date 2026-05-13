# Honcho deriver worker — same image as docker/honcho/, different CMD. (cache-bust: 2026-05-13 diagnostic-entrypoint)
#
# The deriver is a long-running background process that consumes new
# messages from Redis and updates each peer's reasoned representation.
# Without it, Honcho's API still works (the FastAPI server in
# docker/honcho/) but representations never refresh from new traffic.
#
# We duplicate the Dockerfile rather than parameterising it via an ENV
# switch because Railway services build images independently and the
# duplication is six lines of code. When upstream Honcho ships an
# official ghcr image we replace both with FROM <image>.

FROM python:3.13-slim-bookworm

ARG HONCHO_REF=main

ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    UV_COMPILE_BYTECODE=1 \
    UV_LINK_MODE=copy \
    PATH="/app/.venv/bin:$PATH"

RUN apt-get update \
 && apt-get install -y --no-install-recommends git ca-certificates curl \
 && rm -rf /var/lib/apt/lists/*

COPY --from=ghcr.io/astral-sh/uv:0.5.11 /uv /usr/local/bin/uv

WORKDIR /app
RUN git clone --depth 1 --branch "$HONCHO_REF" https://github.com/plastic-labs/honcho.git /app

RUN uv sync --frozen --no-group dev
RUN chmod -R a+rX /app /app/.venv && find /app/.venv/bin -type f -exec chmod a+x {} \;

# No exposed port — the deriver is a worker, not a server.

# Same DB URL rewrite as the API service so Railway reference variables work
# verbatim across both services. Mirrors docker/honcho/Dockerfile's diagnostic
# entrypoint so a silent crash leaves a usable log trail.
# BuildKit's COPY <<EOF chokes on bash-modifier syntax in interpolation.
# Use RUN heredoc so the script body is handled by the shell, not the
# Dockerfile parser.
RUN cat > /app/entrypoint.sh <<'EOF' && chmod +x /app/entrypoint.sh
#!/bin/bash
set -e -u -x
echo "deriver-boot: ===================================================="
echo "deriver-boot: PWD=$(pwd)"
echo "deriver-boot: PYTHON=$(command -v python || true)  ($(python --version 2>&1 || true))"
echo "deriver-boot: PATH=$PATH"
echo "deriver-boot: DB_CONNECTION_URI scheme = ${DB_CONNECTION_URI%%:*}://"
echo "deriver-boot: LLM_OPENAI_API_KEY set = $([ -n "${LLM_OPENAI_API_KEY:-}" ] && echo yes || echo no)"
echo "deriver-boot: CACHE_URL scheme = ${CACHE_URL%%:*}://"

case "${DB_CONNECTION_URI:-}" in
  postgresql+*) echo "deriver-boot: DB URI already psycopg" ;;
  postgresql://*) DB_CONNECTION_URI="postgresql+psycopg://${DB_CONNECTION_URI#postgresql://}"; echo "deriver-boot: rewrote scheme" ;;
  postgres://*)   DB_CONNECTION_URI="postgresql+psycopg://${DB_CONNECTION_URI#postgres://}";   echo "deriver-boot: rewrote scheme" ;;
  *) echo "deriver-boot: WARNING DB_CONNECTION_URI unrecognised or empty" ;;
esac
export DB_CONNECTION_URI

echo "deriver-boot: waiting briefly for api migrations"
sleep 5

echo "deriver-boot: probing deriver module import..."
python - <<'PY'
import importlib, sys, traceback
try:
    m = importlib.import_module("src.deriver")
    print("deriver-boot: src.deriver imported:", m.__file__)
except Exception:
    print("deriver-boot: src.deriver import FAILED")
    traceback.print_exc()
    sys.exit(3)
PY

echo "deriver-boot: launching deriver"
exec python -m src.deriver
EOF

CMD ["/app/entrypoint.sh"]
