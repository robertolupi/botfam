#!/usr/bin/env bash
#
# tools/install.sh — compile and install botfam into ~/bin.
#
# botfam now serves the forge (gitea-mcp) tools in-process as forge_* subtools
# (#429), so the standalone gitea-mcp-server binary is no longer built or used.
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
version="dev"
if git -C "$REPO_ROOT" rev-parse --git-dir >/dev/null 2>&1; then
  sha=$(git -C "$REPO_ROOT" rev-parse --short HEAD 2>/dev/null || echo "unknown")
  date=$(git -C "$REPO_ROOT" log -1 --format=%cs 2>/dev/null || echo "unknown")
  if [ -n "$(git -C "$REPO_ROOT" status --porcelain 2>/dev/null)" ]; then
    sha="${sha}-dirty"
  fi
  version="0.1.0 ($sha, $date)"
fi
go build -v -ldflags "-X 'github.com/robertolupi/botfam/internal/version.BuildSHA=$version'" -o "$BIN_DIR/botfam" "$REPO_ROOT/cmd/botfam"

# 4. Codesign on macOS (Darwin)
if [ "$(uname)" = "Darwin" ]; then
  echo "Signing binary for macOS..."
  codesign --force --sign - "$BIN_DIR/botfam"
fi

echo ""
echo "Success! Binary installed at:"
echo "  - $BIN_DIR/botfam"
echo "Make sure $BIN_DIR is in your PATH."
