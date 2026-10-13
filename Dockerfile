# syntax=docker/dockerfile:1.7

FROM golang:1.27.2-bookworm AS build

WORKDIR /src

# Keep module downloads in a cached layer until dependency manifests change.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .

ARG TARGETOS=linux
ARG TARGETARCH=amd64
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
      go build -trimpath -buildvcs=false -ldflags="-s -w" -o /out/server . && \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
      go build -trimpath -buildvcs=false -ldflags="-s -w" -o /out/migrate ./cmd/migrate && \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
      go build -trimpath -buildvcs=false -ldflags="-s -w" -o /out/create-admin ./cmd/create-admin

FROM alpine:3.24 AS runtime

RUN apk add --no-cache ca-certificates tzdata wget \
    && addgroup -S -g 10001 app \
    && adduser -S -D -H -u 10001 -G app app \
    && mkdir -p /app/uploads/public /app/uploads/private/objects /app/uploads/private/exports \
    && chown -R 10001:10001 /app/uploads

WORKDIR /app
COPY --from=build --chown=0:0 /out/server /app/server
COPY --from=build --chown=0:0 /out/migrate /app/migrate
COPY --from=build --chown=0:0 /out/create-admin /app/create-admin

ENV PORT=3000
EXPOSE 3000
USER 10001:10001

HEALTHCHECK --interval=30s --timeout=3s --start-period=30s --retries=3 \
    CMD wget -q -O /dev/null http://127.0.0.1:3000/ready || exit 1

ENTRYPOINT ["/app/server"]
