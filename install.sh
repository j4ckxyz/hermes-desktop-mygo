#!/bin/sh
# Installs Hermes Desktop for the current user, without root or sudo:
#
#   macOS  the latest release in /Applications, or ~/Applications when you
#          cannot write there
#   Linux  the app in ~/.local/hermes-desktop.app, the hermes-desktop
#          command in ~/.local/bin, and its entry in the applications menu
#
# Either way it is where the app can update itself.
#
#   curl -fsSL https://github.com/j4ckxyz/hermes-desktop-mygo/releases/latest/download/install.sh | sh
#   sh install.sh               # the same, from a checkout
#   sh install.sh --uninstall   # removes the app, keeps your settings
#
# It runs the install.sh of the latest release, which `mygo build` made:
# that script checks the size and Ed25519 signature of what it downloads
# against the app's update key before installing it.
set -eu

repo=j4ckxyz/hermes-desktop-mygo
base=${HERMES_DESKTOP_DOWNLOAD_URL:-https://github.com/$repo/releases/latest/download}
base=${base%/}

case "$(uname -s)" in
  Darwin | Linux) ;;
  *) echo "install.sh: this installs on macOS and Linux; on Windows run install.ps1" >&2; exit 1 ;;
esac

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT INT TERM
if command -v curl > /dev/null 2>&1; then
  curl -fsSL "$base/install.sh" -o "$work/install.sh"
elif command -v wget > /dev/null 2>&1; then
  wget -qO "$work/install.sh" "$base/install.sh"
else
  echo "install.sh: needs curl or wget" >&2
  exit 1
fi
sh "$work/install.sh" "$@"
