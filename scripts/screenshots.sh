#!/bin/sh
# Screenshots of each screen of the demo build, for comparison with
# design/ref/*.png. Runs the real app on a private Xvfb (no display
# needed). Usage: scripts/screenshots.sh [outdir] [screen...]
set -eu
cd "$(dirname "$0")/.."
OUT=${1:-screenshots/linux}; shift 2>/dev/null || true
SCREENS=${*:-"main connect settings new"}
DISPLAY_NUM=${XVFB_DISPLAY:-:97}
mkdir -p "$OUT"
export PATH="$HOME/.local/opt/go/bin:$PATH"
go build -tags demo -o bin/hermes-desktop-demo ./cmd/hermes-desktop

XVFB_PID=""
if ! [ -e "/tmp/.X11-unix/X${DISPLAY_NUM#:}" ]; then
  if command -v Xvfb >/dev/null; then
    Xvfb "$DISPLAY_NUM" -screen 0 1440x900x24 -nolisten tcp +extension GLX +render -noreset >/dev/null 2>&1 &
  else
    "$HOME/.local/opt/xvfb/start.sh" "$DISPLAY_NUM" 1440x900x24 >/dev/null 2>&1 &
  fi
  XVFB_PID=$!
  for _ in $(seq 50); do [ -e "/tmp/.X11-unix/X${DISPLAY_NUM#:}" ] && break; sleep 0.1; done
fi
trap '[ -n "$XVFB_PID" ] && kill $XVFB_PID 2>/dev/null || true' EXIT

for screen in $SCREENS; do
  HOME_DIR=$(mktemp -d)
  DISPLAY=$DISPLAY_NUM HERMES_DESKTOP_HOME=$HOME_DIR HERMES_DESKTOP_BENCH=screenshot HERMES_DEMO_SCREEN=$screen \
    GTK_THEME=Adwaita:dark NO_AT_BRIDGE=1 ./bin/hermes-desktop-demo >"$OUT/$screen.log" 2>&1 &
  APP=$!
  sleep "${SHOT_DELAY:-4}"
  DISPLAY=$DISPLAY_NUM import -window root "$OUT/$screen.png"
  kill $APP 2>/dev/null || true
  wait $APP 2>/dev/null || true
  rm -rf "$HOME_DIR"
  echo "$OUT/$screen.png"
done
