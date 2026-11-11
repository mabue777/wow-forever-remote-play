package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pion/interceptor"
	"github.com/pion/rtcp"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
	"github.com/pion/webrtc/v4/pkg/media/h264reader"
)

var (
	listen      = flag.String("listen", "127.0.0.1:8777", "http listen address")
	webDir      = flag.String("web", "../web", "static web directory")
	screen      = flag.String("screen", "Capture screen 0", "avfoundation screen device name")
	fps         = flag.Int("fps", 60, "capture framerate")
	width       = flag.Int("width", 1280, "stream width (height follows aspect)")
	bitrate     = flag.String("bitrate", "6M", "video bitrate")
	noVideo     = flag.Bool("novideo", false, "disable ffmpeg capture (input only)")
	logPath     = flag.String("log", "", "log file (default: stderr; /tmp/wowtouch.log when run as an app bundle)")
	wtfDir      = flag.String("wtf", "/Applications/World of Warcraft/_forever_/WTF", "WoW WTF folder (for key bindings)")
	capture     = flag.String("capture", "sck", "capture backend: sck (ScreenCaptureKit tool) or ffmpeg (avfoundation)")
	gameProcess = flag.String("game", "World of Warcraft", "process name to keep in front while a phone is connected")
)

var (
	capturePath string               // set when running from the app bundle
	childLog    *os.File = os.Stderr // where the capture tool's messages go
)

type hub struct {
	mu        sync.Mutex
	track     *webrtc.TrackLocalStaticSample
	peers     int
	api       *webrtc.API
	scrW      float64
	scrH      float64
	keysMu    sync.Mutex
	held      map[int]bool // keys currently held, released on disconnect
	ffmpeg    *exec.Cmd
	frames    chan []byte
	dropped   atomic.Int64
	sent      atomic.Int64
	pli       atomic.Int64
	mouseHeld [3]bool
	holdMu    sync.Mutex
	lastHold  time.Time    // last sign of life from the finger that holds a mouse button
	lastInput atomic.Int64 // unix ms of the last input message from any phone
	keyMu     sync.Mutex
	lastKey   []byte // most recent keyframe access unit (Annex-B), for /snapshot
}

func main() {
	flag.Parse()
	log.SetFlags(log.Ltime | log.Lmicroseconds)

	// When run from "WoW Touch.app", find the web files and capture tool inside the bundle and log to a file.
	// Running as an app gives the server its own macOS permissions, independent of whichever terminal started it.
	if exe, err := os.Executable(); err == nil && strings.Contains(exe, ".app/Contents/MacOS/") {
		macOS := filepath.Dir(exe)
		if *webDir == "../web" {
			*webDir = filepath.Join(filepath.Dir(macOS), "Resources", "web")
		}
		capturePath = filepath.Join(macOS, "sckcap")
		if *logPath == "" {
			*logPath = "/tmp/wowtouch.log"
		}
	}
	if *logPath != "" {
		if f, err := os.OpenFile(*logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644); err == nil {
			log.SetOutput(f)
			childLog = f
		}
	}
	requestPermissions()
	log.Printf("permissions: screen recording=%v, post events=%v, accessibility=%v", screenCaptureOK(), postEventOK(), accessibilityOK())

	h := &hub{held: map[int]bool{}, frames: make(chan []byte, 4)}
	h.scrW, h.scrH = screenSize()
	log.Printf("main display %.0fx%.0f points; accessibility trusted=%v", h.scrW, h.scrH, accessibilityOK())
	if !accessibilityOK() {
		log.Printf("WARNING: this process is not trusted for Accessibility; key/mouse injection will be ignored until granted")
	}

	var err error
	h.track, err = webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeH264}, "video", "wowtouch")
	if err != nil {
		log.Fatal(err)
	}

	me := &webrtc.MediaEngine{}
	if err := me.RegisterDefaultCodecs(); err != nil {
		log.Fatal(err)
	}
	se := webrtc.SettingEngine{}
	se.SetIPFilter(func(ip net.IP) bool {
		// Tailscale (100.64/10) and private LAN; ICE picks the best pair
		if ip4 := ip.To4(); ip4 != nil {
			return (ip4[0] == 100 && ip4[1] >= 64 && ip4[1] <= 127) || ip4[0] == 10 || (ip4[0] == 192 && ip4[1] == 168) || (ip4[0] == 172 && ip4[1] >= 16 && ip4[1] <= 31)
		}
		return false
	})
	se.SetICETimeouts(12*time.Second, 40*time.Second, 1*time.Second)
	ir := &interceptor.Registry{}
	if err := webrtc.RegisterDefaultInterceptors(me, ir); err != nil {
		log.Fatal(err)
	}
	h.api = webrtc.NewAPI(webrtc.WithMediaEngine(me), webrtc.WithSettingEngine(se), webrtc.WithInterceptorRegistry(ir))

	if !*noVideo {
		go h.runCapture()
		go h.sender()
	}
	go h.focusGuard()
	go h.mouseWatchdog()
	// make sure a Ctrl-C / kill also takes ffmpeg down, otherwise it keeps the screen device busy
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigs
		h.mu.Lock()
		if h.ffmpeg != nil {
			h.ffmpeg.Process.Kill()
		}
		h.mu.Unlock()
		os.Exit(0)
	}()

	abs, _ := filepath.Abs(*webDir)
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", h.handleWS)
	// latest keyframe as raw H.264, for checking what the stream shows without a second screen capture
	mux.HandleFunc("/snapshot.h264", func(w http.ResponseWriter, r *http.Request) {
		h.keyMu.Lock()
		b := append([]byte(nil), h.lastKey...)
		h.keyMu.Unlock()
		w.Header().Set("Content-Type", "video/h264")
		w.Write(b)
	})
	mux.HandleFunc("/bindings", newBindingsService(*wtfDir).handler)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		h.mu.Lock()
		defer h.mu.Unlock()
		fmt.Fprintf(w, "ok peers=%d ax=%v screen=%v post=%v\n", h.peers, accessibilityOK(), screenCaptureOK(), postEventOK())
	})
	fs := http.FileServer(http.Dir(abs))
	mux.Handle("/", noCache(fs))
	addrs := strings.Split(*listen, ",")
	for _, a := range addrs[1:] {
		go func(a string) { log.Fatal(http.ListenAndServe(strings.TrimSpace(a), mux)) }(a)
	}
	log.Printf("serving %s on http://%s", abs, *listen)
	log.Fatal(http.ListenAndServe(strings.TrimSpace(addrs[0]), mux))
}

func noCache(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

// ---------- video capture ----------

// parseBitrate turns "6M" / "800k" / "6000000" into bits per second
func parseBitrate(s string) int {
	s = strings.TrimSpace(s)
	mult := 1
	switch {
	case strings.HasSuffix(s, "M"), strings.HasSuffix(s, "m"):
		mult, s = 1_000_000, s[:len(s)-1]
	case strings.HasSuffix(s, "k"), strings.HasSuffix(s, "K"):
		mult, s = 1_000, s[:len(s)-1]
	}
	var n float64
	fmt.Sscanf(s, "%g", &n)
	return int(n * float64(mult))
}

func (h *hub) runCapture() {
	for {
		h.captureOnce()
		log.Printf("capture exited; restarting in 2s")
		time.Sleep(2 * time.Second)
	}
}

func (h *hub) captureOnce() {
	var cmd *exec.Cmd
	if *capture == "sck" {
		exe := capturePath
		if exe == "" {
			exe, _ = filepath.Abs(filepath.Join(filepath.Dir(*webDir), "tools", "sckcap"))
		}
		cmd = exec.Command(exe, fmt.Sprint(*fps), fmt.Sprint(*width), fmt.Sprint(parseBitrate(*bitrate)))
	} else {
		// select: pass a frame only if at least 1/(fps+5) s passed since the last one (rate cap without duplicating frames)
		vf := fmt.Sprintf("select='isnan(prev_selected_t)+gte(t-prev_selected_t\\,1/%d)',scale=%d:-2", *fps+5, *width)
		args := []string{
			"-hide_banner", "-loglevel", "warning",
			"-fflags", "nobuffer", "-probesize", "32", "-analyzeduration", "0",
			"-f", "avfoundation", "-capture_cursor", "0", "-framerate", fmt.Sprint(*fps),
			"-pixel_format", "nv12", "-i", *screen + ":none",
			"-vf", vf, "-fps_mode", "passthrough",
			"-c:v", "h264_videotoolbox", "-realtime", "true", "-allow_sw", "false",
			"-profile:v", "baseline", "-level", "4.2", "-bf", "0",
			"-b:v", *bitrate, "-maxrate", *bitrate, "-bufsize", "1M",
			"-g", fmt.Sprint(*fps), "-pix_fmt", "yuv420p",
			"-flags", "low_delay",
			"-f", "h264", "-",
		}
		cmd = exec.Command("ffmpeg", args...)
	}
	cmd.Stderr = childLog
	out, err := cmd.StdoutPipe()
	if err != nil {
		log.Print(err)
		return
	}
	if err := cmd.Start(); err != nil {
		log.Printf("ffmpeg start: %v", err)
		return
	}
	h.mu.Lock()
	h.ffmpeg = cmd
	h.mu.Unlock()
	log.Printf("capture %s started pid=%d (%d fps, %dpx wide, %s)", *capture, cmd.Process.Pid, *fps, *width, *bitrate)
	rd, err := h264reader.NewReader(bufio.NewReaderSize(out, 1<<20))
	if err != nil {
		log.Print(err)
		cmd.Process.Kill()
		return
	}
	start := []byte{0, 0, 0, 1}
	var pending []byte // SPS/PPS/SEI waiting to be sent with the next picture
	frames := 0
	havePic := false
	hasIDR := false
	lastLog := time.Now()
	for {
		nal, err := rd.NextNAL()
		if err != nil {
			if err != io.EOF {
				log.Printf("h264 read: %v", err)
			}
			break
		}
		isSlice := nal.UnitType == 1 || nal.UnitType == 5
		// VideoToolbox splits a picture into several slices; first_mb_in_slice==0 (first payload bit set) starts a new picture
		newPicture := isSlice && len(nal.Data) > 1 && nal.Data[1]&0x80 != 0
		if newPicture && havePic {
			if hasIDR {
				h.keyMu.Lock()
				h.lastKey = append(h.lastKey[:0], pending...)
				h.keyMu.Unlock()
			}
			hasIDR = false
			h.enqueue(pending)
			pending = nil
			havePic = false
			frames++
		}
		pending = append(pending, start...)
		pending = append(pending, nal.Data...)
		if isSlice {
			havePic = true
		}
		if nal.UnitType == 5 {
			hasIDR = true
		}
		if !newPicture {
			continue
		}
		if time.Since(lastLog) > 30*time.Second {
			log.Printf("capture: %.1f fps over last 30s, %d frames dropped for slow network, %d picture-loss requests", float64(frames)/time.Since(lastLog).Seconds(), h.dropped.Swap(0), h.pli.Swap(0))
			frames = 0
			lastLog = time.Now()
		}
	}
	cmd.Wait()
}

// enqueue hands a picture to the sender; if the network is slow the oldest picture is dropped
func (h *hub) enqueue(pic []byte) {
	select {
	case h.frames <- pic:
	default:
		select {
		case <-h.frames:
			h.dropped.Add(1)
		default:
		}
		h.frames <- pic
	}
}

// sender stamps each picture against a wall-clock base so the stream clock never drifts from real time:
// bursts of frames read together get a tiny step, and the next frame absorbs the difference.
func (h *hub) sender() {
	base := time.Now()
	var acc time.Duration
	for pic := range h.frames {
		target := time.Since(base)
		dur := target - acc
		if dur < time.Millisecond {
			dur = time.Millisecond
		}
		acc += dur
		if err := h.track.WriteSample(media.Sample{Data: pic, Duration: dur}); err != nil {
			log.Printf("write sample: %v", err)
		}
		h.sent.Add(1)
	}
}

// mouseWatchdog is a dead-man's switch: a mouse button the phone pressed is released if the phone goes quiet.
// A held synthetic button captures the Mac's pointer (the game stays in mouse-look and every click is ignored),
// so a lost "finger up" must never be able to leave one down.
func (h *hub) mouseWatchdog() {
	for {
		time.Sleep(100 * time.Millisecond)
		h.holdMu.Lock()
		quiet := time.Since(h.lastHold)
		h.holdMu.Unlock()
		if quiet < 700*time.Millisecond {
			continue
		}
		for b := 0; b < 3; b++ {
			if h.mouseHeld[b] {
				mouseButton(b, false, -1, -1)
				h.mouseHeld[b] = false
				log.Printf("watchdog: released mouse button %d after %.1fs without input", b, quiet.Seconds())
			}
		}
	}
}

// focusGuard keeps the game in front while a phone is connected, so taps and keys land in the game
// and not in whatever window last took focus on the Mac.
func (h *hub) focusGuard() {
	lastPid := -1
	for {
		// resolve the game's pid; events are posted straight to it
		pid := 0
		if out, err := exec.Command("pgrep", "-x", *gameProcess).Output(); err == nil {
			fmt.Sscanf(strings.TrimSpace(string(out)), "%d", &pid)
		}
		if pid != lastPid {
			setTargetPid(pid)
			if pid > 0 {
				log.Printf("input target: %q pid %d", *gameProcess, pid)
			} else {
				log.Printf("input target: %q not running, posting to the system instead", *gameProcess)
			}
			lastPid = pid
		}
		time.Sleep(2 * time.Second)
		h.mu.Lock()
		peers := h.peers
		h.mu.Unlock()
		if peers == 0 || time.Now().UnixMilli()-h.lastInput.Load() > 5000 {
			continue // nobody is playing from the phone right now: leave the Mac alone
		}
		out, err := exec.Command("osascript", "-e", `tell application "System Events" to get name of first process whose frontmost is true`).Output()
		if err != nil {
			continue
		}
		front := strings.TrimSpace(string(out))
		if front == *gameProcess || front == "" {
			continue
		}
		if err := exec.Command("osascript", "-e", `tell application "System Events" to set frontmost of process "`+*gameProcess+`" to true`).Run(); err == nil {
			log.Printf("focus: brought %q to front (was %q)", *gameProcess, front)
		}
	}
}

// ---------- websocket: signaling + input ----------

var upgrader = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}

type msg struct {
	T     string                   `json:"t"`
	SDP   string                   `json:"sdp,omitempty"`
	Cand  *webrtc.ICECandidateInit `json:"cand,omitempty"`
	Code  int                      `json:"code,omitempty"`
	Flags int                      `json:"flags,omitempty"`
	Down  bool                     `json:"down,omitempty"`
	DX    float64                  `json:"dx,omitempty"`
	DY    float64                  `json:"dy,omitempty"`
	X     float64                  `json:"x,omitempty"` // normalized 0..1 of the stream frame
	Y     float64                  `json:"y,omitempty"`
	Btn   int                      `json:"btn,omitempty"`
	Text  string                   `json:"text,omitempty"`
	Keys  []int                    `json:"keys,omitempty"`
	N     int64                    `json:"n,omitempty"`
}

func (h *hub) handleWS(w http.ResponseWriter, r *http.Request) {
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer ws.Close()
	var wsMu sync.Mutex
	send := func(m msg) {
		wsMu.Lock()
		defer wsMu.Unlock()
		ws.WriteJSON(m)
	}
	log.Printf("client connected from %s", r.RemoteAddr)

	var pc *webrtc.PeerConnection
	// newPeer builds a fresh WebRTC link; called for every offer so the phone can rebuild video over the same socket
	newPeer := func() error {
		if pc != nil {
			pc.Close()
		}
		p, err := h.api.NewPeerConnection(webrtc.Configuration{})
		if err != nil {
			return err
		}
		sender, err := p.AddTrack(h.track)
		if err != nil {
			p.Close()
			return err
		}
		go func() {
			lastLog := time.Now()
			for {
				pkts, _, err := sender.ReadRTCP()
				if err != nil {
					return
				}
				for _, pk := range pkts {
					if rr, ok := pk.(*rtcp.ReceiverReport); ok && time.Since(lastLog) > 5*time.Second {
						for _, r := range rr.Reports {
							log.Printf("phone reports: loss %.1f%% (%d total), jitter %d", float64(r.FractionLost)/2.56, r.TotalLost, r.Jitter)
						}
						lastLog = time.Now()
					}
					if _, ok := pk.(*rtcp.PictureLossIndication); ok {
						h.pli.Add(1)
					}
				}
			}
		}()
		p.OnICECandidate(func(c *webrtc.ICECandidate) {
			if c != nil {
				ci := c.ToJSON()
				send(msg{T: "cand", Cand: &ci})
			}
		})
		p.OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
			log.Printf("peer state %s", s)
		})
		pc = p
		return nil
	}
	defer func() {
		if pc != nil {
			pc.Close()
		}
	}()
	h.mu.Lock()
	h.peers++
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		h.peers--
		h.mu.Unlock()
		h.releaseAll()
		log.Printf("client disconnected")
	}()

	send(msg{T: "hello", X: h.scrW, Y: h.scrH})
	// heartbeat: tells the phone how many frames the Mac has sent, so it can tell "stuck" from "static screen"
	hbStop := make(chan struct{})
	defer close(hbStop)
	go func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for {
			select {
			case <-hbStop:
				return
			case <-t.C:
				send(msg{T: "hb", N: h.sent.Load()})
			}
		}
	}()

	for {
		var m msg
		if err := ws.ReadJSON(&m); err != nil {
			return
		}
		switch m.T {
		case "key", "tap", "move", "moveabs", "btn", "scroll", "text", "hold":
			h.lastInput.Store(time.Now().UnixMilli())
			h.holdMu.Lock()
			h.lastHold = time.Now()
			h.holdMu.Unlock()
		}
		switch m.T {
		case "hold": // heartbeat from a finger that is holding a drag; handled above
		case "offer":
			if err := newPeer(); err != nil {
				log.Printf("new peer: %v", err)
				continue
			}
			if err := pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: m.SDP}); err != nil {
				log.Printf("set remote: %v", err)
				continue
			}
			ans, err := pc.CreateAnswer(nil)
			if err != nil {
				log.Printf("answer: %v", err)
				continue
			}
			if err := pc.SetLocalDescription(ans); err != nil {
				log.Printf("set local: %v", err)
				continue
			}
			send(msg{T: "answer", SDP: ans.SDP})
		case "cand":
			if m.Cand != nil && pc != nil {
				pc.AddICECandidate(*m.Cand)
			}
		case "key":
			h.key(m.Code, m.Down, m.Flags)
		case "tap": // press+release one key with flags
			h.key(m.Code, true, m.Flags)
			time.Sleep(25 * time.Millisecond)
			h.key(m.Code, false, m.Flags)
		case "move":
			mouseMoveRel(m.DX, m.DY)
		case "moveabs":
			mouseMoveAbs(m.X*h.scrW, m.Y*h.scrH)
		case "btn":
			x, y := -1.0, -1.0
			if m.X > 0 || m.Y > 0 {
				x, y = m.X*h.scrW, m.Y*h.scrH
			}
			if m.Btn >= 0 && m.Btn < 3 {
				h.mouseHeld[m.Btn] = m.Down
			}
			mouseButton(m.Btn, m.Down, x, y)
		case "scroll":
			scroll(int(m.DY))
		case "text":
			typeText(m.Text)
		case "launch":
			// fixed command: open the Battle.net app on the WoW tab; the phone taps Play from there
			go func() {
				if out, err := exec.Command("open", "battlenet://WoW").CombinedOutput(); err != nil {
					log.Printf("launch: %v %s", err, out)
				} else {
					log.Printf("launch: opened Battle.net")
				}
			}()
		case "mousepath": // diagnostics: 0 session, 1 hid, 2 pid, 3 annotated session
			setMousePath(m.Code)
			log.Printf("mouse path set to %d", m.Code)
		case "release":
			h.releaseAll()
		case "ping":
			send(msg{T: "pong"})
		case "stats":
			log.Printf("phone stats: %s", m.Text)
		}
	}
}

func (h *hub) key(code int, down bool, flags int) {
	h.keysMu.Lock()
	if down {
		h.held[code] = true
	} else {
		delete(h.held, code)
	}
	h.keysMu.Unlock()
	keyEvent(code, down, flags)
}

func (h *hub) releaseAll() {
	h.keysMu.Lock()
	codes := make([]int, 0, len(h.held))
	for c := range h.held {
		codes = append(codes, c)
	}
	h.held = map[int]bool{}
	h.keysMu.Unlock()
	for _, c := range codes {
		keyEvent(c, false, 0)
	}
	// release mouse buttons only if this server pressed them: unmatched "up" events confuse the game
	if h.mouseHeld[0] {
		mouseButton(0, false, -1, -1)
	}
	if h.mouseHeld[1] {
		mouseButton(1, false, -1, -1)
	}
	h.mouseHeld = [3]bool{}
	if len(codes) > 0 {
		log.Printf("released %d held keys: %s", len(codes), strings.Trim(fmt.Sprint(codes), "[]"))
	}
}

var _ = json.Marshal
