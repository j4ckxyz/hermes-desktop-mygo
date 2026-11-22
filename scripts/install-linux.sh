#!/bin/sh
# Install Hermes Desktop for the current user: the binary, a menu entry
# and the icon, under ~/.local (or $PREFIX). No root needed.
#
#   scripts/install-linux.sh [path/to/hermes-desktop] [path/to/icon.png]
#   scripts/install-linux.sh --uninstall
set -eu
PREFIX=${PREFIX:-$HOME/.local}
APP=hermes-desktop
if [ "${1:-}" = "--uninstall" ]; then
  rm -f "$PREFIX/bin/$APP" "$PREFIX/share/applications/$APP.desktop" "$PREFIX/share/icons/hicolor/512x512/apps/$APP.png"
  echo "Removed Hermes Desktop from $PREFIX (your settings and cache stay in ~/.config and ~/.cache)."
  exit 0
fi
BIN=${1:-bin/$APP}
ICON=${2:-assets/icon.png}
[ -x "$BIN" ] || { echo "No binary at $BIN: run 'make build' first or pass its path." >&2; exit 1; }
install -Dm755 "$BIN" "$PREFIX/bin/$APP"
install -Dm644 "$ICON" "$PREFIX/share/icons/hicolor/512x512/apps/$APP.png"
mkdir -p "$PREFIX/share/applications"
cat > "$PREFIX/share/applications/$APP.desktop" <<DESKTOP
[Desktop Entry]
Type=Application
Name=Hermes
GenericName=Hermes Agent client
Comment=Chat with your Hermes agents
Exec=$PREFIX/bin/$APP
Icon=$APP
Terminal=false
Categories=Network;Chat;Utility;
Keywords=AI;agent;chat;hermes;
StartupWMClass=$APP
DESKTOP
command -v update-desktop-database >/dev/null && update-desktop-database -q "$PREFIX/share/applications" || true
command -v gtk-update-icon-cache >/dev/null && gtk-update-icon-cache -q -t "$PREFIX/share/icons/hicolor" 2>/dev/null || true
echo "Installed Hermes Desktop to $PREFIX/bin/$APP. Open it from your app menu as \"Hermes\"."
