.PHONY: build test test-full install clean doc

BIN := bin/olympus

build:
	go build -o $(BIN) ./cmd/olympus

# Concurrency for the gate, bounded on purpose.
#
# Go defaults BOTH knobs to GOMAXPROCS, and they multiply: packages run
# concurrently AND tests run concurrently inside each. On a ten-core machine
# that is up to a hundred tests at once, and every one of these spawns a
# multiplexer server, a shell and a client. Measured unbounded: load average 27
# and four unrelated tests failing on twenty-second timeouts — starvation, not
# defects.
#
# 2x4 is eight at once. Measured on ten cores: 330s sequential, 126s here, 79s
# at 4x4 with load peaking at 20 — which is close enough to where the flakes
# started that the gate should not sit there. Raise it per-run instead:
#
#     make test TEST_CONCURRENCY="-p 4 -parallel 4"
#
# A gate that flakes is worth less than a gate that is slower, because a flake
# costs the run that hit it AND the trust in every run after.
TEST_CONCURRENCY ?= -p 2 -parallel 4

# The gate. Everything CI runs, runnable locally in one command.
#
# gofmt is checked rather than applied: a formatting fix belongs in the commit
# that caused it, not silently in whoever runs the gate next.
#
# -race is on because it is nearly free here and this code earns it: per-session
# locks, streaming goroutines, and a suite that now runs its own tests
# concurrently. Measured on ten cores — 1:43 with it against 1:53 without, since
# the suite waits on multiplexer subprocesses rather than on the CPU. A race
# that costs ten seconds a run to catch is not one worth catching in production.
#
# Two gates, one suite.
#
#   make test       the loop, in seconds — everything that does not need a
#                   multiplexer: payload shapes, resolution, matching, the
#                   error vocabulary, parsers.
#   make test-full  the gate before a commit — the above plus every case that
#                   drives a real terminal.
#
# This is a split, NOT a reduction: nothing is deleted and test-full still runs
# all of it. The reason to split is that the two answer different questions.
# "Did I just break the logic" should cost seconds, because a check that costs a
# minute and a half gets run less often — and a check not run is worth less than
# a slow one. The conformance suite in particular is the project's premise
# (CLAUDE.md: the obvious implementation of most rules is wrong), so it is never
# the thing that gets trimmed; it is the thing that moves to the commit gate.
test:
	@unformatted=$$(gofmt -l .); \
	  [ -z "$$unformatted" ] || { echo "gofmt needed: $$unformatted"; exit 1; }
	go vet ./...
	go test -short $(TEST_CONCURRENCY) ./...

test-full:
	@unformatted=$$(gofmt -l .); \
	  [ -z "$$unformatted" ] || { echo "gofmt needed: $$unformatted"; exit 1; }
	go vet ./...
	@# The OTHER supported platform, checked by compiling for it.
	@#
	@# Everything here is developed on one OS, and the parts that reach for a
	@# terminal or a signal are exactly the parts that differ between them —
	@# termios ioctls are spelled TIOCGETA on Darwin and TCGETS on Linux. A test
	@# that hardcodes one compiles cleanly for its author and breaks the whole
	@# package for everyone else, which is how the attach-restore test shipped
	@# building on Darwin and not on Linux.
	@#
	@# vet rather than test: it type-checks tests too, so it catches the class
	@# without needing that OS to run on.
	GOOS=linux GOARCH=amd64 go vet ./...
	GOOS=darwin GOARCH=arm64 go vet ./...
	go test -race $(TEST_CONCURRENCY) ./...

install:
	go install ./cmd/olympus

clean:
	rm -rf bin dist .gendoc coverage.out

# Manual pages and shell completions, generated from the command tree itself so
# they cannot describe a surface the binary no longer has.
doc:
	go run ./tools/gendoc .gendoc
