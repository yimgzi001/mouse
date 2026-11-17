package engine

import (
	"fmt"
	"strings"
)

// Windows virtual-key codes used by the engine itself.
const (
	vkTab        = 0x09
	vkShift      = 0x10
	vkCtrl       = 0x11
	vkAlt        = 0x12
	vkEsc        = 0x1B
	vkVolumeDown = 0xAE
	vkVolumeUp   = 0xAF
)

var keyCodes = map[string]uint16{
	"backspace": 0x08, "tab": vkTab, "enter": 0x0D, "shift": vkShift, "ctrl": vkCtrl, "alt": vkAlt,
	"right_alt": 0xA5,
	"pause":     0x13, "capslock": 0x14, "esc": vkEsc, "space": 0x20,
	"pageup": 0x21, "pagedown": 0x22, "end": 0x23, "home": 0x24,
	"left": 0x25, "up": 0x26, "right": 0x27, "down": 0x28,
	"printscreen": 0x2C, "insert": 0x2D, "delete": 0x2E,
	"win": 0x5B, "apps": 0x5D,
	"multiply": 0x6A, "add": 0x6B, "subtract": 0x6D, "decimal": 0x6E, "divide": 0x6F,
	"numlock": 0x90, "scrolllock": 0x91,
	"browser_back": 0xA6, "browser_forward": 0xA7, "browser_refresh": 0xA8, "browser_stop": 0xA9,
	"browser_search": 0xAA, "browser_favorites": 0xAB, "browser_home": 0xAC,
	"volume_mute": 0xAD, "volume_down": vkVolumeDown, "volume_up": vkVolumeUp,
	"media_next": 0xB0, "media_prev": 0xB1, "media_stop": 0xB2, "media_play_pause": 0xB3,
	"semicolon": 0xBA, "equals": 0xBB, "comma": 0xBC, "minus": 0xBD, "period": 0xBE, "slash": 0xBF,
	"backquote": 0xC0, "lbracket": 0xDB, "backslash": 0xDC, "rbracket": 0xDD, "quote": 0xDE,
}

var keyAliases = map[string]string{
	"control": "ctrl", "escape": "esc", "return": "enter", "del": "delete", "ins": "insert",
	"pgup": "pageup", "pgdn": "pagedown", "windows": "win", "super": "win", "meta": "win",
	"menu": "apps", "plus": "equals", "prtsc": "printscreen",
	"ralt": "right_alt",
	"-":    "minus", "=": "equals", ",": "comma", ".": "period", "/": "slash", ";": "semicolon",
	"'": "quote", "`": "backquote", "[": "lbracket", "]": "rbracket", "\\": "backslash",
}

func init() {
	for c := 'a'; c <= 'z'; c++ {
		keyCodes[string(c)] = uint16(c - 'a' + 0x41)
	}
	for d := 0; d <= 9; d++ {
		keyCodes[fmt.Sprint(d)] = uint16(0x30 + d)
		keyCodes[fmt.Sprintf("numpad%d", d)] = uint16(0x60 + d)
	}
	for f := 1; f <= 24; f++ {
		keyCodes[fmt.Sprintf("f%d", f)] = uint16(0x70 + f - 1)
	}
}

const (
	maxCombos       = 8
	maxKeysPerCombo = 6
)

// ParseKeys turns text such as "ctrl+shift+esc" into virtual-key codes. Keys
// joined with + are pressed together; combinations separated by spaces are
// pressed one after the other, as in "ctrl+k ctrl+c".
func ParseKeys(text string) ([][]uint16, error) {
	fields := strings.Fields(strings.ToLower(text))
	if len(fields) == 0 {
		return nil, fmt.Errorf("no keys given")
	}
	if len(fields) > maxCombos {
		return nil, fmt.Errorf("more than %d key combinations", maxCombos)
	}
	combos := make([][]uint16, 0, len(fields))
	for _, field := range fields {
		var names []string
		if field == "+" {
			names = []string{"plus"}
		} else {
			names = strings.Split(field, "+")
		}
		if len(names) > maxKeysPerCombo {
			return nil, fmt.Errorf("%q: more than %d keys in one combination", field, maxKeysPerCombo)
		}
		combo := make([]uint16, 0, len(names))
		for _, name := range names {
			if alias, ok := keyAliases[name]; ok {
				name = alias
			}
			code, ok := keyCodes[name]
			if !ok {
				if name == "" {
					return nil, fmt.Errorf("%q: a key name is missing next to +", field)
				}
				return nil, fmt.Errorf("unknown key %q", name)
			}
			combo = append(combo, code)
		}
		combos = append(combos, combo)
	}
	return combos, nil
}

// KeyNames lists every key name ParseKeys accepts, without aliases.
func KeyNames() []string {
	names := make([]string, 0, len(keyCodes))
	for name := range keyCodes {
		names = append(names, name)
	}
	return names
}
