GO       ?= go
BIN      := bin/kukel
FUZZTIME ?= 30s
VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo devel)
LDFLAGS  := -X main.version=$(VERSION)
# The build tags that leave everything optional out (make small).
SMALL    := nosqlite,nopostgres,nomysql,nozmq,no9p

.PHONY: all build small tk install test test-small test-tk test-all vet fmt fuzz bench golden dist docker vscode clean

all: vet test build

build:
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/kukel

# A kukel without the optional modules and databases (TAGS picks others:
# make small TAGS=nopostgres,nomysql).
TAGS ?= $(SMALL)
small:
	$(GO) build -tags $(TAGS) -ldflags "$(LDFLAGS)" -o bin/kukel-small ./cmd/kukel

# A kukel with the module tk: desktop windows (about 19 MB more).
tk:
	$(GO) build -tags tk -ldflags "$(LDFLAGS)" -o bin/kukel-tk ./cmd/kukel

# Install kukel in $(go env GOPATH)/bin (or $GOBIN).
install:
	$(GO) install -ldflags "$(LDFLAGS)" ./cmd/kukel

test:
	$(GO) test ./...

# The tests of the smallest build: what needs a left-out part skips.
test-small:
	$(GO) test -tags $(SMALL) ./...

# The tests of a kukel with tk. The tk tests need a display ($$DISPLAY) or
# Xvfb, else they skip; with xdotool they also click, type and drag.
test-tk:
	$(GO) test -tags tk ./...

# Everything: vet, the tests (with the programs in examples/) of the full,
# the smallest and the tk build, and the race detector. The db tests also
# use the servers that KUKEL_TEST_POSTGRES and KUKEL_TEST_MYSQL name
# (README).
test-all: vet test test-small test-tk
	$(GO) test -race ./...

vet:
	@unformatted="$$(gofmt -l .)"; if [ -n "$$unformatted" ]; then \
		echo "gofmt needed:"; echo "$$unformatted"; exit 1; fi
	$(GO) vet ./...
	$(GO) vet -tags $(SMALL) ./...
	$(GO) vet -tags tk ./...

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
	$(GO) test -run '^$$' -fuzz '^FuzzUTF8$$' -fuzztime $(FUZZTIME) ./internal/modules/utf8mod

bench:
	$(GO) test -run '^$$' -bench . -benchmem ./...

# Rewrite the golden files in tests/ after an intended output change.
golden:
	$(GO) test ./cmd/kukel -update

# The archives of a release, in dist/: kukel, and kukel-tk (built with the
# tag tk), for each of PLATFORMS (a .zip for Windows, else a .tar.gz, with
# README.md and LICENSE), and SHA256SUMS. The release workflow runs it for
# a tag: make dist VERSION=v1.2.3.
PLATFORMS ?= linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64
dist:
	rm -rf dist && mkdir -p dist
	@set -e; for p in $(PLATFORMS); do for kind in kukel kukel-tk; do \
		os=$${p%/*}; arch=$${p#*/}; name=$$kind-$(VERSION)-$$os-$$arch; exe=kukel; tags=; \
		[ $$os = windows ] && exe=kukel.exe; \
		[ $$kind = kukel-tk ] && tags=tk; \
		echo "$$name"; mkdir -p dist/$$name; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch $(GO) build -tags "$$tags" -trimpath -ldflags "-s -w $(LDFLAGS)" -o dist/$$name/$$exe ./cmd/kukel; \
		cp README.md LICENSE dist/$$name/; \
		if [ $$os = windows ]; then (cd dist && zip -qr $$name.zip $$name); \
		else tar -C dist -czf dist/$$name.tar.gz $$name; fi; \
		rm -rf dist/$$name; \
	done; done
	cd dist && sha256sum kukel-* > SHA256SUMS

# The Docker image for this machine's platform (the release workflow builds
# and pushes linux/amd64 and linux/arm64 to ghcr.io).
IMAGE ?= kukel:dev
docker:
	docker build --build-arg VERSION=$(VERSION) -t $(IMAGE) .

# The VS Code extension (editors/vscode: highlighting, debugging with kukel
# dap) as dist/kukel-vscode-<version>.vsix; needs Node.js (npx fetches vsce).
# Its version is kukel's: the last tag's numbers (v0.30.0 gives 0.30.0; VS
# Code takes only those), set in a copy in dist/vscode, with the LICENSE.
VSCE = @vscode/vsce@3.2.1
VSCODE_VERSION ?= $(shell git describe --tags --abbrev=0 2>/dev/null | sed -n 's/^v\([0-9]*\.[0-9]*\.[0-9]*\).*/\1/p')
vscode:
	@test -n "$(VSCODE_VERSION)" || { echo "make vscode: no version tag (set VSCODE_VERSION=x.y.z)"; exit 1; }
	rm -rf dist/vscode && mkdir -p dist/vscode
	cp -R editors/vscode/. LICENSE dist/vscode/
	(cd dist/vscode && node -e 'const fs = require("fs"), p = JSON.parse(fs.readFileSync("package.json")); p.version = process.argv[1]; fs.writeFileSync("package.json", JSON.stringify(p, null, 2) + "\n")' $(VSCODE_VERSION) && \
		npx --yes $(VSCE) package --no-dependencies -o ../kukel-vscode-$(VSCODE_VERSION).vsix); \
		status=$$?; rm -rf dist/vscode; exit $$status

clean:
	rm -rf bin dist
	$(GO) clean -testcache
