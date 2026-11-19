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

ICON_URL="https://raw.githubusercontent.com/${REPO}/main/packaging/ahhhh.png"
ICON_DIR="${HOME}/.local/share/icons/hicolor/256x256/apps"
ICON_PATH="${ICON_DIR}/ahhhh.png"

TMP_BIN="$(mktemp)"
TMP_ICON="$(mktemp)"
trap 'rm -f "$TMP_BIN" "$TMP_ICON"' EXIT

echo "Downloading ahhhh (${BIN_ARCH})..."
curl -fsSL "$URL" -o "$TMP_BIN"
chmod +x "$TMP_BIN"

echo "Installing to /usr/local/bin/ahhhh (may ask for sudo password)..."
sudo install -m 0755 "$TMP_BIN" /usr/local/bin/ahhhh

mkdir -p "$ICON_DIR"
if curl -fsSL "$ICON_URL" -o "$TMP_ICON" 2>/dev/null && [ -s "$TMP_ICON" ]; then
  install -m 0644 "$TMP_ICON" "$ICON_PATH"
elif [ -f "$(pwd)/packaging/ahhhh.png" ]; then
  install -m 0644 "$(pwd)/packaging/ahhhh.png" "$ICON_PATH"
fi

cp -n /usr/share/icons/hicolor/index.theme "${HOME}/.local/share/icons/hicolor/" 2>/dev/null || true

mkdir -p "${HOME}/.local/share/applications"
cat > "${HOME}/.local/share/applications/ahhhh.desktop" <<'EOF_DESKTOP'
[Desktop Entry]
Type=Application
Name=Ahhhh
Comment=DIY AppImage Workbench
Exec=/usr/local/bin/ahhhh
Icon=ahhhh
Terminal=false
Categories=Utility;
EOF_DESKTOP

gtk-update-icon-cache -f -t "${HOME}/.local/share/icons/hicolor" >/dev/null 2>&1 || true
update-desktop-database "${HOME}/.local/share/applications" >/dev/null 2>&1 || true

echo "Done! Launch 'Ahhhh' from your app menu or run 'ahhhh' in a terminal."
