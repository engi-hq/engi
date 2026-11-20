# syntax=docker/dockerfile:1
# Multi-stage, multi-arch-ready image:
#   docker buildx build --platform linux/amd64,linux/arm64 -t engi .

ARG NODE_VERSION=22
ARG GO_VERSION=1.27
ARG VERSION=dev

# 1. Editor build (runs on the build host's platform, output is arch-independent).
FROM --platform=$BUILDPLATFORM node:${NODE_VERSION}-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

# 2. Static Go binary with the editor embedded (cross-compiled, no cgo).
FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine AS build
ARG TARGETOS
ARG TARGETARCH
ARG VERSION
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
COPY --from=web /src/web/dist ./internal/adapter/http/ui/dist
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -tags webui -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/engi ./cmd/engi
RUN mkdir /out/data

# 3. Runtime: no shell, no package manager, non-root (uid 65532).
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/engi /engi
COPY --from=build --chown=65532:65532 /out/data /data
ENV ENGI_STORE_PATH=/data/engi.db \
    ENGI_BLOB_FS_DIR=/data/blobs
VOLUME /data
EXPOSE 8080
USER 65532:65532
HEALTHCHECK --interval=15s --timeout=5s --start-period=30s --retries=3 CMD ["/engi", "healthcheck"]
ENTRYPOINT ["/engi"]
CMD ["serve"]
