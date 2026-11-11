#!/bin/zsh
# Builds "WoW Touch.app": the server, the capture tool and the phone UI in one signed bundle.
# A stable signature means the macOS permissions granted to the app survive rebuilds.
set -e
ROOT="$(cd "$(dirname "$0")" && pwd)"
APP="$HOME/Applications/WoW Touch.app"
IDENTITY="${WOWTOUCH_SIGN_IDENTITY:-Apple Development}"
(cd "$ROOT/server" && go build -o wowtouch .)
# install the in-game addon (WoW does not follow symlinks, so copy). New addons appear after a game restart.
ADDONS="${WOWTOUCH_ADDONS:-/Applications/World of Warcraft/_forever_/Interface/AddOns}"
if [ -d "$ADDONS" ]; then rm -rf "$ADDONS/WoWTouchSync"; cp -R "$ROOT/addon/WoWTouchSync" "$ADDONS/"; echo "addon installed in $ADDONS"; fi
[ "$ROOT/tools/sckcap" -nt "$ROOT/tools/sckcap.swift" ] || (cd "$ROOT/tools" && swiftc -O -o sckcap sckcap.swift)
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"
cp "$ROOT/server/wowtouch" "$APP/Contents/MacOS/wowtouch"
cp "$ROOT/tools/sckcap" "$APP/Contents/MacOS/sckcap"
rm -rf "$APP/Contents/Resources/web"; ln -s "$ROOT/web" "$APP/Contents/Resources/web"   # live web files: edit and reload
cat > "$APP/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>CFBundleIdentifier</key><string>no.mabue.wowtouch</string>
  <key>CFBundleName</key><string>WoW Touch</string>
  <key>CFBundleDisplayName</key><string>WoW Touch</string>
  <key>CFBundleExecutable</key><string>wowtouch</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleShortVersionString</key><string>1.0</string>
  <key>CFBundleVersion</key><string>1</string>
  <key>LSMinimumSystemVersion</key><string>13.0</string>
  <key>LSUIElement</key><true/>
  <key>NSScreenCaptureUsageDescription</key><string>WoW Touch streams the game screen to your iPhone.</string>
</dict></plist>
PLIST
# Prefer the developer identity (permissions survive rebuilds). If the keychain refuses (for example when run
# outside a normal terminal login), fall back to an ad-hoc signature: it works, but macOS asks for the
# permissions again after the server binary changes.
if codesign --force --sign "$IDENTITY" "$APP/Contents/MacOS/sckcap" 2>/dev/null && codesign --force --sign "$IDENTITY" "$APP" 2>/dev/null; then
  echo "built $APP (signed: $IDENTITY)"
elif codesign -dv "$APP" 2>&1 | grep -q "Authority=Apple Development" && [ -z "$WOWTOUCH_ALLOW_ADHOC" ]; then
  # never silently downgrade a developer-signed app: that would throw away its macOS permissions
  echo "ERROR: cannot reach the signing certificate from this shell. Run ~/Coding/wow-touch/start.sh from a normal Ghostty window." >&2
  exit 1
else
  codesign --force --sign - "$APP/Contents/MacOS/sckcap"
  codesign --force --sign - "$APP"
  echo "built $APP (ad-hoc signature)"
fi
