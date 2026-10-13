GO       ?= go
BIN      := bin/kukel
FUZZTIME ?= 30s
VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo devel)
LDFLAGS  := -X main.version=$(VERSION)

.PHONY: all build install test vet fmt fuzz bench golden clean

all: vet test build

build:
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/kukel

# Install kukel in $(go env GOPATH)/bin (or $GOBIN).
install:
	$(GO) install -ldflags "$(LDFLAGS)" ./cmd/kukel

test:
	$(GO) test ./...

vet:
	@unformatted="$$(gofmt -l .)"; if [ -n "$$unformatted" ]; then \
		echo "gofmt needed:"; echo "$$unformatted"; exit 1; fi
	$(GO) vet ./...

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
	$(GO) test -run '^$$' -fuzz '^FuzzParseFormat$$' -fuzztime $(FUZZTIME) ./internal/modules/timemod

bench:
	$(GO) test -run '^$$' -bench . -benchmem ./...

# Rewrite the golden files in tests/ after an intended output change.
golden:
	$(GO) test ./cmd/kukel -update

clean:
	rm -rf bin
	$(GO) clean -testcache
