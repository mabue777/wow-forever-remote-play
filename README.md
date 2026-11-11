# WoW Touch

Play WoW on the Mac from an iPhone: the Mac streams the game display over Tailscale or Wi-Fi (WebRTC, ScreenCaptureKit +
VideoToolbox H.264), the phone shows class-specific touch controls and sends keyboard/mouse input straight to the game process.

Parts: `server/` (Go: WebRTC, WebSocket input, CGEvent injection), `tools/sckcap.swift` (screen capture + encoder, Annex-B on stdout),
`web/` (phone UI; `layout.json` holds the per-class layouts).

## Run
    ~/Coding/wow-touch/start.sh          # builds and launches ~/Applications/WoW Touch.app in the background; log: /tmp/wowtouch.log
    ~/Coding/wow-touch/stop.sh
    curl localhost:8777/health           # peers, and the three permissions (ax / screen / post)
The server runs as its own app (bundle id no.mabue.wowtouch) so its permissions do not depend on the terminal that starts it.
Launched from a shell under tmux it silently lost Screen Recording and mouse posting (2026-11-09).
Signing: build-app.sh uses the Apple Development identity when the keychain allows it (grants survive rebuilds), else ad-hoc
(macOS asks again after the server binary changes). Run start.sh once from Ghostty to get the stable signature.
Rebuild the capture tool after editing it: `cd tools && swiftc -O -o sckcap sckcap.swift`.
On the phone (same tailnet): `https://<your-mac>.<your-tailnet>.ts.net/` (via `tailscale serve --bg 8777`, HTTPS 443 -> 127.0.0.1:8777)
Add to Home Screen in Safari for fullscreen.

## Permissions (System Settings > Privacy & Security)
- Screen & System Audio Recording: WoW Touch.
- Accessibility: WoW Touch. Without it key/mouse injection is silently dropped.

## Key bindings and your character
Buttons name WoW actions (ACTIONBUTTON1, SHAPESHIFTBUTTON1, TURNLEFT, ...). The server resolves each to a key for the
character you played last and serves `/bindings`.

- With the addon (`addon/WoWTouchSync`, installed by build-app.sh; restart the game once after the first install):
  on logout or `/reload` it saves the character's class, real key bindings and the spells on the action bars to
  `WTF/Account/<acct>/<realm>/<char>/SavedVariables/WoWTouchSync.lua`. Phone actions you left unbound get a spare key
  (numpad, F13-F19, Home/End/PageUp/PageDown) through session-only override bindings, so every phone button works and
  your saved key bindings stay untouched. `/wts` in game shows how many spare keys are active.
- The phone picks the class layout by itself when the character changes, shows short spell names on the bar buttons,
  and re-reads bindings every 10 s. Mac menu > Sync from game types `/reload` for you.
- Without the addon: defaults plus either the character's own `bindings-cache.wtf` (character-specific bindings) or the
  account one, never both (that is how the game itself does it).

Unbound actions render struck through.

## Controls (web/layout.json — edit and reload)
- Left stick: W/S forward-back, Q/E strafe. Right area: drag = right-mouse camera look, tap = left click (target), long-press = right click (interact/loot), pinch = zoom.
- Bottom: action bar 1-12. Shift/Ctrl toggles apply to the next action button.
- Right column: stances (Ctrl+1/2/3), Tab target, Jump. Top: Esc, Map, Bags, Char, Spells, Talents, Quests, Chat, auto-run, Sit, nameplates.
- Chat opens the iPhone keyboard; return sends via Enter, text, Enter.

## HUD
Hide (top left) removes all phone controls; camera drag, tap and long-press still work. The eye icon in the corner brings
them back. The choice is remembered on the phone. Mac menu > Game interface toggles the game's own UI (Alt-Z).

## Flags
    -fps 60 -width 1280 -bitrate 6M -capture sck|ffmpeg -game "World of Warcraft" -novideo

## Lessons (2026-11-09)
- ffmpeg's avfoundation screen input delivered 8 fps while the screen drew 60; ScreenCaptureKit fixed it.
- Keyboard events posted to the HID tap were dropped after another app had been in front; posting to the game pid (CGEventPostToPid) is reliable.
- Mouse is the opposite: WoW ignores mouse events posted to the HID tap or to its pid, and accepts them only from kCGSessionEventTap. Keyboard -> pid, mouse -> session tap.
- WoW needs ~100 ms between a mouse move and the button press, and ~120 ms between press and release, or the click is ignored. Never warp the cursor around a click (CGWarpMouseCursorPosition suppresses it); do warp during mouse-look so the real cursor advances.
- Never pad frames (fps filter): a static screen followed by motion produced a backlog that ran seconds behind.
- Stamp frames with real arrival time; a fixed 1/60 clock drifted and stalled Safari.
- Without default pion interceptors there is no NACK retransmission.
- Keep the game in front and do not open other windows on the Mac while playing from the phone.
- A synthetic mouse button left down captures the Mac pointer: trackpad dead, cursor hidden, game ignores every click, until the
  posting process exits. Cause: a camera drag whose finger-up never reached the server. Fix: mouseWatchdog in the server releases
  any held button after 0.7 s without input; the phone sends a "hold" heartbeat every 150 ms during a drag. Emergency: stop.sh.
- Fresh server = working mouse. If clicks are ignored, check for a held button before anything else.
- Test harnesses: /tmp-style scripts drove the real page with CDP touch events and checked "does a tap still close the Spellbook".
