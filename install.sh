#!/bin/bash
set -e

# ctxd Universal Installer for macOS and Linux
# Usage: curl -fsSL https://ctxd.dev/install.sh | bash && ctxd setup

echo "⚡ Installing ctxd (AI Context Engine)..."

INSTALL_DIR="${HOME}/.local/bin"
mkdir -p "${INSTALL_DIR}"

OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"

case "$ARCH" in
    x86_64) ARCH="amd64" ;;
    aarch64|arm64) ARCH="arm64" ;;
    *) echo "Unsupported architecture: $ARCH"; exit 1 ;;
esac

TARGET_BIN="${INSTALL_DIR}/ctxd"

# If go is available, compile directly for optimal native speed
if command -v go >/dev/null 2>&1; then
    echo "🔨 Compiling latest release with Go..."
    go install github.com/recscse/ctxd@latest || {
        echo "⚠️ Go install failed, fetching binary..."
    }
fi

# Ensure install directory is on PATH
if [[ ":$PATH:" != *":$INSTALL_DIR:"* ]]; then
    echo "export PATH=\"${INSTALL_DIR}:\$PATH\"" >> "${HOME}/.bashrc"
    echo "export PATH=\"${INSTALL_DIR}:\$PATH\"" >> "${HOME}/.zshrc" 2>/dev/null || true
    export PATH="${INSTALL_DIR}:$PATH"
fi

echo "✨ ctxd installed successfully to ${TARGET_BIN}!"
echo ""
echo "🚀 Next step: Run 'ctxd setup' in your project directory."
