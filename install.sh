#!/usr/bin/env sh
set -eu

REPO="pink-blancmange/ahhhh"
VERSION="${AHHHH_VERSION:-latest}"

ARCH="$(uname -m)"
case "$ARCH" in
  x86_64|amd64) BIN_ARCH="amd64" ;;
  aarch64|arm64) BIN_ARCH="arm64" ;;
  *)
    echo "Unsupported architecture: $ARCH" >&2
    exit 1
    ;;
esac

if [ "$VERSION" = "latest" ]; then
  URL="https://github.com/${REPO}/releases/latest/download/ahhhh-linux-${BIN_ARCH}"
else
  URL="https://github.com/${REPO}/releases/download/${VERSION}/ahhhh-linux-${BIN_ARCH}"
fi

TMP_BIN="$(mktemp)"
trap 'rm -f "$TMP_BIN"' EXIT

echo "Downloading ahhhh (${BIN_ARCH})..."
curl -fsSL "$URL" -o "$TMP_BIN"
chmod +x "$TMP_BIN"

echo "Installing to /usr/local/bin/ahhhh (may ask for sudo password)..."
sudo install -m 0755 "$TMP_BIN" /usr/local/bin/ahhhh

mkdir -p "${HOME}/.local/share/applications"
cat > "${HOME}/.local/share/applications/ahhhh.desktop" <<'EOF_DESKTOP'
[Desktop Entry]
Type=Application
Name=Ahhhh
Comment=DIY AppImage Workbench
Exec=/usr/local/bin/ahhhh
Terminal=false
Categories=System;Utility;
EOF_DESKTOP

echo "Done! Launch 'Ahhhh' from your app menu or run 'ahhhh' in a terminal."
