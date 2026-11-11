# syntax=docker/dockerfile:1

# ---- build -------------------------------------------------------------------
# The tag is unversioned on purpose: Cargo.toml uses edition 2024, and the
# bookworm base keeps the build glibc version equal to the runtime image's.
FROM rust:1-bookworm AS build

WORKDIR /app
COPY . .

# SQLite is bundled and TLS is rustls, so no system libraries are needed.
RUN --mount=type=cache,target=/usr/local/cargo/registry \
    --mount=type=cache,target=/app/target,sharing=locked \
    set -eux; \
    cargo build --release --locked; \
    install -Dm755 target/release/bkd /out/bkd

# The runtime image has no shell, so create the data directory here. A new named
# volume copies its owner from this directory.
RUN mkdir /out-data

# ---- runtime: distroless with libc, no shell, non-root ----------------------
FROM gcr.io/distroless/cc-debian12:nonroot

COPY --from=build /out/bkd /bkd
COPY --from=build --chown=65532:65532 /out-data /data

VOLUME /data

ENV BKD_DB=/data/bkd.sqlite3
ENV BKD_ADDR=0.0.0.0:8080

EXPOSE 8080

# bkd has no health endpoint, so there is no HEALTHCHECK.
ENTRYPOINT ["/bkd"]
