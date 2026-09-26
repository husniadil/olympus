#!/bin/bash
# Installs every multiplexer backend so `make test-full` runs each leg in a
# Claude Code on the web session instead of skipping it. Versions match the
# main leg of .github/workflows/ci.yml; bump them together.
set -euo pipefail

if [ "${CLAUDE_CODE_REMOTE:-}" != "true" ]; then
  exit 0
fi

ZMX_VERSION=0.7.0
MEJA_VERSION=0.0.26
HERDR_VERSION=0.9.1

bin="$HOME/.local/bin"
mkdir -p "$bin"
case "$(uname -m)" in
  arm64 | aarch64) arch=aarch64 ;;
  x86_64) arch=x86_64 ;;
  *) echo "unsupported architecture $(uname -m)" >&2; exit 1 ;;
esac

install_tmux() {
  command -v tmux >/dev/null 2>&1 && return 0
  apt-get update -qq && apt-get install -y -qq tmux
}

install_zmx() {
  "$bin/zmx" version 2>/dev/null | grep -q "$ZMX_VERSION" && return 0
  local name="zmx-${ZMX_VERSION}-linux-${arch}.tar.gz"
  local base="https://github.com/neurosnap/zmx/releases/download/v${ZMX_VERSION}"
  local tmp
  tmp=$(mktemp -d)
  curl -fsSL -o "$tmp/$name" "$base/$name"
  # The published .sha256 names the file by the build machine's cache path,
  # so compare the digest field rather than running sha256sum -c.
  local expected actual
  expected=$(curl -fsSL "$base/$name.sha256" | awk '{print $1}')
  actual=$(sha256sum "$tmp/$name" | awk '{print $1}')
  [ "$expected" = "$actual" ] || { echo "zmx sha256 mismatch: want $expected, got $actual" >&2; return 1; }
  tar xzf "$tmp/$name" -C "$bin" zmx
  chmod +x "$bin/zmx"
  rm -rf "$tmp"
}

install_meja() {
  "$bin/meja" version 2>/dev/null | grep -q "$MEJA_VERSION" && return 0
  GOBIN="$bin" go install "github.com/garindra/meja@v${MEJA_VERSION}"
}

install_herdr() {
  "$bin/herdr" --version 2>/dev/null | grep -q "$HERDR_VERSION" && return 0
  # herdr publishes no checksum; the version it reports is the only check.
  curl -fsSL -o "$bin/herdr.tmp" \
    "https://github.com/herdrdev/herdr/releases/download/v${HERDR_VERSION}/herdr-linux-${arch}"
  chmod +x "$bin/herdr.tmp"
  "$bin/herdr.tmp" --version | grep -q "$HERDR_VERSION"
  mv "$bin/herdr.tmp" "$bin/herdr"
}

# Each backend is attempted even when another fails, and any failure fails the
# hook: a missing backend makes its tests skip, which must not pass unnoticed.
failed=()
for backend in tmux zmx meja herdr; do
  if ! "install_$backend"; then
    echo "installing $backend failed; its tests will skip" >&2
    failed+=("$backend")
  fi
done

(cd "$CLAUDE_PROJECT_DIR" && go mod download)

echo "export PATH=\"$bin:\$PATH\"" >> "$CLAUDE_ENV_FILE"

if [ ${#failed[@]} -gt 0 ]; then
  echo "backends not installed: ${failed[*]}" >&2
  exit 1
fi
