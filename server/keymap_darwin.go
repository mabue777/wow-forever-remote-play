package main

/*
#cgo LDFLAGS: -framework Carbon
#include <Carbon/Carbon.h>

// Translate a virtual key code (no modifiers) into the character it produces under the current keyboard layout.
static int wt_char_for_key(int keycode, UniChar *out, int outLen) {
    TISInputSourceRef src = TISCopyCurrentKeyboardLayoutInputSource();
    if (!src) return 0;
    CFDataRef data = (CFDataRef)TISGetInputSourceProperty(src, kTISPropertyUnicodeKeyLayoutData);
    if (!data) { CFRelease(src); return 0; }
    const UCKeyboardLayout *layout = (const UCKeyboardLayout *)CFDataGetBytePtr(data);
    UInt32 dead = 0; UniCharCount len = 0;
    OSStatus st = UCKeyTranslate(layout, (UInt16)keycode, kUCKeyActionDisplay, 0, LMGetKbdType(), kUCKeyTranslateNoDeadKeysMask, &dead, outLen, &len, out);
    CFRelease(src);
    return st == noErr ? (int)len : 0;
}
*/
import "C"
import (
	"strings"
	"sync"
	"unicode/utf16"
)

// charToKeycode maps the character a key produces (current layout, no modifiers) to its macOS key code.
func charToKeycode() map[string]int {
	m := map[string]int{}
	var buf [8]C.UniChar
	for code := 0; code < 128; code++ {
		n := C.wt_char_for_key(C.int(code), &buf[0], 8)
		if n == 0 {
			continue
		}
		u := make([]uint16, n)
		for i := 0; i < int(n); i++ {
			u[i] = uint16(buf[i])
		}
		s := strings.ToUpper(string(utf16.Decode(u)))
		if s == "" || s == " " || s == "\t" || s == "\r" || s == "\x1b" || s == "\x7f" {
			continue
		}
		if _, dup := m[s]; !dup {
			m[s] = code
		}
	}
	return m
}

var (
	charKeysOnce sync.Once
	charKeys     map[string]int
)

// keyForChar returns the key code that types ch (any case) on the Mac's current keyboard layout.
func keyForChar(ch string) (int, bool) {
	charKeysOnce.Do(func() { charKeys = charToKeycode() })
	code, ok := charKeys[strings.ToUpper(ch)]
	return code, ok
}
