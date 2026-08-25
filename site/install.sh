#!/bin/bash
set -e

# ctxd Universal Installer for macOS and Linux
# Usage: curl -fsSL https://ctxd.dev/install.sh | bash && ctxd setup

echo "⚡ Installing ctxd (AI Context Engine)..."

INSTALL_DIR="${HOME}/.local/bin"
mkdir -p "${INSTALL_DIR}"

TARGET_BIN="${INSTALL_DIR}/ctxd"

if command -v go >/dev/null 2>&1; then
    echo "🔨 Compiling latest release with Go..."
    go install github.com/recscse/ctxd@latest || true
fi

if [[ ":$PATH:" != *":$INSTALL_DIR:"* ]]; then
    echo "export PATH=\"${INSTALL_DIR}:\$PATH\"" >> "${HOME}/.bashrc"
    echo "export PATH=\"${INSTALL_DIR}:\$PATH\"" >> "${HOME}/.zshrc" 2>/dev/null || true
    export PATH="${INSTALL_DIR}:$PATH"
fi

echo "✨ ctxd installed successfully!"
echo "🚀 Next step: Run 'ctxd setup' in your project directory."
