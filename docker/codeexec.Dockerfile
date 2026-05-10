# Sandboxed Python execution sidecar.
# Core never executes user code — it posts JSON to this service which runs
# Python under hard CPU/memory limits with no network.
#
# Build:   docker build -f docker/codeexec.Dockerfile -t infinity-codeexec .
# Deploy:  set CODEEXEC_URL on the core service to this container's URL.

FROM python:3.12-alpine

RUN apk add --no-cache tini

WORKDIR /app
RUN pip install --no-cache-dir fastapi==0.115.* uvicorn==0.32.*

COPY <<'EOF' /app/server.py
import json
import os
import resource
import subprocess
import sys
import tempfile
import time
from fastapi import FastAPI
from pydantic import BaseModel

# Hard limits enforced via setrlimit before exec.
CPU_SECONDS = 10
MEMORY_BYTES = 512 * 1024 * 1024  # 512 MB
MAX_OUTPUT = 64 * 1024            # 64 KB stdout/stderr each

app = FastAPI()


class RunRequest(BaseModel):
    language: str = "python"
    code: str


def set_limits():
    resource.setrlimit(resource.RLIMIT_CPU, (CPU_SECONDS, CPU_SECONDS))
    resource.setrlimit(resource.RLIMIT_AS, (MEMORY_BYTES, MEMORY_BYTES))
    resource.setrlimit(resource.RLIMIT_FSIZE, (1 * 1024 * 1024, 1 * 1024 * 1024))
    # No network: rely on the container having no NIC; alpine drop is at deploy.


@app.post("/run")
def run(req: RunRequest):
    if req.language != "python":
        return {"stdout": "", "stderr": f"unsupported language: {req.language}", "exit_code": 1, "duration_ms": 0}

    started = time.monotonic_ns()
    with tempfile.NamedTemporaryFile(suffix=".py", delete=False, mode="w") as tf:
        tf.write(req.code)
        path = tf.name

    try:
        proc = subprocess.run(
            [sys.executable, "-I", path],
            capture_output=True,
            text=True,
            timeout=CPU_SECONDS + 2,
            preexec_fn=set_limits,
            env={"PYTHONDONTWRITEBYTECODE": "1", "PATH": os.environ.get("PATH", "")},
        )
        out = proc.stdout[:MAX_OUTPUT]
        err = proc.stderr[:MAX_OUTPUT]
        return {
            "stdout": out,
            "stderr": err,
            "exit_code": proc.returncode,
            "duration_ms": int((time.monotonic_ns() - started) / 1_000_000),
        }
    except subprocess.TimeoutExpired as e:
        return {
            "stdout": (e.stdout or "")[:MAX_OUTPUT] if isinstance(e.stdout, str) else "",
            "stderr": "TIMEOUT after %d seconds" % CPU_SECONDS,
            "exit_code": 124,
            "duration_ms": int((time.monotonic_ns() - started) / 1_000_000),
        }
    finally:
        try:
            os.unlink(path)
        except OSError:
            pass


@app.get("/health")
def health():
    return {"status": "ok", "cpu_seconds": CPU_SECONDS, "memory_mb": MEMORY_BYTES // (1024 * 1024)}
EOF

EXPOSE 8000
ENTRYPOINT ["/sbin/tini", "--"]
CMD ["uvicorn", "server:app", "--host", "0.0.0.0", "--port", "8000"]
