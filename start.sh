#!/bin/zsh
# Starts WoW Touch as its own app (so its macOS permissions do not depend on the terminal). Log: /tmp/wowtouch.log
ROOT="$(cd "$(dirname "$0")" && pwd)"
"$ROOT/build-app.sh" || exit 1
pkill -x wowtouch 2>/dev/null; pkill -x sckcap 2>/dev/null; sleep 0.5
# also listen on this Mac's Tailscale address when Tailscale is running
TS_IP="$(tailscale ip -4 2>/dev/null || /Applications/Tailscale.app/Contents/MacOS/Tailscale ip -4 2>/dev/null | head -1)"
LISTEN="127.0.0.1:8777${TS_IP:+,$TS_IP:8777}"
open -g -a "$HOME/Applications/WoW Touch.app" --args -listen "$LISTEN" "$@"
echo "started; log at /tmp/wowtouch.log"
