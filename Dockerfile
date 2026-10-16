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
FROM --platform=$BUILDPLATFORM golang:1.27-trixie@sha256:47667934874913a449b96faa7ef4777fa285d9ec29126ee80ed61c45b7cccf0e AS build
ARG TARGETOS TARGETARCH
ARG VERSION=devel
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -buildvcs=false -ldflags "-s -w -X main.version=$VERSION" -o /out/kukel ./cmd/kukel

FROM gcr.io/distroless/static-debian13:nonroot@sha256:e2e927ec666bae08560abb3c55d0659eceabb657f56b6782ab500a9fc7f555e3
COPY --from=build /out/kukel /usr/local/bin/kukel
# Programs and their files come in through a volume: docker run -v "$PWD:/work".
WORKDIR /work
ENTRYPOINT ["/usr/local/bin/kukel"]
CMD ["help"]

LABEL org.opencontainers.image.title="kukel" \
      org.opencontainers.image.description="The Kukel programming language: compiler, VM, REPL and debugger" \
      org.opencontainers.image.source="https://github.com/maarten-boot/kukel" \
      org.opencontainers.image.licenses="MIT"
