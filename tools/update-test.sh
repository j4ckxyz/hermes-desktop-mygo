#!/usr/bin/env bash
# Checks that Hermes Desktop updates itself, on the machine this runs on:
# builds version 0.0.1, installs it as a user would, publishes 0.0.2 as an
# update on this machine, and has the installed app replace itself with it.
#
# Then as a user meets it: 0.0.3 is published, the installed app opens on
# an instance with a chat open and unsent text in it, offers the new
# version on its own page, is told to update, and must come back by
# itself as 0.0.3, on the same instance and chat, with the text. That part
# needs a display, as every desktop has (xvfb-run on a server).
#
#   tools/update-test.sh
#
# The build tool only takes https addresses for updates, and the app takes
# http from this machine alone: so the builds name https://127.0.0.1, and
# the test changes that to http in the app it installs and in the manifest
# it publishes. Everything else is as released: the check, the download,
# its signature, the replacing.
#
# It signs with a key made for the test, changes mygo.json for the builds
# and puts it back, and keeps the app's data in a temporary directory. On
# Linux and Windows it installs the test's app as their installers do
# (under its own name, apart from an installed Hermes) and removes it.
set -euo pipefail
cd "$(dirname "$0")/.."

port=${PORT:-8765}
python=python3
command -v python3 > /dev/null 2>&1 || python=python
os=$(go env GOOS)
arch=$(go env GOARCH)
work=$(mktemp -d)
out=build-update-test
# In the project, so that its path reads the same to every program on
# Windows.
notes=build-update-test-changes.md
server=""

cp mygo.json "$work/mygo.json"
cleanup() {
  cp "$work/mygo.json" mygo.json
  [ -n "$server" ] && kill "$server" 2>/dev/null || true
  rm -rf "$out" "$notes"
}
trap cleanup EXIT

# native turns a path into the app's own form (C:\... on Windows).
native() {
  if [ "$os" = windows ]; then cygpath -w "$1"; else printf '%s' "$1"; fi
}

go tool mygo keygen -o "$work/keys" > /dev/null
MYGO_UPDATER_PRIVATE_KEY=$(cat "$work/keys/mygo-update.key")
export MYGO_UPDATER_PRIVATE_KEY
printf '# Changes\n\n## 0.0.3\n\n- The update the app offers as it opens.\n- Its second point.\n\n## 0.0.2\n\nThe update of the test.\n\n## 0.0.1\n\nThe first version of the test.\n' > "$notes"

# A name of its own, so that the test's app is apart from an installed
# Hermes (its files, its single-instance lock).
build() {
  cat > mygo.json <<JSON
{
  "name": "HermesUpdateTest",
  "slug": "hermes-update-test",
  "identifier": "io.github.j4ckxyz.hermesdesktop.updatetest",
  "version": "$1",
  "main": "./cmd/hermes-desktop",
  "out": "$out",
  "linux": { "command": "hermes-update-test", "webview": false, "formats": [] },
  "updates": {
    "publicKey": "$(cat "$work/keys/mygo-update.pub")",
    "url": "https://127.0.0.1:$port",
    "changelog": "$notes",
    "deltas": 0
  }
}
JSON
  go tool mygo build -platform "$os/$arch"
}

echo "== Building 0.0.1"
build 0.0.1
dir="$out/$os-$arch"
ls -la "$dir"

echo "== Installing 0.0.1"
case "$os" in
  darwin)
    mkdir -p "$work/Applications"
    cp -R "$dir/HermesUpdateTest.app" "$work/Applications/"
    app="$work/Applications/HermesUpdateTest.app/Contents/MacOS/HermesUpdateTest"
    ;;
  linux)
    # As install.sh installs it: in ~/.local, where the app can write.
    sh "$dir/install.sh" "$dir"/*-0.0.1-linux-"$arch".tar.gz
    app="$HOME/.local/hermes-update-test.app/hermes-update-test"
    [ -f "$app" ] || app=$(find "$HOME/.local/hermes-update-test.app" -maxdepth 2 -type f -perm -u+x -name 'hermes-update-test*' | head -1)
    ;;
  windows)
    installer=$(ls "$dir"/*-setup-*.exe 2>/dev/null | head -1 || true)
    if [ -n "$installer" ]; then
      # The installer, silently: per user, in %LOCALAPPDATA%\Programs.
      "$installer" //S
      app="$LOCALAPPDATA/Programs/HermesUpdateTest/HermesUpdateTest.exe"
      for _ in $(seq 1 20); do [ -f "$app" ] && break; sleep 1; done
    else
      mkdir -p "$work/install"
      cp -R "$dir"/. "$work/install/"
      app="$work/install/HermesUpdateTest.exe"
    fi
    ;;
esac
[ -f "$app" ] || { echo "the app was not installed: $app"; exit 1; }
echo "installed at $app"

# The address of updates, from https to http: the same length, so that
# the program stays whole.
plain_http() {
  "$python" - "$app" "$port" <<'PY'
import sys
path, port = sys.argv[1], sys.argv[2]
data = open(path, "rb").read()
old = ("https://127.0.0.1:%s/update-" % port).encode()
new = ("http://127.0.0.1:%s//update-" % port).encode()
assert len(old) == len(new) and data.count(old) >= 1, "the app does not name the test's address"
open(path, "wb").write(data.replace(old, new))
PY
  if [ "$os" = darwin ]; then
    # A program changed must be signed again to run.
    codesign --force --sign - "${app%/Contents/MacOS/*}"
  fi
}
plain_http

# publish serves the build in $dir as the update, its manifest naming http.
publish() {
  [ -n "$server" ] && kill "$server" 2>/dev/null || true
  "$python" - "$dir" "$port" <<'PY'
import glob, sys
for path in glob.glob(sys.argv[1] + "/update-*.json"):
    text = open(path).read()
    open(path, "w").write(text.replace("https://127.0.0.1:%s/" % sys.argv[2], "http://127.0.0.1:%s/" % sys.argv[2]))
PY
  "$python" -m http.server "$port" --bind 127.0.0.1 --directory "$dir" > "$work/server.log" 2>&1 &
  server=$!
  feed="http://127.0.0.1:$port/update-$os-$arch.json"
  for _ in $(seq 1 15); do
    curl -fsS -m 5 "$feed" > /dev/null 2>&1 && break
    sleep 1
  done
  curl -fsS -m 5 "$feed" || { echo "the update is not served at $feed"; cat "$work/server.log"; exit 1; }
  echo
}

"$app" --write-version "$(native "$work/before.txt")"
echo "before: $(cat "$work/before.txt")"
grep -qx "0.0.1" "$work/before.txt"

echo "== Building 0.0.2, and publishing it on this machine"
rm -rf "$out"
build 0.0.2
publish

echo "== Updating"
"$app" --self-update "$(native "$work/update.txt")" || true
cat "$work/update.txt"
if ! grep -q "^updated: 0.0.1 -> 0.0.2" "$work/update.txt"; then
  echo "-- what the server saw:"
  cat "$work/server.log"
  exit 1
fi

# The file at the same place is now the new version.
"$app" --write-version "$(native "$work/after.txt")"
echo "after: $(cat "$work/after.txt")"
grep -qx "0.0.2" "$work/after.txt"

echo "== Hermes updated itself from 0.0.1 to 0.0.2 on $os/$arch"

echo "== Building 0.0.3, and publishing it"
plain_http # the 0.0.2 installed now names https again
rm -rf "$out"
build 0.0.3
publish

echo "== Opening 0.0.2 on a chat: it offers 0.0.3, updates and reopens"
# The app's data: an instance it opens (nobody answers at its address,
# which is fine: the sidebar and the chat open all the same).
home="$work/home"
mkdir -p "$home"
cat > "$home/instances.json" <<JSON
{"instances": [{"id": "i1", "name": "Test", "url": "http://127.0.0.1:9", "local": false, "auth": "token"}]}
JSON
cat > "$home/settings.json" <<JSON
{"version": 1, "activeInstance": "i1", "autoUpdate": true}
JSON
dbg="$work/dbg"
mkdir -p "$dbg"
# drive has the app that runs do something, and waits for it.
drive() {
  rm -f "$dbg/done"
  printf '%s\n' "$1" > "$dbg/do.tmp" && mv "$dbg/do.tmp" "$dbg/do"
  for _ in $(seq 1 "${2:-50}"); do
    [ -f "$dbg/done" ] && return 0
    sleep 0.2
  done
  rm -f "$dbg/do"
  return 1
}
state() { cat "$dbg/state" 2>/dev/null || true; }
fail() {
  echo "$1"; state; echo "-- its log:"; cat "$work/app.log"; echo "-- the server's:"; cat "$work/server.log"
  drive quit 10 || true
  exit 1
}
# --home and --debug, not the environment: a relaunch keeps arguments on
# every system (macOS reopens the app with /usr/bin/open).
"$app" --home "$(native "$home")" --debug "$(native "$dbg")" > "$work/app.log" 2>&1 &
offered=""
for _ in $(seq 1 60); do
  if drive state 10 && state | grep -q 'offered="0.0.3"'; then offered=yes; break; fi
  sleep 1
done
[ -n "$offered" ] || fail "the app did not offer 0.0.3:"
state
state | grep -q '^version=0.0.2 .*instance=i1' || fail "the app that offers the update is not 0.0.2 on the instance"
drive "open s1" 25 || fail "the app did not open the chat"
drive "type words not sent yet" 25 || fail "the app took no text"
drive state 10
state | grep -q 'chat=s1 draft="words not sent yet"' || fail "the chat and its text are not there before the update"
drive "update accept" 25 || true
reopened=""
for _ in $(seq 1 90); do
  sleep 1
  rm -f "$dbg/state"
  if drive state 10 && state | grep -q '^version=0.0.3 '; then reopened=yes; break; fi
done
[ -n "$reopened" ] || fail "the app did not come back as 0.0.3:"
state
state | grep -q 'instance=i1 chat=s1 draft="words not sent yet"' || fail "the app came back without its instance, chat or text"
drive quit 15 || true
sleep 1
echo "== Hermes offered 0.0.3, updated and reopened by itself on the same chat, on $os/$arch"

case "$os" in
  linux) sh "$dir/install.sh" --uninstall || true ;;
  windows) [ -f "$LOCALAPPDATA/Programs/HermesUpdateTest/Uninstall.exe" ] && "$LOCALAPPDATA/Programs/HermesUpdateTest/Uninstall.exe" //S || true ;;
esac
