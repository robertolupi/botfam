#!/usr/bin/env bash
#
# tools/install.sh — compile and install botfam and gitea-mcp-server into ~/bin.
#
set -euo pipefail

# Locate the repository root
REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BIN_DIR="$HOME/bin"

echo "=== botfam Installer ==="
echo "Repository root: $REPO_ROOT"
echo "Installation directory: $BIN_DIR"
echo ""

# 1. Ensure bin directory exists
mkdir -p "$BIN_DIR"

# 2. Update/initialize git submodules
echo "Updating submodules..."
git -C "$REPO_ROOT" submodule update --init --recursive

# 3. Build botfam
echo "Building botfam..."
go build -v -o "$BIN_DIR/botfam" "$REPO_ROOT/cmd/botfam"

# 4. Build gitea-mcp-server
echo "Building gitea-mcp-server..."
version=$(git -C "$REPO_ROOT/third_party/gitea-mcp" describe --tags --always 2>/dev/null | sed 's/-/+/' | sed 's/^v//' || echo "unknown")
(cd "$REPO_ROOT/third_party/gitea-mcp" && go build -v -ldflags "-s -w -X main.Version=$version" -o "$BIN_DIR/gitea-mcp-server")

# 5. Codesign on macOS (Darwin)
if [ "$(uname)" = "Darwin" ]; then
  echo "Signing binaries for macOS..."
  codesign --force --sign - "$BIN_DIR/botfam"
  codesign --force --sign - "$BIN_DIR/gitea-mcp-server"
fi

echo ""
echo "Success! Binaries installed at:"
echo "  - $BIN_DIR/botfam"
echo "  - $BIN_DIR/gitea-mcp-server"
echo "Make sure $BIN_DIR is in your PATH."
