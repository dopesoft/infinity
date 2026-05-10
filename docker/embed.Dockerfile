# Optional: embedding sidecar service.
# Used when INFINITY_EMBED=http and INFINITY_EMBED_URL points to this container.
# Default Core uses the stub embedder — only deploy this for production-quality
# semantic retrieval against the memory subsystem.

FROM python:3.12-slim
WORKDIR /app

ENV PIP_DISABLE_PIP_VERSION_CHECK=1 \
    PYTHONUNBUFFERED=1 \
    HF_HOME=/app/.cache/huggingface

RUN pip install --no-cache-dir \
    fastapi==0.115.* \
    uvicorn==0.32.* \
    sentence-transformers==3.* \
    torch==2.4.* --index-url https://download.pytorch.org/whl/cpu

COPY <<'EOF' /app/server.py
from fastapi import FastAPI
from pydantic import BaseModel
from sentence_transformers import SentenceTransformer

model = SentenceTransformer("sentence-transformers/all-MiniLM-L6-v2")
app = FastAPI()

class EmbedRequest(BaseModel):
    text: str

@app.post("/embed")
def embed(req: EmbedRequest):
    vec = model.encode(req.text, normalize_embeddings=True).tolist()
    return {"embedding": vec}

@app.get("/health")
def health():
    return {"status": "ok", "dim": 384}
EOF

EXPOSE 8000
CMD ["uvicorn", "server:app", "--host", "0.0.0.0", "--port", "8000"]
