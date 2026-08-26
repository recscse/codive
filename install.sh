#!/bin/bash
set -e

# devctx Universal Installer for macOS and Linux
# Usage: curl -fsSL https://recscse.github.io/devctx/install.sh | bash && devctx setup

echo "⚡ Installing devctx (Developer Context Engine)..."

INSTALL_DIR="${HOME}/.local/bin"
mkdir -p "${INSTALL_DIR}"

OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"

case "$ARCH" in
    x86_64) ARCH="amd64" ;;
    aarch64|arm64) ARCH="arm64" ;;
    *) echo "Unsupported architecture: $ARCH"; exit 1 ;;
esac

TARGET_BIN="${INSTALL_DIR}/devctx"

# 1. Try downloading pre-compiled release binary from GitHub Releases
DOWNLOAD_URL="https://github.com/recscse/devctx/releases/latest/download/devctx_v1.0.0_${OS}_${ARCH}.tar.gz"
SUCCESS=false

if command -v curl >/dev/null 2>&1; then
    TMP_DIR=$(mktemp -d)
    if curl -fsSL "$DOWNLOAD_URL" -o "${TMP_DIR}/devctx.tar.gz" 2>/dev/null; then
        tar -xzf "${TMP_DIR}/devctx.tar.gz" -C "${INSTALL_DIR}"
        chmod +x "${TARGET_BIN}"
        SUCCESS=true
        rm -rf "${TMP_DIR}"
    fi
fi

# 2. Fallback: Build from source if Go is installed
if [ "$SUCCESS" = false ]; then
    if command -v go >/dev/null 2>&1; then
        echo "🔨 Compiling with Go..."
        go install github.com/recscse/devctx@latest
        GOPATH_BIN="$(go env GOPATH)/bin/devctx"
        if [ -f "$GOPATH_BIN" ]; then
            cp "$GOPATH_BIN" "$TARGET_BIN"
            chmod +x "$TARGET_BIN"
            SUCCESS=true
        fi
    fi
fi

# 3. Ensure install directory is on PATH
if [[ ":$PATH:" != *":$INSTALL_DIR:"* ]]; then
    echo "export PATH=\"${INSTALL_DIR}:\$PATH\"" >> "${HOME}/.bashrc"
    echo "export PATH=\"${INSTALL_DIR}:\$PATH\"" >> "${HOME}/.zshrc" 2>/dev/null || true
    export PATH="${INSTALL_DIR}:$PATH"
fi

echo "✨ devctx installed successfully to ${TARGET_BIN}!"
echo ""
echo "🚀 Next step: Run 'devctx setup' in your repository root."
