package main

/*
#cgo LDFLAGS: -framework CoreGraphics -framework CoreFoundation -framework ApplicationServices
#include <ApplicationServices/ApplicationServices.h>
#include <CoreGraphics/CoreGraphics.h>

// Events go straight to the game's process when we know its pid; macOS otherwise routes
// keyboard events to whatever window it considers key, which is not always the game.
static pid_t wt_target = 0;
static void wt_set_target(int pid) { wt_target = (pid_t)pid; }
static void wt_post(CGEventRef e) {
    if (wt_target > 0) CGEventPostToPid(wt_target, e);
    else CGEventPost(kCGHIDEventTap, e);
}
// Mouse goes through the session tap: the game ignores mouse events posted to the HID tap or to its pid,
// but accepts them from the session tap (verified by clicking frame close buttons).
// The delivery path is switchable at runtime (diagnostics): 0 session tap, 1 HID tap, 2 game pid, 3 annotated session tap.
static int wt_mouse_path = 0;
static void wt_set_mouse_path(int p) { wt_mouse_path = p; }
static void wt_post_mouse(CGEventRef e) {
    switch (wt_mouse_path) {
    case 1: CGEventPost(kCGHIDEventTap, e); break;
    case 2: if (wt_target > 0) CGEventPostToPid(wt_target, e); else CGEventPost(kCGSessionEventTap, e); break;
    case 3: CGEventPost(kCGAnnotatedSessionEventTap, e); break;
    default: CGEventPost(kCGSessionEventTap, e);
    }
}

static void wt_key(int keycode, int down, int flags) {
    CGEventRef e = CGEventCreateKeyboardEvent(NULL, (CGKeyCode)keycode, down ? true : false);
    CGEventSetFlags(e, (CGEventFlags)flags);
    wt_post(e);
    CFRelease(e);
}

static void wt_type(const unsigned short *chars, int len) {
    CGEventRef d = CGEventCreateKeyboardEvent(NULL, 0, true);
    CGEventKeyboardSetUnicodeString(d, len, chars);
    wt_post(d);
    CFRelease(d);
    CGEventRef u = CGEventCreateKeyboardEvent(NULL, 0, false);
    CGEventKeyboardSetUnicodeString(u, len, chars);
    wt_post(u);
    CFRelease(u);
}

static CGPoint wt_pos(void) {
    CGEventRef e = CGEventCreate(NULL);
    CGPoint p = CGEventGetLocation(e);
    CFRelease(e);
    return p;
}

// buttons held, tracked here so moves become drags of the right type
static int wt_left = 0, wt_right = 0;

// keep the pointer on the main display: WoW only sees the mouse while it is over its own screen
static CGPoint wt_clamp(CGPoint p) {
    CGRect b = CGDisplayBounds(CGMainDisplayID());
    if (p.x < b.origin.x) p.x = b.origin.x;
    if (p.y < b.origin.y) p.y = b.origin.y;
    if (p.x > b.origin.x + b.size.width - 2) p.x = b.origin.x + b.size.width - 2;
    if (p.y > b.origin.y + b.size.height - 2) p.y = b.origin.y + b.size.height - 2;
    return p;
}

static void wt_move_rel(double dx, double dy) {
    CGPoint p = wt_pos();
    p.x += dx; p.y += dy;
    p = wt_clamp(p);
    CGEventType t = kCGEventMouseMoved;
    CGMouseButton b = kCGMouseButtonLeft;
    if (wt_right) { t = kCGEventRightMouseDragged; b = kCGMouseButtonRight; }
    else if (wt_left) { t = kCGEventLeftMouseDragged; b = kCGMouseButtonLeft; }
    CGEventRef e = CGEventCreateMouseEvent(NULL, t, p, b);
    CGEventSetIntegerValueField(e, kCGMouseEventDeltaX, (int64_t)dx);
    CGEventSetIntegerValueField(e, kCGMouseEventDeltaY, (int64_t)dy);
    wt_post_mouse(e);
    CFRelease(e);
    // mouse-look needs the real cursor to advance; taps/clicks must NOT warp (warping suppresses the click)
    CGWarpMouseCursorPosition(p);
}

static void wt_move_abs(double x, double y) {
    CGPoint p = CGPointMake(x, y);
    CGEventType t = kCGEventMouseMoved;
    CGMouseButton b = kCGMouseButtonLeft;
    if (wt_right) { t = kCGEventRightMouseDragged; b = kCGMouseButtonRight; }
    else if (wt_left) { t = kCGEventLeftMouseDragged; b = kCGMouseButtonLeft; }
    CGEventRef e = CGEventCreateMouseEvent(NULL, t, p, b);
    wt_post_mouse(e);
    CFRelease(e);
}

// button: 0 left, 1 right, 2 middle. x,y < 0 means "current position"
static void wt_button(int button, int down, double x, double y) {
    CGPoint p = (x < 0 || y < 0) ? wt_pos() : CGPointMake(x, y);
    CGEventType t;
    CGMouseButton b;
    if (button == 1) { b = kCGMouseButtonRight; t = down ? kCGEventRightMouseDown : kCGEventRightMouseUp; wt_right = down; }
    else if (button == 2) { b = kCGMouseButtonCenter; t = down ? kCGEventOtherMouseDown : kCGEventOtherMouseUp; }
    else { b = kCGMouseButtonLeft; t = down ? kCGEventLeftMouseDown : kCGEventLeftMouseUp; wt_left = down; }
    CGEventRef e = CGEventCreateMouseEvent(NULL, t, p, b);
    if (button == 0) CGEventSetIntegerValueField(e, kCGMouseEventClickState, 1);
    wt_post_mouse(e);
    CFRelease(e);
}

static void wt_scroll(int dy) {
    CGEventRef e = CGEventCreateScrollWheelEvent(NULL, kCGScrollEventUnitLine, 1, (int32_t)dy);
    wt_post_mouse(e);
    CFRelease(e);
}

static int wt_trusted(void) { return AXIsProcessTrusted() ? 1 : 0; }

// permission checks; the request variants make macOS show its prompt and list the app in System Settings
static int wt_screen_ok(void) { return CGPreflightScreenCaptureAccess() ? 1 : 0; }
static void wt_screen_request(void) { CGRequestScreenCaptureAccess(); }
static int wt_post_ok(void) { return CGPreflightPostEventAccess() ? 1 : 0; }
static void wt_post_request(void) { CGRequestPostEventAccess(); }
static void wt_ax_prompt(void) {
    const void *keys[] = { kAXTrustedCheckOptionPrompt };
    const void *vals[] = { kCFBooleanTrue };
    CFDictionaryRef opts = CFDictionaryCreate(NULL, keys, vals, 1, &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
    AXIsProcessTrustedWithOptions(opts);
    CFRelease(opts);
}

static void wt_screen(double *w, double *h) {
    CGDirectDisplayID d = CGMainDisplayID();
    *w = (double)CGDisplayPixelsWide(d);
    *h = (double)CGDisplayPixelsHigh(d);
}
*/
import "C"
import "unicode/utf16"

const (
	flagShift = 1 << 17
	flagCtrl  = 1 << 18
	flagAlt   = 1 << 19
	flagCmd   = 1 << 20
)

func setTargetPid(pid int) { C.wt_set_target(C.int(pid)) }
func setMousePath(p int)   { C.wt_set_mouse_path(C.int(p)) }

func keyEvent(code int, down bool, flags int) {
	d := 0
	if down {
		d = 1
	}
	C.wt_key(C.int(code), C.int(d), C.int(flags))
}

func typeText(s string) {
	u := utf16.Encode([]rune(s))
	if len(u) == 0 {
		return
	}
	// post in small chunks; CGEvent supports up to 20 chars per event
	for i := 0; i < len(u); i += 16 {
		j := i + 16
		if j > len(u) {
			j = len(u)
		}
		chunk := u[i:j]
		C.wt_type((*C.ushort)(&chunk[0]), C.int(len(chunk)))
	}
}

func mouseMoveRel(dx, dy float64) { C.wt_move_rel(C.double(dx), C.double(dy)) }
func mouseMoveAbs(x, y float64)   { C.wt_move_abs(C.double(x), C.double(y)) }
func mouseButton(button int, down bool, x, y float64) {
	d := 0
	if down {
		d = 1
	}
	C.wt_button(C.int(button), C.int(d), C.double(x), C.double(y))
}
func scroll(dy int)         { C.wt_scroll(C.int(dy)) }
func accessibilityOK() bool { return C.wt_trusted() == 1 }
func screenCaptureOK() bool { return C.wt_screen_ok() == 1 }
func postEventOK() bool     { return C.wt_post_ok() == 1 }
func requestPermissions() {
	if !screenCaptureOK() {
		C.wt_screen_request()
	}
	if !postEventOK() {
		C.wt_post_request()
	}
	if !accessibilityOK() {
		C.wt_ax_prompt()
	}
}
func screenSize() (float64, float64) {
	var w, h C.double
	C.wt_screen(&w, &h)
	return float64(w), float64(h)
}
