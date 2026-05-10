# GEPA optimizer sidecar — self-contained service directory.
#
# A FastAPI service that wraps DSPy + GEPA so Infinity can have skill prompts
# evolved without putting Python into the Go core. Inputs: a skill's current
# SKILL.md, recent execution traces, and an eval set. Outputs: a Pareto
# frontier of candidate variants ranked on (quality, size, latency proxy).
#
# Build context = docker/gepa/ so Railway picks this Dockerfile directly when
# the service's root directory is set to docker/gepa.
#
# Hard gates ride on top in core/internal/voyager/optimizer.go (test pass,
# size cap, semantic preservation, Trust Contract approval). The sidecar's
# only job is to *generate* candidates — it never decides to ship one.

FROM python:3.12-slim AS builder

ENV PIP_DISABLE_PIP_VERSION_CHECK=1 \
    PIP_NO_CACHE_DIR=1 \
    PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1

WORKDIR /app

COPY requirements.txt /app/requirements.txt
RUN pip install --prefix=/install -r /app/requirements.txt

FROM python:3.12-slim

ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1

WORKDIR /app
COPY --from=builder /install /usr/local
COPY server.py /app/

RUN useradd --no-create-home --shell /usr/sbin/nologin --uid 10001 gepa \
 && chown -R gepa:gepa /app
USER gepa

# Railway injects PORT; default to 8090 locally.
ENV PORT=8090
EXPOSE 8090

# Use shell-form so $PORT expands. Railway expects the service to bind to
# the port it injects via $PORT.
CMD uvicorn server:app --host 0.0.0.0 --port ${PORT:-8090} --workers 1
