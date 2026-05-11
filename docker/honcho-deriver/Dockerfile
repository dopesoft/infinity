# Honcho deriver worker — same image as docker/honcho/, different CMD.
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
# verbatim across both services.
CMD ["sh", "-c", "\
  if [ -n \"$DB_CONNECTION_URI\" ]; then \
    case \"$DB_CONNECTION_URI\" in \
      postgresql+*) ;; \
      postgresql://*) DB_CONNECTION_URI=\"postgresql+psycopg://${DB_CONNECTION_URI#postgresql://}\" ;; \
      postgres://*)   DB_CONNECTION_URI=\"postgresql+psycopg://${DB_CONNECTION_URI#postgres://}\" ;; \
    esac; \
    export DB_CONNECTION_URI; \
  fi; \
  echo 'deriver: waiting briefly for api migrations' && sleep 5 && \
  exec python -m src.deriver \
"]
