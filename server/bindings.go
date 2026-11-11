package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Resolves WoW binding actions (ACTIONBUTTON1, TURNLEFT, ...) to the key the player actually has bound,
// using the Classic defaults overlaid with the account and character bindings-cache.wtf files.

// Classic default bindings for the actions the overlay uses.
var defaultBindings = map[string][]string{
	"MOVEFORWARD": {"W", "UP"}, "MOVEBACKWARD": {"S", "DOWN"}, "TURNLEFT": {"A", "LEFT"}, "TURNRIGHT": {"D", "RIGHT"},
	"STRAFELEFT": {"Q"}, "STRAFERIGHT": {"E"}, "JUMP": {"SPACE"}, "SITORSTAND": {"X"}, "TOGGLEAUTORUN": {"NUMLOCK"},
	"TOGGLERUN": {"NUMPADDIVIDE"}, "TARGETNEARESTENEMY": {"TAB"}, "TARGETPREVIOUSENEMY": {"SHIFT-TAB"},
	"TOGGLEBACKPACK": {"B"}, "TOGGLECHARACTER0": {"C"}, "TOGGLESPELLBOOK": {"P"}, "TOGGLETALENTS": {"N"},
	"TOGGLEQUESTLOG": {"L"}, "TOGGLEWORLDMAP": {"M"}, "TOGGLEGAMEMENU": {"ESCAPE"}, "OPENCHAT": {"ENTER"}, "NAMEPLATES": {"V"}, "TOGGLEUI": {"ALT-Z"},
	"SCREENSHOT": {"PRINTSCREEN"}, "TOGGLESHEATH": {"Z"}, "ATTACKTARGET": {"T"}, "ASSISTTARGET": {"F"},
	"TARGETSELF": {"F1"}, "TARGETPARTYMEMBER1": {"F2"}, "TARGETPARTYMEMBER2": {"F3"}, "TARGETPARTYMEMBER3": {"F4"}, "TARGETPARTYMEMBER4": {"F5"},
}

func init() {
	bar := []string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "0", "-", "="}
	for i, k := range bar {
		defaultBindings[fmt.Sprintf("ACTIONBUTTON%d", i+1)] = []string{k}
	}
	for i := 1; i <= 10; i++ {
		k := fmt.Sprint(i % 10)
		defaultBindings[fmt.Sprintf("BONUSACTIONBUTTON%d", i)] = []string{"CTRL-" + k}
		defaultBindings[fmt.Sprintf("SHAPESHIFTBUTTON%d", i)] = []string{fmt.Sprintf("CTRL-F%d", i)}
	}
}

// named keys WoW uses -> macOS virtual key codes
var namedKeys = map[string]int{
	"SPACE": 49, "TAB": 48, "ESCAPE": 53, "ENTER": 36, "BACKSPACE": 51, "DELETE": 117, "INSERT": 114, "HOME": 115, "END": 119,
	"PAGEUP": 116, "PAGEDOWN": 121, "UP": 126, "DOWN": 125, "LEFT": 123, "RIGHT": 124, "NUMLOCK": 71, "CAPSLOCK": 57,
	"NUMPAD0": 82, "NUMPAD1": 83, "NUMPAD2": 84, "NUMPAD3": 85, "NUMPAD4": 86, "NUMPAD5": 87, "NUMPAD6": 88, "NUMPAD7": 89, "NUMPAD8": 91, "NUMPAD9": 92,
	"NUMPADDIVIDE": 75, "NUMPADMULTIPLY": 67, "NUMPADMINUS": 78, "NUMPADPLUS": 69, "NUMPADDECIMAL": 65, "NUMPADENTER": 76,
	"F1": 122, "F2": 120, "F3": 99, "F4": 118, "F5": 96, "F6": 97, "F7": 98, "F8": 100, "F9": 101, "F10": 109, "F11": 103, "F12": 111, "F13": 105, "F14": 107, "F15": 113, "F16": 106, "F17": 64, "F18": 79, "F19": 80,
}

type keyRef struct {
	Bind  string `json:"bind"`            // WoW's name, e.g. "SHIFT-1"
	Code  int    `json:"code"`            // macOS key code (-1 if mouse)
	Flags int    `json:"flags"`           // modifier flags
	Mouse int    `json:"mouse,omitempty"` // 1 left, 2 right, 3 middle, 4 wheel up, 5 wheel down
	Label string `json:"label"`           // short human label, e.g. "⇧1"
	Phone bool   `json:"phone,omitempty"` // spare key the addon assigned; not one the player bound
}

type bindingsResult struct {
	Character string            `json:"character"`
	Account   string            `json:"account"`
	Loaded    []string          `json:"loaded"`
	Map       map[string]keyRef `json:"map"`
	Unbound   []string          `json:"unbound"`
	Layout    string            `json:"layout"`
	Source    string            `json:"source"`           // "game" (addon snapshot) or "files" (binding files)
	Class     string            `json:"class,omitempty"`  // WARRIOR, HUNTER, ... (addon only)
	Spells    map[string]string `json:"spells,omitempty"` // action -> spell on that button (addon only)
	Synced    string            `json:"synced,omitempty"` // when the game last saved the snapshot
}

type bindingsService struct {
	wtf   string
	mu    sync.Mutex
	chars map[string]int
}

func newBindingsService(wtfDir string) *bindingsService {
	return &bindingsService{wtf: wtfDir, chars: charToKeycode()}
}

// activeCharacter finds the character folder (Account/<account>/<realm>/<character>) the game wrote to last.
func (b *bindingsService) activeCharacter() (dir, account, character string) {
	root := filepath.Join(b.wtf, "Account")
	var best time.Time
	chars, _ := filepath.Glob(filepath.Join(root, "*", "*", "*"))
	for _, c := range chars {
		if fi, err := os.Stat(c); err != nil || !fi.IsDir() || filepath.Base(c) == "SavedVariables" {
			continue
		}
		entries, _ := os.ReadDir(c)
		for _, e := range entries {
			if info, err := e.Info(); err == nil && !e.IsDir() && info.ModTime().After(best) {
				best, dir = info.ModTime(), c
			}
		}
	}
	if dir == "" {
		return "", "", ""
	}
	rel, _ := filepath.Rel(root, dir)
	parts := strings.Split(rel, string(filepath.Separator))
	return dir, parts[0], parts[2]
}

// files returns the bindings-cache.wtf to apply on top of the defaults. The game uses either the character's own
// key bindings (when "Character Specific Key Bindings" is ticked, the file exists) or the account-wide ones, never both.
func (b *bindingsService) files(charDir, account string) []string {
	if p := filepath.Join(charDir, "bindings-cache.wtf"); fileExists(p) {
		return []string{p}
	}
	if p := filepath.Join(b.wtf, "Account", account, "bindings-cache.wtf"); fileExists(p) {
		return []string{p}
	}
	return nil
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }

// gameSnapshot is what the WoWTouchSync addon saved for a character on its last logout or /reload.
type gameSnapshot struct {
	class  string
	binds  map[string][]string // action -> keys in the game's priority order
	phone  map[string]string   // action -> spare key the addon assigned because the action had none
	spells map[string]string   // action -> spell, item or macro on that button
	saved  time.Time
}

var luaString = regexp.MustCompile(`"((?:[^"\\]|\\.)*)"`)

func readSnapshot(charDir string) *gameSnapshot {
	p := filepath.Join(charDir, "SavedVariables", "WoWTouchSync.lua")
	data, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	fi, _ := os.Stat(p)
	s := &gameSnapshot{binds: map[string][]string{}, phone: map[string]string{}, spells: map[string]string{}, saved: fi.ModTime()}
	for _, m := range luaString.FindAllStringSubmatch(string(data), -1) {
		line := strings.ReplaceAll(strings.ReplaceAll(m[1], `\"`, `"`), `\\`, `\`)
		f := strings.SplitN(line, " ", 3)
		switch {
		case f[0] == "class" && len(f) >= 2:
			s.class = f[1]
		case f[0] == "bind" && len(f) == 3:
			s.binds[f[1]] = append(s.binds[f[1]], f[2])
		case f[0] == "phone" && len(f) == 3:
			s.phone[f[1]] = f[2]
		case f[0] == "spell" && len(f) == 3:
			s.spells[f[1]] = f[2]
		}
	}
	return s
}

func parseBindFile(path string, into map[string][]string) {
	fh, err := os.Open(path)
	if err != nil {
		return
	}
	defer fh.Close()
	sc := bufio.NewScanner(fh)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) != 3 || fields[0] != "bind" {
			continue
		}
		key, action := fields[1], fields[2]
		// a key is bound to exactly one action: remove it from whatever had it
		for act, keys := range into {
			for i, k := range keys {
				if k == key {
					into[act] = append(keys[:i], keys[i+1:]...)
					break
				}
			}
			if len(into[act]) == 0 {
				delete(into, act)
			}
		}
		if action != "NONE" {
			into[action] = append(into[action], key)
		}
	}
}

func (b *bindingsService) resolveKey(bind string) (keyRef, bool) {
	ref := keyRef{Bind: bind, Code: -1}
	parts := strings.Split(bind, "-")
	// "-" itself is a key: "SHIFT--" splits oddly, handle by rejoining
	key := parts[len(parts)-1]
	if key == "" {
		key = "-"
		parts = parts[:len(parts)-1]
	}
	label := ""
	for _, m := range parts[:len(parts)-1] {
		switch m {
		case "SHIFT":
			ref.Flags |= flagShift
			label += "⇧"
		case "CTRL":
			ref.Flags |= flagCtrl
			label += "⌃"
		case "ALT":
			ref.Flags |= flagAlt
			label += "⌥"
		case "META":
			ref.Flags |= flagCmd
			label += "⌘"
		}
	}
	switch key {
	case "BUTTON1":
		ref.Mouse = 1
	case "BUTTON2":
		ref.Mouse = 2
	case "BUTTON3":
		ref.Mouse = 3
	case "MOUSEWHEELUP":
		ref.Mouse = 4
	case "MOUSEWHEELDOWN":
		ref.Mouse = 5
	default:
		if c, ok := namedKeys[key]; ok {
			ref.Code = c
		} else if c, ok := b.chars[strings.ToUpper(key)]; ok {
			ref.Code = c
		} else {
			return ref, false
		}
	}
	short := map[string]string{"SPACE": "Space", "ESCAPE": "Esc", "ENTER": "Enter", "NUMLOCK": "NumLk", "NUMPADDIVIDE": "Num/", "BUTTON3": "M3", "TAB": "Tab", "MOUSEWHEELUP": "Wh↑", "MOUSEWHEELDOWN": "Wh↓"}
	if s, ok := short[key]; ok {
		key = s
	}
	ref.Label = label + key
	return ref, true
}

// Resolve builds the action -> key map for the requested actions, for the character played last.
// With the addon installed it uses the game's own answer; otherwise it reads the saved binding files.
func (b *bindingsService) Resolve(actions []string) bindingsResult {
	b.mu.Lock()
	defer b.mu.Unlock()
	charDir, account, character := b.activeCharacter()
	res := bindingsResult{Character: character, Account: account, Map: map[string]keyRef{}, Source: "files"}
	binds := map[string][]string{}
	phone := map[string]string{}
	if snap := readSnapshot(charDir); snap != nil {
		res.Source, res.Class, res.Spells, res.Synced = "game", snap.class, snap.spells, snap.saved.Format(time.RFC3339)
		res.Loaded = []string{filepath.Join(charDir, "SavedVariables", "WoWTouchSync.lua")}
		binds = snap.binds
		for a, k := range snap.phone {
			binds[a] = append(binds[a], k)
			phone[a] = k
		}
	} else {
		for k, v := range defaultBindings {
			binds[k] = append([]string(nil), v...)
		}
		res.Loaded = b.files(charDir, account)
		for _, f := range res.Loaded {
			parseBindFile(f, binds)
		}
	}
	for _, a := range actions {
		found := false
		for _, k := range binds[a] {
			if ref, ok := b.resolveKey(k); ok {
				if phone[a] == k {
					ref.Phone, ref.Label = true, ""
				}
				res.Map[a] = ref
				found = true
				break
			}
		}
		if !found {
			res.Unbound = append(res.Unbound, a)
		}
	}
	return res
}

func (b *bindingsService) handler(w http.ResponseWriter, r *http.Request) {
	var actions []string
	for _, a := range strings.Split(r.URL.Query().Get("actions"), ",") {
		if a = strings.TrimSpace(a); a != "" {
			actions = append(actions, a)
		}
	}
	res := b.Resolve(actions)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(res)
	log.Printf("bindings: character %q (%s %s), %d actions resolved, %d unbound %v", res.Character, res.Source, res.Class, len(res.Map), len(res.Unbound), res.Unbound)
}
