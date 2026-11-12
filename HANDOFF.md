# WoW Touch — handoff (2026-11-12)

## What it is
An iPhone touch controller for World of Warcraft on the Mac. A Go server streams
the Mac screen and sound to the phone over WebRTC and turns phone touches into
keyboard and mouse input for the game.

- Project: `~/Coding/wow-touch` (its own git repo, nested inside `~/Coding`)
- GitHub: https://github.com/mabue777/wow-forever-remote-play (PUBLIC, `main`, all pushed)
- App: `~/Applications/WoW Touch.app` (bundle id `no.mabue.wowtouch`; keep it, or Mac permissions reset)
- Start: `./start.sh` (builds and signs)  Stop: `./stop.sh`
- Log: `/tmp/wowtouch.log`  Health: `curl localhost:8777/health`
- Phone reaches it through Tailscale serve. Only ONE video peer at a time: any
  test page kicks the user's phone off.

## Layout
- `server/` Go server: WebRTC, input injection (CGEvent), keybind resolver
  (`bindings.go`), analog turning (`turn.go`), sound (`audio.go`).
  Tests: `cd server && go test ./...` (keymap and turn tests pass).
- `tools/sckcap.swift` screen capture + H.264; with `WT_AUDIO=1` also system
  audio as Opus to fd 3. Server flag `-noaudio` disables sound.
- `web/` phone UI (`app.js`, `index.html`, `style.css`, `layout.json` for tuning).
- `addon/WoWTouchSync` in-game addon. `build-app.sh` copies it into
  `/Applications/World of Warcraft/_forever_/Interface/AddOns`. It gives phone
  actions spare keys, records spells while playing, writes a snapshot at logout,
  and owns the camera button (tap = snap, hold = save angle). `/wts` in chat.

## Game facts
- Client `_forever_`, interface 16001, account folder `WTF/Account/101150557#1/93/<Char>`.
- Main character Mabu-Man, level 26 warrior. 55 phone actions all bound.

## Feature state
| Feature | State |
|---|---|
| Keybinds per character | Done, verified in game |
| Spell names on buttons | Done, verified in game |
| Mouse mode (trackpad) | Done, needs user feel test on iPhone |
| Keys mode (iPhone keyboard) | Done, P opened Spellbook; predictive text untested |
| Analog turning stick | Done, verified at 70% push; tune `turn_curve`, `turn_max`, `joystick_size` |
| Camera button | Works; user must save a level angle once by holding Cam |
| Sound | Streams from Mac (log shows Opus packets); NOT yet confirmed audible on phone |

## Open items (waiting on the user)
1. Reload phone page, tap speaker button (top right), confirm sound plays.
   If muting the Mac also mutes the phone, make capture independent of Mac volume.
2. Hold Cam once at a level angle to save it.
3. Feel-test turning, mouse mode, and Keys mode on the real phone.

## Testing without the user's screen
- Claude has no Screen Recording permission; computer use and `screencapture` fail.
- Headless "phone": `/tmp/wt-test/scripts/peek.mjs` saves frames to
  `/tmp/wt-test/shots/`. It disconnects the user's phone; don't use it while they play.
- Offline test server (fake WTF, no video, nothing reaches the game):
  `/tmp/wt-test/wowtouch -listen 127.0.0.1:8790 -novideo -web ../web -wtf /tmp/wt-test/wtf`

## Conventions
- Commits end with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.
- Keep personal info out of the public repo (tailnet name, IPs, email).
