# docker/browser — Jarvis's cloud browser bridge.
#
# Drives a real headless Chromium via CDP (chromedp) and exposes the
# observe → act → extract verb set + a live screencast (SSE) to Core.
# Lives on Railway's private network; no LLM inside (Rule #1 substrate).
#
# Stage 1 builds a static Go binary. Stage 2 is a slim Debian runtime with
# Chromium + the fonts a real page needs so screenshots/screencast aren't
# tofu boxes.

FROM golang:1.22-bookworm AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY main.go ./
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/browser-bridge ./...

FROM debian:bookworm-slim

# chromium            the browser chromedp drives
# fonts-*             without these, real pages render as boxes
# ca-certificates     TLS for https sites
# dumb-init           reaps zombie Chromium child processes (no PID-1 leaks)
RUN apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
        chromium \
        ca-certificates \
        dumb-init \
        fonts-liberation \
        fonts-noto-core \
        fonts-noto-color-emoji \
        fonts-noto-cjk \
        libnss3 \
        libxss1 \
        libasound2 \
    && rm -rf /var/lib/apt/lists/*

# Non-root, matching the gepa/workspace sidecar convention.
RUN groupadd -r browser && useradd -r -g browser -u 10001 -m -d /home/browser browser

ENV PORT=8080
ENV CHROME_PATH=/usr/bin/chromium
ENV BROWSER_MAX_SESSIONS=2
EXPOSE 8080

COPY --from=build /out/browser-bridge /usr/local/bin/browser-bridge

USER browser

HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
  CMD ["/usr/local/bin/browser-bridge", "-healthcheck"]

ENTRYPOINT ["dumb-init", "--"]
CMD ["/usr/local/bin/browser-bridge"]
