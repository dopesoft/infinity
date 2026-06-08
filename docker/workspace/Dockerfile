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
COPY *.go ./
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
        # ── media + motion-graphics rendering stack ──
        # ffmpeg: cut/crop/caption/encode for shorts AND the design renderer's
        #   encode step (image-sequence -> ProRes/mp4). Also self-installs via
        #   the ffmpeg CLI extension; baking it guarantees the renderer's path.
        # chromium: headless engine the design renderer drives (puppeteer-core
        #   points at /usr/bin/chromium). fonts-* keep text rendering correct.
        ffmpeg \
        chromium \
        fonts-noto-color-emoji \
        fonts-noto-cjk \
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

# Baked HTML->video renderer (the design engine's finishing harness). The
# render-html-to-prores skill invokes `node /opt/render/render.js …` through
# media_job. puppeteer-core drives the system chromium (no bundled download),
# so the image stays lean. RENDER_DIR is read by the skill; PUPPETEER_* pins
# the engine + skips the chromium download at npm install.
ENV PUPPETEER_SKIP_DOWNLOAD=1
ENV PUPPETEER_EXECUTABLE_PATH=/usr/bin/chromium
COPY render/ /opt/render/
RUN cd /opt/render && npm install --omit=dev --no-audit --no-fund
ENV RENDER_DIR=/opt/render

# Pre-bake the Go toolchain (pinned to infinity core's go.mod — 1.26.x) so the
# cloud workspace can `go build ./... && go vet ./... && go test ./...` its own
# fix during nightly self-heal WITHOUT a run-time download. This is the
# reliability the boss asked for: when the Mac is offline, self-heal runs here,
# and it must never stall waiting on a network fetch of the compiler.
# GOTOOLCHAIN=local pins the installed version so `go` never auto-downloads a
# different one mid-run. GOPATH/GOCACHE live under the persistent /workspace
# volume so the module cache survives across runs.
ENV GO_VERSION=1.26.3
RUN curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz" -o /tmp/go.tgz \
    && tar -C /usr/local -xzf /tmp/go.tgz \
    && rm /tmp/go.tgz \
    && /usr/local/go/bin/go version
ENV PATH="/usr/local/go/bin:${PATH}"
ENV GOPATH=/workspace/.go
ENV GOCACHE=/workspace/.gocache
ENV GOTOOLCHAIN=local

WORKDIR /workspace
ENV WORKSPACE_ROOT=/workspace
ENV PORT=8080
EXPOSE 8080

COPY --from=build /out/workspace-bridge /usr/local/bin/workspace-bridge

# Healthcheck Railway can hit; Studio also pre-warms via the same path.
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD curl -fsS http://localhost:${PORT}/health || exit 1

CMD ["/usr/local/bin/workspace-bridge"]
