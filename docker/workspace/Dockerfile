# docker/workspace — Jarvis's cloud filesystem bridge.
#
# Speaks the same five-primitive protocol as the Mac bridge (fs/save,
# fs/edit, bash, git/*, health). Lives on Railway's private network.
# No LLM inside — Jarvis's cognition stays in Core (ChatGPT subscription
# via openai_oauth). This image is pure substrate.
#
# Stage 1 builds a static Go binary. Stage 2 is the runtime, kept lean
# but with git/curl/build-essential available because `bash` needs them
# for any real coding workflow.

FROM golang:1.22-bookworm AS build

WORKDIR /src
COPY go.mod ./
COPY main.go ./
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/workspace-bridge ./...

FROM debian:bookworm-slim

# Tooling Jarvis will actually use through /bash:
#  - git           commits, push/pull (auth via env)
#  - curl, ca-certificates  outbound network for installs / api calls
#  - openssh-client         in case Jarvis chooses ssh over https for git
#  - build-essential        gcc/make for native deps (node-gyp, etc.)
#  - python3, pipx          common scripting / test runners
#  - jq, ripgrep            text munging without bloating bash output
#  - nodejs, npm, pnpm      via NodeSource — feature parity with the Mac
#
# Heavy toolchains (Go, Bun, Rust) install on first use via bash since
# baking them all in bloats the image and most sessions touch only one.

RUN apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
        ca-certificates \
        curl \
        git \
        openssh-client \
        build-essential \
        python3 \
        python3-pip \
        python3-venv \
        pipx \
        jq \
        ripgrep \
        less \
        nano \
        unzip \
        # ── document-generation stack (models Claude's file-ops skills) ──
        # LibreOffice headless: the quiet workhorse for format conversion,
        # XLSX formula recalc, and rendering Office docs to PDF/images.
        libreoffice-calc \
        libreoffice-writer \
        libreoffice-impress \
        # PDF toolchain: poppler (pdftotext/pdfimages/pdf2image), qpdf,
        # pdftk (merge/split/form-fill), ghostscript, tesseract OCR.
        poppler-utils \
        qpdf \
        pdftk-java \
        ghostscript \
        tesseract-ocr \
        fonts-liberation \
    && curl -fsSL https://deb.nodesource.com/setup_22.x | bash - \
    && apt-get install -y --no-install-recommends nodejs \
    && npm install -g pnpm \
    && rm -rf /var/lib/apt/lists/*

# Baked document-generation helpers. document_create (core tool) dispatches
# the structured spec to these via the /docgen endpoint, so the agent never
# hand-writes docx/pptx code (that's the jank we're avoiding).
#   DOCX/PPTX → JavaScript (docx-js / pptxgenjs)  — cleaner Office output
#   XLSX/PDF  → Python (openpyxl / reportlab + pypdf/pdfplumber/OCR)
COPY docgen/ /opt/docgen/
RUN cd /opt/docgen \
    && npm install --omit=dev --no-audit --no-fund \
    && python3 -m venv venv \
    && ./venv/bin/pip install --no-cache-dir -r requirements.txt
ENV DOCGEN_DIR=/opt/docgen

WORKDIR /workspace
ENV WORKSPACE_ROOT=/workspace
ENV PORT=8080
EXPOSE 8080

COPY --from=build /out/workspace-bridge /usr/local/bin/workspace-bridge

# Healthcheck Railway can hit; Studio also pre-warms via the same path.
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD curl -fsS http://localhost:${PORT}/health || exit 1

CMD ["/usr/local/bin/workspace-bridge"]
