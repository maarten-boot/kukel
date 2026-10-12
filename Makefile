GO       ?= go
BIN      := bin/kukel
FUZZTIME ?= 30s

.PHONY: all build test vet fmt fuzz bench golden clean

all: vet test build

build:
	$(GO) build -o $(BIN) ./cmd/kukel

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

bench:
	$(GO) test -run '^$$' -bench . -benchmem ./...

# Rewrite the golden files in tests/ after an intended output change.
golden:
	$(GO) test ./cmd/kukel -update

clean:
	rm -rf bin
	$(GO) clean -testcache
