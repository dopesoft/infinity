# syntax=docker/dockerfile:1.7
FROM golang:1.26-alpine AS build
WORKDIR /src

RUN apk add --no-cache git ca-certificates
COPY core/go.mod core/go.sum ./core/
WORKDIR /src/core
RUN go mod download

WORKDIR /src
COPY core ./core
COPY db ./db

WORKDIR /src/core
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
COPY --from=build /src/db/migrations /app/db/migrations

ENV PORT=8080
EXPOSE 8080

USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/infinity"]
CMD ["serve"]
