// WoW Touch — phone side. Video over WebRTC, input over WebSocket.
const KEYS = {
  A:0,S:1,D:2,F:3,H:4,G:5,Z:6,X:7,C:8,V:9,B:11,Q:12,W:13,E:14,R:15,Y:16,T:17,
  "1":18,"2":19,"3":20,"4":21,"6":22,"5":23,"=":24,"9":25,"7":26,"-":27,"8":28,"0":29,
  O:31,U:32,"[":33,I:34,P:35,Return:36,L:37,J:38,"'":39,K:40,";":41,"\\":42,",":43,"/":44,N:45,M:46,".":47,
  Tab:48,Space:49,"`":50,Delete:51,Escape:53,NumLock:71,F1:122,F2:120,F3:99,F4:118,F5:96,F6:97,F7:98,F8:100,F9:101,F10:109,F11:103,F12:111,
  Left:123,Right:124,Down:125,Up:126
};
const FLAG = { shift: 1<<17, ctrl: 1<<18, alt: 1<<19, cmd: 1<<20 };

const $ = (s) => document.querySelector(s);
let bindings = { map: {}, unbound: [], character: "" };
function allActions() {
  const acts = new Set();
  const add = (a) => { if (!a) return; (Array.isArray(a) ? a : [a]).forEach((x) => acts.add(x)); };
  const walk = (defs) => (defs || []).forEach((d) => add(d.action));
  walk(layout.top_left); walk(layout.top_right); walk(layout.menu_game);
  if (layout.camera_button) { add(layout.camera_button.action); add(layout.camera_button.hold); }
  Object.values(layout.joystick).forEach(add);
  (layout.actionbar.actions || []).forEach(add);
  (layout.actionbar2 && layout.actionbar2.actions || []).forEach(add);
  Object.values(layout.profiles || {}).forEach((p) => walk(p.right_column));
  acts.add("OPENCHAT");
  return [...acts];
}
let bindingsJSON = "";
async function loadBindings() {
  const r = await fetch("/bindings?actions=" + encodeURIComponent(allActions().join(",")), { cache: "no-store" });
  const text = await r.text();
  const changed = text !== bindingsJSON;
  bindingsJSON = text;
  bindings = JSON.parse(text);
  followCharacter();
  return changed;
}
// a new character (or class) from the game picks its class layout once; a manual pick afterwards sticks
function followCharacter() {
  const cls = (bindings.class || "").toLowerCase();
  const who = bindings.character + "/" + cls;
  if (!cls || !layout.profiles || !layout.profiles[cls] || localStorage.getItem("wowtouch.char") === who) return;
  localStorage.setItem("wowtouch.char", who);
  profileName = cls;
  localStorage.setItem("wowtouch.profile", cls);
}
// pick up changed key bindings, spells or character without a manual reload; never rebuild under a finger
async function refreshBindings() {
  try {
    if (!(await loadBindings())) return;
    if (document.querySelector(".btn.active") || stickId !== null) { bindingsJSON = ""; return; } // try again next round
    send({ t: "release" });
    build();
  } catch (e) {}
}
setInterval(refreshBindings, 10000);
document.addEventListener("visibilitychange", () => { if (!document.hidden) refreshBindings(); });
// resolve an action (or ordered list of fallbacks) to {code, flags, mouse, label} or null
function resolve(action) {
  for (const a of Array.isArray(action) ? action : [action]) { if (bindings.map[a]) return bindings.map[a]; }
  return null;
}
const video = $("#video"), status = $("#status");
let ws, pc, layout, screenW = 1512, screenH = 982;
const sticky = { shift: false, ctrl: false, alt: false, cmd: false };
let sendQueue = [];

const MOUSE_MSGS = new Set(["move", "moveabs", "btn", "scroll", "hold"]);
function send(m) {
  if (layout && layout.mouse === false && MOUSE_MSGS.has(m.t)) return; // mouse disabled in layout.json
  if (ws && ws.readyState === 1) ws.send(JSON.stringify(m));
}
function flagsFor(mods = []) {
  let f = 0;
  for (const m of mods) f |= FLAG[m] || 0;
  if (sticky.shift) f |= FLAG.shift;
  if (sticky.ctrl) f |= FLAG.ctrl;
  return f;
}
function keyDown(name, mods) { send({ t: "key", code: KEYS[name], down: true, flags: flagsFor(mods) }); }
function keyUp(name, mods) { send({ t: "key", code: KEYS[name], down: false, flags: flagsFor(mods) }); }
function tap(name, mods) { send({ t: "tap", code: KEYS[name], flags: flagsFor(mods) }); }
function stickyFlags() { let f = 0; for (const m in sticky) if (sticky[m]) f |= FLAG[m]; return f; }
// press/release a resolved binding (keyboard or mouse)
function refDown(ref) {
  if (ref.mouse) {
    if (ref.mouse === 4 || ref.mouse === 5) { send({ t: "scroll", dy: ref.mouse === 4 ? 1 : -1 }); return; }
    send({ t: "btn", btn: ref.mouse - 1, down: true }); return;
  }
  send({ t: "key", code: ref.code, down: true, flags: ref.flags | stickyFlags() });
}
function refUp(ref) {
  if (ref.mouse) { if (ref.mouse <= 3) send({ t: "btn", btn: ref.mouse - 1, down: false }); return; }
  send({ t: "key", code: ref.code, down: false, flags: ref.flags | stickyFlags() });
}
function actionTap(action) { const r = resolve(action); if (!r) return; refDown(r); setTimeout(() => refUp(r), 30); }

// ---------- connection ----------
function connect() {
  const proto = location.protocol === "https:" ? "wss" : "ws";
  ws = new WebSocket(`${proto}://${location.host}/ws`);
  ws.onopen = () => { status.textContent = "Starting video…"; status.classList.remove("ok"); startVideo(); };
  ws.onclose = () => { status.textContent = "Reconnecting…"; status.classList.remove("ok"); if (pc) { pc.close(); pc = null; } setTimeout(connect, 1500); };
  ws.onmessage = async (ev) => {
    const m = JSON.parse(ev.data);
    if (m.t === "hello") { screenW = m.x; screenH = m.y; }
    else if (m.t === "hb") serverFrames = m.n;
    else if (m.t === "answer") await pc.setRemoteDescription({ type: "answer", sdp: m.sdp });
    else if (m.t === "cand" && m.cand) { try { await pc.addIceCandidate(m.cand); } catch (e) {} }
  };
}
async function startVideo() {
  prevStats = null; hist = [];
  pc = new RTCPeerConnection({ iceServers: [] });
  pc.addTransceiver("video", { direction: "recvonly" });
  pc.ontrack = (ev) => { video.srcObject = ev.streams[0]; video.play().catch(() => {}); };
  pc.onicecandidate = (ev) => { if (ev.candidate) send({ t: "cand", cand: ev.candidate.toJSON() }); };
  pc.onconnectionstatechange = () => {
    status.textContent = pc.connectionState === "connected" ? "Live" : pc.connectionState;
    status.classList.toggle("ok", pc.connectionState === "connected");
    if (pc.connectionState === "failed" || pc.connectionState === "closed") restartVideo();
  };
  const offer = await pc.createOffer();
  await pc.setLocalDescription(offer);
  send({ t: "offer", sdp: offer.sdp });
}

let restarting = false;
function restartVideo(force) {
  if (restarting) return;
  restarting = true;
  if (force) { try { ws && ws.close(); } catch (e) {} restarting = false; return; } // full reconnect: ws.onclose rebuilds everything
  status.textContent = "Reconnecting…"; status.classList.remove("ok");
  try { pc && pc.close(); } catch (e) {}
  pc = null;
  setTimeout(() => { restarting = false; if (ws && ws.readyState === 1) startVideo(); else if (ws) ws.close(); }, 800);
}
// watchdog: the Mac reports how many frames it has sent (heartbeat). If it sent plenty in the last
// 5s but we decoded none, the stream is stuck -> rebuild. A static screen (no frames sent) is left alone.
let serverFrames = 0, hist = [];
async function decodedFrames() {
  if (!pc) return 0;
  let n = 0;
  (await pc.getStats()).forEach((s) => { if (s.type === "inbound-rtp" && s.kind === "video") n = s.framesDecoded || 0; });
  return n;
}
setInterval(async () => {
  if (!pc || pc.connectionState !== "connected" || document.hidden) { hist = []; return; }
  hist.push({ s: serverFrames, d: await decodedFrames() });
  if (hist.length > 6) hist.shift();
  if (hist.length < 6) return;
  const a = hist[0], b = hist[hist.length - 1];
  if (b.s - a.s > 20 && b.d - a.d === 0) { hist = []; restartVideo(); }
}, 1000);

// every 5s send the phone's own view of the stream to the Mac log (decode rate, drops, freezes, buffer, path)
let prevStats = null;
setInterval(async () => {
  if (!pc || pc.connectionState !== "connected") return;
  const st = await pc.getStats();
  let v = null, pair = null, cands = {};
  st.forEach((s) => {
    if (s.type === "inbound-rtp" && s.kind === "video") v = s;
    if (s.type === "candidate-pair" && s.state === "succeeded" && s.nominated) pair = s;
    if (s.type === "local-candidate" || s.type === "remote-candidate") cands[s.id] = s;
  });
  if (!v) return;
  const p = prevStats; prevStats = v;
  if (!p) return;
  const dt = (v.timestamp - p.timestamp) / 1000;
  const n = v.framesDecoded - p.framesDecoded;
  const o = {
    fps: +(n / dt).toFixed(1), recv: +((v.framesReceived - p.framesReceived) / dt).toFixed(1),
    dropped: v.framesDropped - p.framesDropped, freezes: v.freezeCount - p.freezeCount, freezeMs: Math.round((v.totalFreezesDuration - p.totalFreezesDuration) * 1000),
    pli: v.pliCount - p.pliCount, nack: v.nackCount - p.nackCount, lost: v.packetsLost - p.packetsLost,
    jbMs: n ? Math.round((v.jitterBufferDelay - p.jitterBufferDelay) / (v.jitterBufferEmittedCount - p.jitterBufferEmittedCount) * 1000) : null,
    decMs: n ? +((v.totalDecodeTime - p.totalDecodeTime) / n * 1000).toFixed(1) : null,
    assemblyMs: n && v.totalAssemblyTime != null ? Math.round((v.totalAssemblyTime - p.totalAssemblyTime) / n * 1000) : null,
    kbps: Math.round((v.bytesReceived - p.bytesReceived) * 8 / dt / 1000),
    rttMs: pair && pair.currentRoundTripTime != null ? Math.round(pair.currentRoundTripTime * 1000) : null,
    path: pair ? `${(cands[pair.localCandidateId] || {}).address || "?"}->${(cands[pair.remoteCandidateId] || {}).address || "?"}` : null,
    size: `${v.frameWidth}x${v.frameHeight}`, lowPower: navigator.getBattery ? undefined : undefined,
  };
  send({ t: "stats", text: JSON.stringify(o) });
}, 5000);

// tell the player when the Mac is missing a permission, instead of showing a silent black screen
setInterval(async () => {
  try {
    const t = await (await fetch("/health", { cache: "no-store" })).text();
    const missing = [];
    if (t.includes("screen=false")) missing.push("Screen Recording");
    if (t.includes("ax=false")) missing.push("Accessibility");
    const el = $("#perm");
    el.hidden = missing.length === 0;
    if (missing.length) el.textContent = "On the Mac: System Settings › Privacy & Security › turn on WoW Touch under " + missing.join(" and ");
  } catch (e) {}
}, 3000);

// when the app comes back to the foreground, make sure video is alive
document.addEventListener("visibilitychange", () => {
  if (!document.hidden && (!pc || pc.connectionState !== "connected")) restartVideo();
});

// ---------- map a point on screen to the video frame (0..1) ----------
function frameRect() {
  const vw = video.videoWidth || 16, vh = video.videoHeight || 10;
  const ew = video.clientWidth, eh = video.clientHeight;
  const s = Math.min(ew / vw, eh / vh);
  const w = vw * s, h = vh * s;
  return { x: (ew - w) / 2, y: (eh - h) / 2, w, h };
}
function norm(clientX, clientY) {
  const r = frameRect();
  return { x: Math.min(1, Math.max(0, (clientX - r.x) / r.w)), y: Math.min(1, Math.max(0, (clientY - r.y) / r.h)) };
}

// ---------- camera / mouse area ----------
const cam = $("#cam");
const touches = new Map(); // pointerId -> state
let pinchDist = null;
cam.addEventListener("pointerdown", (e) => {
  if (mouseMode) return padDown(e);
  if (e.isPrimary && touches.size) { // leftovers from a touch whose end we never saw
    for (const s of touches.values()) { clearTimeout(s.timer); if (s.right) send({ t: "btn", btn: 1, down: false }); }
    touches.clear(); pinchDist = null;
  }
  cam.setPointerCapture(e.pointerId);
  touches.set(e.pointerId, { x: e.clientX, y: e.clientY, sx: e.clientX, sy: e.clientY, t: performance.now(), moved: false, right: false, timer: null });
  const st = touches.get(e.pointerId);
  if (touches.size === 1) {
    // long press without movement -> right click (interact / loot)
    st.timer = setTimeout(() => {
      if (!st.moved && touches.has(e.pointerId)) {
        const p = norm(st.sx, st.sy);
        send({ t: "moveabs", x: p.x, y: p.y });
        setTimeout(() => send({ t: "btn", btn: 1, down: true, x: p.x, y: p.y }), 100);
        setTimeout(() => send({ t: "btn", btn: 1, down: false, x: p.x, y: p.y }), 220);
        st.longed = true;
        navigator.vibrate && navigator.vibrate(10);
      }
    }, 450);
  } else {
    // second finger: cancel any pending camera drag; prepare pinch
    for (const s of touches.values()) { clearTimeout(s.timer); if (s.right) { send({ t: "btn", btn: 1, down: false }); s.right = false; } }
    const [a, b] = [...touches.values()];
    pinchDist = Math.hypot(a.x - b.x, a.y - b.y);
  }
});
cam.addEventListener("pointermove", (e) => {
  if (mouseMode) return padMove(e);
  const st = touches.get(e.pointerId);
  if (!st) return;
  const dx = e.clientX - st.x, dy = e.clientY - st.y;
  st.x = e.clientX; st.y = e.clientY;
  if (touches.size >= 2) {
    const [a, b] = [...touches.values()];
    const d = Math.hypot(a.x - b.x, a.y - b.y);
    if (pinchDist != null && Math.abs(d - pinchDist) > 18) {
      send({ t: "scroll", dy: d > pinchDist ? 1 : -1 });
      pinchDist = d;
    }
    return;
  }
  if (!st.moved && Math.hypot(e.clientX - st.sx, e.clientY - st.sy) > 14) {
    st.moved = true; clearTimeout(st.timer);
    if (!st.longed) {
      const p = norm(st.sx, st.sy);
      st.queued = { dx: 0, dy: 0 };
      send({ t: "moveabs", x: p.x, y: p.y });
      setTimeout(() => {
        if (!touches.has(e.pointerId)) return; // finger already lifted: it was a flick, not a look
        send({ t: "btn", btn: 1, down: true, x: p.x, y: p.y });
        st.right = true;
        if (st.queued.dx || st.queued.dy) send({ t: "move", dx: st.queued.dx, dy: st.queued.dy });
        st.queued = null;
      }, 100);
    }
  }
  const k = (layout && profile().camera_sensitivity) || 2;
  if (st.right) send({ t: "move", dx: dx * k, dy: dy * k });
  else if (st.queued) { st.queued.dx += dx * k; st.queued.dy += dy * k; }
});
// while a finger holds the camera drag, tell the Mac every 150ms that it is still there;
// if these stop (lost touch-up, frozen page, dropped connection) the Mac releases the button by itself
setInterval(() => { for (const st of touches.values()) if (st.right) { send({ t: "hold" }); break; } }, 150);

function camEnd(e) {
  if (mouseMode) return padEnd(e);
  const st = touches.get(e.pointerId);
  if (!st) return;
  touches.delete(e.pointerId);
  clearTimeout(st.timer);
  if (touches.size < 2) pinchDist = null;
  if (st.right) { send({ t: "btn", btn: 1, down: false }); return; }
  if (!st.moved && !st.longed && performance.now() - st.t < 350 && touches.size === 0) {
    // short tap -> left click at that spot (select target / click UI)
    const p = norm(st.sx, st.sy);
    send({ t: "moveabs", x: p.x, y: p.y });
    setTimeout(() => send({ t: "btn", btn: 0, down: true, x: p.x, y: p.y }), 100);
    setTimeout(() => send({ t: "btn", btn: 0, down: false, x: p.x, y: p.y }), 220);
  }
}
cam.addEventListener("pointerup", camEnd);
cam.addEventListener("pointercancel", camEnd);

// ---------- mouse mode: the screen is a trackpad with a visible cursor (like Jump Desktop) ----------
// one finger slides the cursor, tap = left click, tap then touch-and-drag = drag with the left button held,
// two-finger tap or long press = right click, two-finger slide = scroll (camera zoom in game)
let mouseMode = localStorage.getItem("wowtouch.mousemode") === "on";
const cursorEl = $("#cursor");
const pad = { fingers: new Map(), cx: 0.5, cy: 0.5, lastTapEnd: 0, dragging: false, maxFingers: 0, moved: false,
  start: 0, timer: null, longed: false, scrollY: null, lastMoveSent: 0, moveTimer: null };
function drawCursor() {
  const r = frameRect();
  cursorEl.style.transform = `translate(${r.x + pad.cx * r.w}px, ${r.y + pad.cy * r.h}px)`;
}
function sendCursor(now) {
  // throttle to ~30 per second; always send the final position
  clearTimeout(pad.moveTimer);
  const due = 33 - (performance.now() - pad.lastMoveSent);
  if (now || due <= 0) { pad.lastMoveSent = performance.now(); send({ t: "moveabs", x: pad.cx, y: pad.cy }); }
  else pad.moveTimer = setTimeout(() => sendCursor(true), due);
}
// WoW ignores a click that arrives within ~100 ms of a move, so clicks wait for the cursor to settle
let clickFreeAt = 0; // clicks queue up so a fast double tap never overlaps press and release
function clickAt(btn) {
  sendCursor(true);
  const p = { x: pad.cx, y: pad.cy };
  const at = Math.max(100, clickFreeAt - performance.now());
  clickFreeAt = performance.now() + at + 150;
  setTimeout(() => send({ t: "btn", btn, down: true, x: p.x, y: p.y }), at);
  setTimeout(() => send({ t: "btn", btn, down: false, x: p.x, y: p.y }), at + 120);
  navigator.vibrate && navigator.vibrate(btn ? 12 : 6);
}
function padDown(e) {
  try { cam.setPointerCapture(e.pointerId); } catch (err) {}
  pad.fingers.set(e.pointerId, { x: e.clientX, y: e.clientY });
  pad.maxFingers = Math.max(pad.maxFingers, pad.fingers.size);
  if (pad.fingers.size === 1) {
    pad.moved = false; pad.longed = false; pad.start = performance.now(); pad.maxFingers = 1;
    pad.sx = e.clientX; pad.sy = e.clientY;
    const quickSecondTouch = performance.now() - pad.lastTapEnd < 300;
    // tap, then touch again: arm a drag. The left button goes down once the finger moves and stays down until it
    // lifts (drag items and spells). If the finger lifts without moving it was a double-click.
    pad.dragging = quickSecondTouch; pad.dragDown = false;
    clearTimeout(pad.timer);
    pad.timer = setTimeout(() => {
      if (!pad.moved && !pad.dragging && pad.fingers.size === 1) { pad.longed = true; clickAt(1); }
    }, 500);
  } else {
    clearTimeout(pad.timer);
    const ys = [...pad.fingers.values()].map((f) => f.y);
    pad.scrollY = ys.reduce((a, b) => a + b, 0) / ys.length;
  }
}
function padMove(e) {
  const f = pad.fingers.get(e.pointerId);
  if (!f) return;
  const dx = e.clientX - f.x, dy = e.clientY - f.y;
  f.x = e.clientX; f.y = e.clientY;
  if (Math.hypot(e.clientX - pad.sx, e.clientY - pad.sy) > 8) {
    pad.moved = true; clearTimeout(pad.timer);
    if (pad.dragging && !pad.dragDown) { // press where the cursor rests (no move for >100 ms), then follow the finger
      pad.dragDown = true; pad.dragStart = performance.now();
      send({ t: "btn", btn: 0, down: true, x: pad.cx, y: pad.cy });
    }
  }
  if (pad.fingers.size >= 2) {
    const ys = [...pad.fingers.values()].map((p) => p.y);
    const y = ys.reduce((a, b) => a + b, 0) / ys.length;
    if (pad.scrollY != null && Math.abs(y - pad.scrollY) > 16) { send({ t: "scroll", dy: y > pad.scrollY ? 1 : -1 }); pad.scrollY = y; pad.moved = true; }
    return;
  }
  // relative motion with a little acceleration: slow for precision, fast to cross the screen
  const r = frameRect();
  const speed = (layout && layout.trackpad_speed) || 1.4;
  const accel = 1 + Math.min(2, Math.hypot(dx, dy) / 12);
  pad.cx = Math.min(1, Math.max(0, pad.cx + dx * speed * accel / r.w));
  pad.cy = Math.min(1, Math.max(0, pad.cy + dy * speed * accel / r.h));
  drawCursor();
  sendCursor(false);
}
function padEnd(e) {
  if (!pad.fingers.delete(e.pointerId)) return;
  if (pad.fingers.size) return;
  clearTimeout(pad.timer);
  pad.scrollY = null;
  if (pad.dragging) {
    pad.dragging = false; pad.lastTapEnd = 0;
    if (pad.dragDown) {
      pad.dragDown = false;
      sendCursor(true);
      // WoW ignores a release that comes less than ~120 ms after the press
      const wait = Math.max(60, 150 - (performance.now() - pad.dragStart));
      setTimeout(() => send({ t: "btn", btn: 0, down: false, x: pad.cx, y: pad.cy }), wait);
      return;
    }
    if (!pad.moved && performance.now() - pad.start < 300) clickAt(0); // second click of a double-click
    return;
  }
  const short = performance.now() - pad.start < 300;
  if (!pad.moved && !pad.longed && short) {
    if (pad.maxFingers >= 2) clickAt(1);
    else { clickAt(0); pad.lastTapEnd = performance.now(); }
  }
}
// a held drag tells the Mac it is still there, like the camera drag does
setInterval(() => { if (mouseMode && pad.dragDown) send({ t: "hold" }); }, 150);
window.addEventListener("resize", () => mouseMode && drawCursor());
function setMouseMode(on) {
  mouseMode = on;
  localStorage.setItem("wowtouch.mousemode", on ? "on" : "off");
  document.body.classList.toggle("mouse-mode", on);
  document.querySelectorAll(".btn.mousemode").forEach((b) => b.classList.toggle("on", on));
  // leave nothing held from the other mode
  for (const st of touches.values()) { clearTimeout(st.timer); if (st.right) send({ t: "btn", btn: 1, down: false }); }
  touches.clear(); pad.fingers.clear(); pinchDist = null;
  if (pad.dragDown) send({ t: "btn", btn: 0, down: false });
  pad.dragging = pad.dragDown = false;
  if (on) { drawCursor(); sendCursor(true); }
}

// ---------- joystick -> WQSE ----------
const stick = $("#stick"), knob = $("#knob");
let stickId = null, held = new Set();
function setKeys(want) {
  const j = profile().joystick;
  for (const k of held) if (!want.has(k)) { const r = resolve(j[k]); if (r) refUp(r); held.delete(k); }
  for (const k of want) if (!held.has(k)) { const r = resolve(j[k]); if (r) refDown(r); held.add(k); }
}
stick.addEventListener("pointerdown", (e) => { stickId = e.pointerId; try { stick.setPointerCapture(e.pointerId); } catch (err) {} stick.classList.add("live"); stickMove(e); });
stick.addEventListener("pointermove", (e) => { if (e.pointerId === stickId) stickMove(e); });
// Forward/back are plain key holds. Sideways is an analog turn rate: the Mac pulses the turn key so a light push
// turns slowly and a full push turns at full speed (see server/turn.go). A small dead zone and a curve keep
// fine aim near the centre.
let turnSent = 0, turnSentAt = 0;
function sendTurn(rate, force) {
  const now = performance.now();
  if (!force && Math.abs(rate - turnSent) < 0.03 && (rate === 0 || now - turnSentAt < 200)) return;
  const j = profile().joystick, l = resolve(j.left), r = resolve(j.right);
  if (!l || !r || l.mouse || r.mouse) return;
  turnSent = rate; turnSentAt = now;
  send({ t: "turn", dx: rate, keys: [l.code, l.flags | stickyFlags(), r.code, r.flags | stickyFlags()] });
}
// keep a held turn alive (the Mac stops turning after 600 ms of silence)
setInterval(() => { if (stickId !== null && turnSent !== 0) sendTurn(turnSent, true); }, 200);
function turnRate(nx) {
  const dead = 0.18, curve = (layout && layout.turn_curve) || 1.7, max = (layout && layout.turn_max) || 1;
  const a = Math.abs(nx);
  if (a < dead) return 0;
  return Math.sign(nx) * Math.min(1, Math.pow((a - dead) / (1 - dead), curve) * max);
}
function stickMove(e) {
  const r = stick.getBoundingClientRect();
  const cx = r.left + r.width / 2, cy = r.top + r.height / 2;
  let dx = e.clientX - cx, dy = e.clientY - cy;
  const max = r.width / 2 - 16, d = Math.hypot(dx, dy);
  if (d > max) { dx *= max / d; dy *= max / d; }
  knob.style.transform = `translate(${dx}px, ${dy}px)`;
  const nx = dx / max, ny = dy / max;
  const want = new Set();
  if (ny < -0.35) want.add("forward");
  if (ny > 0.35) want.add("back");
  setKeys(want);
  const rate = turnRate(nx);
  sendTurn(rate, rate === 0 && turnSent !== 0);
}
function stickEnd(e) {
  if (e.pointerId !== stickId) return;
  stickId = null; stick.classList.remove("live"); knob.style.transform = ""; setKeys(new Set()); sendTurn(0, true);
}
stick.addEventListener("pointerup", stickEnd);
stick.addEventListener("pointercancel", stickEnd);

// ---------- buttons ----------
function makeBtn(def, cls = "") {
  const b = document.createElement("div");
  b.className = "btn " + cls + (def.big ? " big" : "");
  b.textContent = def.spell ? shortName(def.spell) : def.label;
  if (def.spell) { b.classList.add("spell"); b.title = def.spell; }
  if (def.mod) {
    b.classList.add("toggle");
    b.addEventListener("pointerdown", (e) => { e.preventDefault(); sticky[def.mod] = !sticky[def.mod]; b.classList.toggle("on", sticky[def.mod]); });
    return b;
  }
  if (def.ui === "chat") { b.addEventListener("pointerdown", openChat); return b; }
  if (def.ui === "menu") { b.addEventListener("pointerdown", openMenu); return b; }
  if (def.ui === "keys") { b.addEventListener("pointerdown", openKeys); return b; }
  if (def.ui === "mousemode") {
    b.classList.add("toggle-text", "mousemode");
    b.classList.toggle("on", mouseMode);
    b.addEventListener("pointerdown", (e) => { e.preventDefault(); setMouseMode(!mouseMode); });
    return b;
  }
  if (def.ui === "hud") { b.addEventListener("pointerdown", (e) => { e.preventDefault(); setHud(false); }); return b; }
  if (def.ui === "refresh") {
    b.classList.add("icon");
    b.setAttribute("aria-label", "Restart video");
    b.innerHTML = '<svg width="16" height="16" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"><path d="M13.5 8a5.5 5.5 0 1 1-1.6-3.9"/><path d="M13.5 2.5v3h-3"/></svg>';
    b.addEventListener("pointerdown", (e) => { e.preventDefault(); b.classList.add("spin"); setTimeout(() => b.classList.remove("spin"), 900); restartVideo(true); });
    return b;
  }
  const ref = resolve(def.action);
  if (!ref) { b.classList.add("unbound"); b.title = "Not bound in game"; return b; }
  if (def.showKey !== false && ref.label) {
    const k = document.createElement("span"); k.className = "key"; k.textContent = ref.label; b.appendChild(k);
  }
  if (def.hold) return holdButton(b, ref, def);
  let down = false;
  b.addEventListener("pointerdown", (e) => {
    e.preventDefault(); b.setPointerCapture(e.pointerId); down = true; b.classList.add("active");
    refDown(ref);
  });
  const up = () => {
    if (!down) return; down = false; b.classList.remove("active");
    refUp(ref);
    if (cls.includes("bar") && Object.values(sticky).some(Boolean)) {
      for (const m in sticky) sticky[m] = false;
      document.querySelectorAll("#mods .btn").forEach((m) => m.classList.remove("on"));
    }
  };
  b.addEventListener("pointerup", up); b.addEventListener("pointercancel", up);
  return b;
}
// tap = action, press and hold = the "hold" action (camera: tap snaps behind the character, hold saves the angle)
function holdButton(b, ref, def) {
  const holdRef = resolve(def.hold);
  let timer = null, held = false;
  b.addEventListener("pointerdown", (e) => {
    e.preventDefault(); b.classList.add("active"); held = false;
    if (holdRef) timer = setTimeout(() => {
      held = true; actionTap(def.hold);
      b.classList.add("saved"); navigator.vibrate && navigator.vibrate(20);
      setTimeout(() => b.classList.remove("saved"), 900);
    }, 600);
  });
  const up = () => {
    if (!b.classList.contains("active")) return;
    b.classList.remove("active"); clearTimeout(timer);
    if (!held) { refDown(ref); setTimeout(() => refUp(ref), 30); }
  };
  b.addEventListener("pointerup", up); b.addEventListener("pointercancel", () => { clearTimeout(timer); b.classList.remove("active"); });
  return b;
}
let profileName = localStorage.getItem("wowtouch.profile");
function cap(s) { return s[0].toUpperCase() + s.slice(1); }
function profile() {
  const names = Object.keys(layout.profiles || {});
  if (!names.length) return layout;
  if (!names.includes(profileName)) profileName = names[0];
  return Object.assign({}, layout, layout.profiles[profileName]);
}
function setProfile(name) {
  profileName = name;
  localStorage.setItem("wowtouch.profile", name);
  send({ t: "release" });
  build();
}
function openPicker(e) {
  e && e.preventDefault();
  const sheet = $("#picker");
  sheet.innerHTML = "";
  for (const name of Object.keys(layout.profiles)) {
    const b = document.createElement("div");
    b.className = "btn pick" + (name === profileName ? " on" : "");
    b.textContent = cap(name);
    b.addEventListener("pointerdown", (ev) => { ev.preventDefault(); setProfile(name); sheet.hidden = true; });
    sheet.appendChild(b);
  }
  sheet.hidden = false;
}
$("#picker").addEventListener("pointerdown", (e) => { if (e.target === e.currentTarget) e.currentTarget.hidden = true; });

// the spell on an action button, from the game (addon); null when unknown
function spellOf(action) { const n = ((bindings.spells || {})[action] || "").trim(); return n || null; } // blank macro names fall back to the number
// "Heroic Strike" -> "HS", "Charge" -> "Char": fits a 40px cell
function shortName(name) {
  const words = name.replace(/[^\p{L}\p{N} ]/gu, "").split(/\s+/).filter(Boolean);
  return words.length > 1 ? words.slice(0, 3).map((w) => w[0].toUpperCase()).join("") : name.slice(0, 4);
}

function build() {
  const L = profile();
  const tl = $("#topleft"), tr = $("#topright"), rc = $("#rightcol"), bar = $("#bar"), bar2 = $("#bar2"), mods = $("#mods");
  for (const el of [tl, tr, rc, bar, bar2, mods]) el.innerHTML = "";
  L.top_left.forEach((d) => tl.appendChild(makeBtn(d, "small")));
  L.top_right.forEach((d) => tr.appendChild(makeBtn(d, "small")));
  if (layout.profiles && Object.keys(layout.profiles).length > 1) {
    const pb = document.createElement("div");
    pb.className = "btn small profile";
    pb.textContent = cap(profileName);
    pb.addEventListener("pointerdown", openPicker);
    tr.appendChild(pb);
  }
  if (layout.camera_button) rc.appendChild(makeBtn(layout.camera_button, "camera"));
  let group = null;
  L.right_column.forEach((d) => {
    if (d.big) { group = null; rc.appendChild(makeBtn(d)); return; }
    if (!group) { group = document.createElement("div"); group.className = "group"; rc.appendChild(group); }
    group.appendChild(makeBtn(d));
  });
  L.actionbar.actions.forEach((a, i) => bar.appendChild(makeBtn({ label: String(i + 1), action: a, spell: spellOf(a) }, "bar")));
  const bar2Bound = L.actionbar2 && L.actionbar2.actions.some((a) => resolve(a));
  if (bar2Bound) {
    L.actionbar2.actions.forEach((a, i) => bar2.appendChild(makeBtn({ label: String(i + 1), action: a, spell: spellOf(a) }, "bar bar2")));
    document.body.classList.add("has-bar2");
  } else document.body.classList.remove("has-bar2");
  L.modifiers.forEach((d) => mods.appendChild(makeBtn(d, "small")));
}

// ---------- chat ----------
const chatbox = $("#chatbox"), chatinput = $("#chatinput");
let typingMode = "chat"; // "chat": Enter, text, Enter.  "type": text only, into whatever field has focus on the Mac
function openChat(e) {
  e && e.preventDefault();
  typingMode = "chat";
  chatinput.placeholder = "Say something… return sends, esc cancels";
  chatbox.hidden = false; chatinput.value = ""; chatinput.focus();
}
function openType() {
  typingMode = "type";
  chatinput.placeholder = "Type into the focused field… return inserts, esc cancels";
  chatbox.hidden = false; chatinput.value = ""; chatinput.focus();
}
chatinput.addEventListener("keydown", (e) => {
  if (e.key === "Enter") {
    const text = chatinput.value;
    chatbox.hidden = true; chatinput.blur();
    if (!text) return;
    if (typingMode === "type") { send({ t: "text", text }); return; }
    actionTap("OPENCHAT");
    setTimeout(() => { send({ t: "text", text }); setTimeout(() => tap("Return"), 120); }, 180);
  } else if (e.key === "Escape") { chatbox.hidden = true; chatinput.blur(); }
});

// ---------- key mode: the iPhone keyboard presses game keys (O, N, Shift-R) instead of typing chat ----------
const keybar = $("#keybar"), keyinput = $("#keyinput"), keylast = $("#keylast");
const keyMods = { shift: false, ctrl: false, alt: false, cmd: false };
function keyModFlags() { let f = 0; for (const m in keyMods) if (keyMods[m]) f |= FLAG[m]; return f; }
function clearKeyMods() { for (const m in keyMods) keyMods[m] = false; keybar.querySelectorAll(".kmod").forEach((b) => b.classList.remove("on")); }
function showSent(label) {
  keylast.textContent = label;
  keylast.classList.remove("flash"); void keylast.offsetWidth; keylast.classList.add("flash");
}
function modLabel(f) { return (f & FLAG.ctrl ? "⌃" : "") + (f & FLAG.alt ? "⌥" : "") + (f & FLAG.shift ? "⇧" : "") + (f & FLAG.cmd ? "⌘" : ""); }
// one character from the keyboard -> one key press on the Mac (its own layout picks the key)
function pressChar(ch) {
  let flags = keyModFlags();
  if (ch === " ") { send({ t: "tap", code: KEYS.Space, flags }); showSent(modLabel(flags) + "Space"); clearKeyMods(); return; }
  if (ch !== ch.toLowerCase()) flags |= FLAG.shift; // a capital letter is Shift + that key
  send({ t: "keychar", text: ch.toLowerCase(), flags });
  showSent(modLabel(flags) + ch.toUpperCase());
  clearKeyMods();
}
function pressNamed(name, label) {
  const flags = keyModFlags();
  send({ t: "tap", code: KEYS[name], flags });
  showSent(modLabel(flags) + label);
  clearKeyMods();
}
function openKeys(e) {
  e && e.preventDefault();
  keybar.hidden = false; keyinput.value = ""; keylast.textContent = "Press a key"; keylast.classList.remove("flash");
  keyinput.focus();
}
function closeKeys() { keybar.hidden = true; keyinput.blur(); clearKeyMods(); }
keyinput.addEventListener("beforeinput", (e) => {
  if (e.inputType === "insertText" && e.data) { e.preventDefault(); [...e.data].forEach(pressChar); }
  else if (e.inputType === "deleteContentBackward") { e.preventDefault(); pressNamed("Delete", "Delete"); }
  else if (e.inputType === "insertLineBreak" || e.inputType === "insertParagraph") { e.preventDefault(); pressNamed("Return", "Enter"); }
});
// fallback for keyboards that skip beforeinput (predictive text): send whatever landed in the field, then clear it
keyinput.addEventListener("input", () => { const v = keyinput.value; keyinput.value = ""; [...v].forEach(pressChar); });
keyinput.addEventListener("keydown", (e) => { // hardware keyboards
  if (e.key === "Escape") { e.preventDefault(); pressNamed("Escape", "Esc"); }
  else if (e.key === "Tab") { e.preventDefault(); pressNamed("Tab", "Tab"); }
});
keyinput.addEventListener("blur", () => { if (!keybar.hidden) setTimeout(() => { if (!keybar.hidden && document.activeElement !== keyinput) closeKeys(); }, 250); });
keybar.querySelectorAll("[data-mod]").forEach((b) => b.addEventListener("pointerdown", (e) => {
  e.preventDefault(); const m = b.dataset.mod; keyMods[m] = !keyMods[m]; b.classList.toggle("on", keyMods[m]); keyinput.focus();
}));
keybar.querySelectorAll("[data-key]").forEach((b) => b.addEventListener("pointerdown", (e) => {
  e.preventDefault(); pressNamed(b.dataset.key, b.textContent); keyinput.focus();
}));
$("#keydone").addEventListener("pointerdown", (e) => { e.preventDefault(); closeKeys(); });

// ---------- Mac menu: launcher and desktop helpers ----------
const MENU = [
  { label: "Open Battle.net", hint: "then tap Play", run: () => send({ t: "launch" }) },
  { label: "Type text", hint: "into focused field", run: openType },
  { label: "Return", hint: "Enter key", run: () => tap("Return") },
  { label: "Delete", hint: "backspace", run: () => tap("Delete") },
  { label: "Release all keys", hint: "if stuck", run: () => send({ t: "release" }) },
  { label: "Sync from game", hint: "keys, spells, class · reloads game UI", run: syncFromGame },
  { label: "Reload bindings", hint: "after rebinding", run: () => loadBindings().then(build) },
];
// /reload makes the WoWTouchSync addon save this character's keys and spells; the Mac reads them a moment later
function syncFromGame() {
  actionTap("OPENCHAT");
  setTimeout(() => { send({ t: "text", text: "/reload" }); setTimeout(() => tap("Return"), 120); }, 180);
  for (const ms of [4000, 8000, 15000]) setTimeout(() => { bindingsJSON = ""; refreshBindings(); }, ms);
}
function openMenu(e) {
  e && e.preventDefault();
  const sheet = $("#menu");
  sheet.innerHTML = "";
  const game = document.createElement("div"); game.className = "menu-row";
  for (const d of layout.menu_game || []) {
    const ref = resolve(d.action);
    const b = document.createElement("div");
    b.className = "btn pick game-item" + (ref ? "" : " unbound");
    b.innerHTML = `<span>${d.label}</span>` + (ref ? `<small>${ref.label}</small>` : "");
    if (ref) b.addEventListener("pointerdown", (ev) => { ev.preventDefault(); sheet.hidden = true; refDown(ref); setTimeout(() => refUp(ref), 30); });
    game.appendChild(b);
  }
  sheet.appendChild(game);
  const mac = document.createElement("div"); mac.className = "mac-items";
  for (const item of MENU) {
    const b = document.createElement("div");
    b.className = "btn pick menu-item";
    b.innerHTML = `<span>${item.label}</span><small>${item.hint}</small>`;
    b.addEventListener("pointerdown", (ev) => { ev.preventDefault(); sheet.hidden = true; item.run(); });
    mac.appendChild(b);
  }
  sheet.appendChild(mac);
  sheet.hidden = false;
}
$("#menu").addEventListener("pointerdown", (e) => { if (e.target === e.currentTarget) e.currentTarget.hidden = true; });

// HUD switch: hides every control so only the game shows; camera, tap and long-press still work.
// A small button in the corner brings the controls back. Remembered on this phone.
function setHud(on) {
  document.body.classList.toggle("hud-off", !on);
  localStorage.setItem("wowtouch.hud", on ? "on" : "off");
  if (!on) send({ t: "release" });
}
$("#hudshow").addEventListener("pointerdown", (e) => { e.preventDefault(); setHud(true); });
setHud(localStorage.getItem("wowtouch.hud") !== "off");

// idle fade: controls dim when untouched so more of the game shows; any touch brings them back
let fadeTimer = null;
function wake() {
  document.body.classList.remove("idle");
  clearTimeout(fadeTimer);
  fadeTimer = setTimeout(() => document.body.classList.add("idle"), (layout && layout.fade_after_ms) || 3000);
}
document.addEventListener("pointerdown", wake, { capture: true, passive: true });
document.addEventListener("pointermove", wake, { capture: true, passive: true });
wake();

// safety: release everything when the page hides
document.addEventListener("visibilitychange", () => { if (document.hidden) send({ t: "release" }); });
window.addEventListener("blur", () => send({ t: "release" }));
document.addEventListener("gesturestart", (e) => e.preventDefault());
document.addEventListener("dblclick", (e) => e.preventDefault());

fetch("layout.json").then((r) => r.json()).then(async (l) => { layout = l; document.documentElement.style.setProperty("--stick", (l.joystick_size || 150) + "px"); await loadBindings(); build(); setMouseMode(mouseMode); connect(); });
video.addEventListener("loadedmetadata", () => mouseMode && drawCursor());
