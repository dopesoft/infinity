# syntax=docker/dockerfile:1.7
# Self-contained Dockerfile for the Core Go service.
# Build context = core/. Migrations are embedded into the binary via
# core/db/migrations.go so no parent-directory files are needed.

FROM golang:1.26-alpine AS build
WORKDIR /src

RUN apk add --no-cache git ca-certificates
COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=dev
ARG COMMIT=none
ARG BUILD_DATE=unknown
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w \
      -X main.version=${VERSION} \
      -X main.commit=${COMMIT} \
      -X main.date=${BUILD_DATE}" \
    -o /out/infinity ./cmd/infinity

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/infinity /usr/local/bin/infinity

ENV PORT=8080
EXPOSE 8080

USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/infinity"]
CMD ["serve"]
