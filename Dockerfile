# syntax=docker/dockerfile:1

# Build on the host platform and cross-compile instead of emulating the target
# architecture: TARGETOS/TARGETARCH are injected by buildx, so arm64 builds do
# not need QEMU. Never remove --platform=$BUILDPLATFORM: without it the Go
# toolchain itself would run under emulation on the arm64 pass.
FROM --platform=$BUILDPLATFORM golang:1.24-alpine AS build

ARG TARGETOS
ARG TARGETARCH

# Release version, without the leading v (0.3.2). Required: an image must never
# claim a version that no tag owns. `.git` is not in the build context, so the
# Dockerfile cannot read the tag itself — the Makefile and CI pass VERSION, and
# the build fails loudly when it is missing.
ARG VERSION

RUN printf '%s' "$VERSION" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$' || { \
	echo "error: build arg VERSION is required and must look like 0.3.2 (without the leading v), got '$VERSION'" >&2; \
	exit 1; \
    }

WORKDIR /src
ENV CGO_ENABLED=0

# The module has no third-party dependencies yet, so there is no go.sum and no
# vendor/ directory and the build needs neither the network nor a module cache.
# When the first dependency lands, add `COPY go.sum ./` (or `COPY vendor ./vendor`
# plus `-mod=vendor` here) in the same commit — otherwise the image build would
# silently depend on the Go module proxy being reachable and unchanged.
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal

RUN GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w" -o /out/cloudferry ./cmd/cloudferry

# The runtime stays on the official rclone image: it brings CA certificates,
# tzdata and fuse3, and is pinned to the multi-arch manifest-list digest of
# rclone 1.76.0 (linux/amd64 + linux/arm64) so both Umbrel architectures work.
FROM rclone/rclone:1.76.0@sha256:7cbeec24a44f206067597c0368be4de90ae19a64d5113c7d1938bd737a81e466

ARG VERSION
# Commit the image was built from, so the published index can be traced back to
# its source. Empty is allowed for plain local builds.
ARG REVISION=

LABEL org.opencontainers.image.title="Cloud Ferry" \
      org.opencontainers.image.description="Download files from cloud storage to your Umbrel" \
      org.opencontainers.image.version="$VERSION" \
      org.opencontainers.image.revision="$REVISION" \
      org.opencontainers.image.licenses="MIT" \
      org.opencontainers.image.source="https://github.com/chikurov/cloud-ferry" \
      org.opencontainers.image.url="https://github.com/chikurov/cloud-ferry"

# Licence for the app plus the rclone MIT notice required by its licence; the
# runtime image is a redistribution, so both must ship with it.
COPY LICENSE THIRD_PARTY_NOTICES /usr/share/licenses/cloudferry/

COPY --from=build /out/cloudferry /usr/local/bin/cloudferry

EXPOSE 3000

# The probe is the app binary itself: no curl/wget in the image and basic auth is
# handled from CF_BASIC_* the same way the server reads it. Compose may override
# this, but the fallback keeps `docker run` usable.
HEALTHCHECK --interval=30s --timeout=5s --start-period=15s --retries=3 \
  CMD ["/usr/local/bin/cloudferry", "-healthcheck"]

ENTRYPOINT ["/usr/local/bin/cloudferry"]
