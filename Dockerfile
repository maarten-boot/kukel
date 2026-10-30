# The kukel image: the static kukel binary on distroless (CA certificates
# and time zones, a non-root user, no shell). The release workflow builds
# it for linux/amd64 and linux/arm64 and pushes it to ghcr.io; locally:
#
#   make docker                      # kukel:dev for this machine
#   docker run --rm -v "$PWD:/work" kukel:dev run prog.kkl
#
# The base images are pinned to digests (the tag in front says which).

# Build on the machine's own platform and cross-compile: no emulation
# needed for the other one.
FROM --platform=$BUILDPLATFORM golang:1.27-trixie@sha256:fbb7ae40b62ff8afe31662df570d39b4f5052ac32b90dae8e60d7657a20a74f4 AS build
ARG TARGETOS TARGETARCH
ARG VERSION=devel
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -buildvcs=false -ldflags "-s -w -X main.version=$VERSION" -o /out/kukel ./cmd/kukel

FROM gcr.io/distroless/static-debian13:nonroot@sha256:18f4cc26c9ffa7f20a359298be7deadac5c607c8e03a02b8119f1a5fc2f82c99
COPY --from=build /out/kukel /usr/local/bin/kukel
# Programs and their files come in through a volume: docker run -v "$PWD:/work".
WORKDIR /work
ENTRYPOINT ["/usr/local/bin/kukel"]
CMD ["help"]

LABEL org.opencontainers.image.title="kukel" \
      org.opencontainers.image.description="The Kukel programming language: compiler, VM, REPL and debugger" \
      org.opencontainers.image.source="https://github.com/maarten-boot/kukel" \
      org.opencontainers.image.licenses="MIT"
