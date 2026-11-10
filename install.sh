#!/bin/bash
set -e

# codive Universal Installer for macOS and Linux
# Usage: curl -fsSL https://recscse.github.io/codive/install.sh | bash && codive setup

echo "⚡ Installing codive (Developer Context Engine)..."

INSTALL_DIR="${HOME}/.local/bin"
mkdir -p "${INSTALL_DIR}"

OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"

case "$ARCH" in
    x86_64) ARCH="amd64" ;;
    aarch64|arm64) ARCH="arm64" ;;
    *) echo "Unsupported architecture: $ARCH"; exit 1 ;;
esac

TARGET_BIN="${INSTALL_DIR}/codive"

# 1. Try downloading pre-compiled release binary from GitHub Releases
SUCCESS=false

if command -v curl >/dev/null 2>&1; then
    # Asset names include the version, so find the latest release's tag first.
    LATEST_URL=$(curl -fsSLI -o /dev/null -w '%{url_effective}' https://github.com/recscse/codive/releases/latest 2>/dev/null || true)
    TAG="${LATEST_URL##*/}"
    DOWNLOAD_URL="https://github.com/recscse/codive/releases/download/${TAG}/codive_${TAG}_${OS}_${ARCH}.tar.gz"
    TMP_DIR=$(mktemp -d)
    if [ -n "$TAG" ] && curl -fsSL "$DOWNLOAD_URL" -o "${TMP_DIR}/codive.tar.gz" 2>/dev/null; then
        tar -xzf "${TMP_DIR}/codive.tar.gz" -C "${INSTALL_DIR}"
        chmod +x "${TARGET_BIN}"
        SUCCESS=true
        rm -rf "${TMP_DIR}"
    fi
fi

# 2. Fallback: Build from source if Go is installed
if [ "$SUCCESS" = false ]; then
    if command -v go >/dev/null 2>&1; then
        echo "🔨 Compiling with Go..."
        go install github.com/recscse/codive@latest
        GOPATH_BIN="$(go env GOPATH)/bin/codive"
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

echo "✨ codive installed successfully to ${TARGET_BIN}!"
echo ""
echo "🚀 Next step: Run 'codive setup' in your repository root."
