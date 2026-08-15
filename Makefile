.PHONY: build test install clean

BIN := bin/olympus

build:
	go build -o $(BIN) ./cmd/olympus

# The gate. Everything CI runs, runnable locally in one command.
#
# gofmt is checked rather than applied: a formatting fix belongs in the commit
# that caused it, not silently in whoever runs the gate next.
test:
	@unformatted=$$(gofmt -l .); \
	  [ -z "$$unformatted" ] || { echo "gofmt needed: $$unformatted"; exit 1; }
	go vet ./...
	go test ./...

install:
	go install ./cmd/olympus

clean:
	rm -rf bin dist coverage.out
