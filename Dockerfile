# syntax=docker/dockerfile:1

# ---- build: cross-compile a static musl binary with cargo-zigbuild ----------
# The builder is pinned to the native build platform; zig cross-compiles to the
# target arch's musl triple, so no qemu emulation is needed and an arm64 image
# builds at the host's native speed. The C dependencies (aws-lc-sys and the
# bundled SQLite) are compiled by zig cc, so only zig has to be fetched.
# No Rust version here: rust-toolchain.toml is the single source of truth and rustup
# installs it below. Do not "simplify" this to `rust:1.99`: the un-suffixed tag
# resolves to trixie, which would be a silent Debian major bump.
FROM --platform=$BUILDPLATFORM rust:bookworm AS build

# curl + xz fetch zig; that is the only build-time system dependency.
RUN apt-get update \
    && apt-get install -y --no-install-recommends curl xz-utils \
    && rm -rf /var/lib/apt/lists/*

# Zig 0.14.1 avoids the libc++-19 bindgen requirement that 0.15+ introduces.
ARG ZIG_VERSION=0.14.1
# 0.23.0 or newer: Rust 1.98 passes `--fix-cortex-a53-843419` to the linker on
# aarch64, which zig's does not accept. cargo-zigbuild filters it out as of
# rust-cross/cargo-zigbuild#452; without that the arm64 leg fails to link.
ARG ZIGBUILD_VERSION=0.23.0
RUN cargo install cargo-zigbuild --version "${ZIGBUILD_VERSION}" --locked
RUN set -eux; \
    case "$(uname -m)" in \
      x86_64) zarch=x86_64 ;; \
      aarch64) zarch=aarch64 ;; \
      *) echo "unsupported build arch $(uname -m)" >&2; exit 1 ;; \
    esac; \
    curl -fsSL "https://ziglang.org/download/${ZIG_VERSION}/zig-${zarch}-linux-${ZIG_VERSION}.tar.xz" \
      | tar -xJ -C /opt; \
    ln -s "/opt/zig-${zarch}-linux-${ZIG_VERSION}/zig" /usr/local/bin/zig

WORKDIR /app

# Install the pinned toolchain in a layer keyed on rust-toolchain.toml alone, so
# editing source does not re-download the compiler. Any rustup proxy invocation
# triggers the install.
COPY rust-toolchain.toml .
RUN cargo --version

COPY . .

# Map Docker's TARGETARCH onto the Rust musl triple and build. `rustup target
# add` runs after the source (and rust-toolchain.toml) is in place, so it
# resolves against the pinned toolchain rather than the base image's default.
ARG TARGETARCH
RUN --mount=type=cache,target=/usr/local/cargo/registry \
    --mount=type=cache,target=/app/target,sharing=locked \
    set -eux; \
    case "$TARGETARCH" in \
      amd64) target=x86_64-unknown-linux-musl ;; \
      arm64) target=aarch64-unknown-linux-musl ;; \
      *) echo "unsupported target arch $TARGETARCH" >&2; exit 1 ;; \
    esac; \
    rustup target add "$target"; \
    cargo zigbuild --release --locked --target "$target"; \
    install -Dm755 "target/${target}/release/bkd" /out/bkd

# The runtime image has no shell, so create the data directory here. A new named
# volume copies its owner from this directory.
RUN mkdir /out-data

# ---- runtime: static distroless, no shell, non-root -------------------------
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/bkd /bkd
COPY --from=build --chown=65532:65532 /out-data /data

VOLUME /data

ENV BKD_DB=/data/bkd.sqlite3
ENV BKD_ADDR=0.0.0.0:8080

EXPOSE 8080

# bkd has no health endpoint, so there is no HEALTHCHECK.
ENTRYPOINT ["/bkd"]
