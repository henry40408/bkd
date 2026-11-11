# syntax=docker/dockerfile:1

# ---- build -------------------------------------------------------------------
# No Rust version here: rust-toolchain.toml is the single source of truth and rustup
# installs it below. Do not "simplify" this to `rust:1.99`: the un-suffixed tag
# resolves to trixie, which would be a silent Debian major bump. The bookworm base
# keeps the build glibc version equal to the runtime image's.
FROM rust:bookworm AS build

WORKDIR /app

# Install the pinned toolchain in a layer keyed on rust-toolchain.toml alone, so
# editing source does not re-download the compiler. Any rustup proxy invocation
# triggers the install.
COPY rust-toolchain.toml .
RUN cargo --version

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
