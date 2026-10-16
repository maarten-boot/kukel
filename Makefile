GO       ?= go
BIN      := bin/kukel
FUZZTIME ?= 30s
VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo devel)
LDFLAGS  := -X main.version=$(VERSION)
# The build tags that leave everything optional out (make small).
SMALL    := nosqlite,nopostgres,nomysql,nozmq,no9p

.PHONY: all build small install test test-small test-all vet fmt fuzz bench golden dist docker clean

all: vet test build

build:
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/kukel

# A kukel without the optional modules and databases (TAGS picks others:
# make small TAGS=nopostgres,nomysql).
TAGS ?= $(SMALL)
small:
	$(GO) build -tags $(TAGS) -ldflags "$(LDFLAGS)" -o bin/kukel-small ./cmd/kukel

# Install kukel in $(go env GOPATH)/bin (or $GOBIN).
install:
	$(GO) install -ldflags "$(LDFLAGS)" ./cmd/kukel

test:
	$(GO) test ./...

# The tests of the smallest build: what needs a left-out part skips.
test-small:
	$(GO) test -tags $(SMALL) ./...

# Everything: vet, the tests (with the programs in examples/) of the full
# and the smallest build, and the race detector. The db tests also use the
# servers that KUKEL_TEST_POSTGRES and KUKEL_TEST_MYSQL name (README).
test-all: vet test test-small
	$(GO) test -race ./...

vet:
	@unformatted="$$(gofmt -l .)"; if [ -n "$$unformatted" ]; then \
		echo "gofmt needed:"; echo "$$unformatted"; exit 1; fi
	$(GO) vet ./...
	$(GO) vet -tags $(SMALL) ./...

fmt:
	gofmt -w .

# Run each fuzz test for FUZZTIME (make fuzz FUZZTIME=5m).
fuzz:
	$(GO) test -run '^$$' -fuzz '^FuzzLexer$$' -fuzztime $(FUZZTIME) ./internal/lexer
	$(GO) test -run '^$$' -fuzz '^FuzzParser$$' -fuzztime $(FUZZTIME) ./internal/parser
	$(GO) test -run '^$$' -fuzz '^FuzzCompile$$' -fuzztime $(FUZZTIME) ./internal/compiler
	$(GO) test -run '^$$' -fuzz '^FuzzDecode$$' -fuzztime $(FUZZTIME) ./internal/bytecode
	$(GO) test -run '^$$' -fuzz '^FuzzVerifyCode$$' -fuzztime $(FUZZTIME) ./internal/bytecode
	$(GO) test -run '^$$' -fuzz '^FuzzJSON$$' -fuzztime $(FUZZTIME) ./internal/modules/jsonmod
	$(GO) test -run '^$$' -fuzz '^FuzzFormat$$' -fuzztime $(FUZZTIME) ./internal/format
	$(GO) test -run '^$$' -fuzz '^FuzzParseFormat$$' -fuzztime $(FUZZTIME) ./internal/modules/timemod

bench:
	$(GO) test -run '^$$' -bench . -benchmem ./...

# Rewrite the golden files in tests/ after an intended output change.
golden:
	$(GO) test ./cmd/kukel -update

# The archives of a release, in dist/: kukel for each of PLATFORMS (a .zip
# for Windows, else a .tar.gz, with README.md and LICENSE), and SHA256SUMS.
# The release workflow runs it for a tag: make dist VERSION=v1.2.3.
PLATFORMS ?= linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64
dist:
	rm -rf dist && mkdir -p dist
	@set -e; for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; name=kukel-$(VERSION)-$$os-$$arch; exe=kukel; \
		[ $$os = windows ] && exe=kukel.exe; \
		echo "$$name"; mkdir -p dist/$$name; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch $(GO) build -trimpath -ldflags "-s -w $(LDFLAGS)" -o dist/$$name/$$exe ./cmd/kukel; \
		cp README.md LICENSE dist/$$name/; \
		if [ $$os = windows ]; then (cd dist && zip -qr $$name.zip $$name); \
		else tar -C dist -czf dist/$$name.tar.gz $$name; fi; \
		rm -rf dist/$$name; \
	done
	cd dist && sha256sum kukel-* > SHA256SUMS

# The Docker image for this machine's platform (the release workflow builds
# and pushes linux/amd64 and linux/arm64 to ghcr.io).
IMAGE ?= kukel:dev
docker:
	docker build --build-arg VERSION=$(VERSION) -t $(IMAGE) .

clean:
	rm -rf bin dist
	$(GO) clean -testcache
