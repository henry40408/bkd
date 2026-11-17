# syntax=docker/dockerfile:1

# ---- build: cross-compile a static Go binary --------------------------------
# Runs on the native build platform; Go cross-compiles to the target with
# CGO off, so no qemu and no C toolchain.
# No Go version tag: the image's Go is used when it is at least go.mod's
# `toolchain` line; otherwise GOTOOLCHAIN=auto fetches that line (auto only
# moves up). Keep the `bookworm` suffix: a bare tag resolves to trixie, a
# silent Debian major bump.
FROM --platform=$BUILDPLATFORM golang:bookworm AS build

WORKDIR /app

# The official image sets GOTOOLCHAIN=local, which ignores go.mod's toolchain
# line. Fetch a newer toolchain in a layer keyed on go.mod/go.sum alone, so
# source edits do not re-download it.
ENV GOTOOLCHAIN=auto
COPY go.mod go.sum ./
RUN go version

COPY . .

# `vendor/` ships in the repository, so the build needs no network.
ARG TARGETOS
ARG TARGETARCH
ARG GIT_VERSION=dev
RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS="$TARGETOS" GOARCH="$TARGETARCH" \
    go build -mod=vendor -trimpath \
      -ldflags "-s -w -X github.com/henry40408/bkd/internal/version.version=${GIT_VERSION}" \
      -o /out/bkd ./cmd/bkd

# The runtime image has no shell, so create the data directory here. A new named
# volume copies its owner from this directory.
RUN mkdir /out-data

# ---- runtime: static distroless, no shell, non-root -------------------------
# uid 65532 is what existing volumes are owned by; do not change it.
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/bkd /bkd
COPY --from=build --chown=65532:65532 /out-data /data

VOLUME /data

ENV BKD_DB=/data/bkd.sqlite3
ENV BKD_ADDR=0.0.0.0:8080
# BKD_LOG_LEVEL (debug|info|warn|error, default info) is left unset so the
# default applies. bkd refuses to start while the retired RUST_LOG is set.

EXPOSE 8080

# bkd has no health endpoint, so there is no HEALTHCHECK.
ENTRYPOINT ["/bkd"]
