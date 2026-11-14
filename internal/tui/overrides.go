package tui

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/gdamore/tcell/v2"

	"github.com/DerKyberTakt/tn5250-tui/internal/highlight"
	"github.com/DerKyberTakt/tn5250-tui/internal/sites"
)

// The palette and the key table are package state (one terminal per process); the
// defaults block of the sites file overrides them at start-up and after :settings or
// :import. Reset* restore the built-in values (tests).

var (
	builtinPalette = map[string]tcell.Color{
		"green": colGreen, "white": colWhite, "red": colRed, "turquoise": colTurquoise,
		"yellow": colYellow, "pink": colPink, "blue": colBlue,
	}
	builtinKeyTable []Binding
)

func init() {
	builtinKeyTable = append([]Binding(nil), KeyTable...)
}

// ApplyColors overrides the palette from name -> "#rrggbb" (unknown names and bad values
// are ignored and returned) and rebuilds the SEU token colours on the new palette.
func ApplyColors(m map[string]string) (skipped []string) {
	ResetColors()
	for name, hex := range m {
		c, ok := parseHexColour(hex)
		if !ok {
			skipped = append(skipped, name+": "+hex)
			continue
		}
		switch name {
		case "green":
			colGreen = c
		case "white":
			colWhite = c
		case "red":
			colRed = c
		case "turquoise":
			colTurquoise = c
		case "yellow":
			colYellow = c
		case "pink":
			colPink = c
		case "blue":
			colBlue = c
		default:
			skipped = append(skipped, name)
		}
	}
	rebuildTokenColours()
	sort.Strings(skipped)
	return skipped
}

// ResetColors restores the built-in palette.
func ResetColors() {
	colGreen, colWhite, colRed = builtinPalette["green"], builtinPalette["white"], builtinPalette["red"]
	colTurquoise, colYellow = builtinPalette["turquoise"], builtinPalette["yellow"]
	colPink, colBlue = builtinPalette["pink"], builtinPalette["blue"]
	rebuildTokenColours()
}

func rebuildTokenColours() {
	tokenColours = map[highlight.Tok]tcell.Color{
		highlight.TokComment:   colBlue,
		highlight.TokKeyword:   colTurquoise,
		highlight.TokBuiltin:   colPink,
		highlight.TokString:    colYellow,
		highlight.TokNumber:    colWhite,
		highlight.TokSpecial:   colPink,
		highlight.TokVariable:  colWhite,
		highlight.TokIndicator: colRed,
		highlight.TokName:      colWhite,
		highlight.TokMarker:    colBlue,
		highlight.TokSpec:      colWhite,
	}
}

func parseHexColour(s string) (tcell.Color, bool) {
	s = strings.TrimSpace(s)
	if len(s) != 7 || s[0] != '#' {
		return 0, false
	}
	n, err := strconv.ParseUint(s[1:], 16, 32)
	if err != nil {
		return 0, false
	}
	return tcell.NewRGBColor(int32(n>>16&0xff), int32(n>>8&0xff), int32(n&0xff)), true
}

// ApplyKeyOverrides rebinds keys from the emulators' key names ("Ctrl+Home", "Alt+F1",
// "Shift+Escape", "Ctrl+K") to engine actions; "" unbinds the key. It starts from the
// built-in table every time. Names a console cannot deliver (Alt+letter, Ctrl+Shift+letter,
// Meta, Numpad, Space, Pause...) and unknown actions are returned as skipped.
func ApplyKeyOverrides(m map[string]string) (skipped []string) {
	ResetKeyTable()
	names := make([]string, 0, len(m))
	for k := range m {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, name := range names {
		action := strings.TrimSpace(m[name])
		if action != "" {
			a, ok := LookupAction(action)
			if !ok {
				skipped = append(skipped, name+": unknown action "+action)
				continue
			}
			action = a
		}
		keys, ok := ParseKeyName(name)
		if !ok {
			skipped = append(skipped, name)
			continue
		}
		for _, k := range keys {
			rebind(k.Key, k.Mod, action, name)
		}
	}
	return skipped
}

// ResetKeyTable restores the built-in key table.
func ResetKeyTable() {
	KeyTable = append([]Binding(nil), builtinKeyTable...)
}

// rebind drops every binding of key+mod and, when action is not "", puts the new one
// first (the lookup takes the first match).
func rebind(key tcell.Key, mod tcell.ModMask, action, desc string) {
	kept := KeyTable[:0]
	for _, b := range KeyTable {
		if b.Key != key || b.Mod != mod {
			kept = append(kept, b)
		}
	}
	KeyTable = kept
	if action != "" {
		KeyTable = append([]Binding{{key, mod, action, desc}}, KeyTable...)
	}
}

// KeyRef is one tcell key with its modifiers.
type KeyRef struct {
	Key tcell.Key
	Mod tcell.ModMask
}

var namedKeys = map[string]tcell.Key{
	"enter": tcell.KeyEnter, "tab": tcell.KeyTab, "escape": tcell.KeyEscape, "esc": tcell.KeyEscape,
	"delete": tcell.KeyDelete, "insert": tcell.KeyInsert, "home": tcell.KeyHome, "end": tcell.KeyEnd,
	"pageup": tcell.KeyPgUp, "pagedown": tcell.KeyPgDn, "arrowup": tcell.KeyUp, "arrowdown": tcell.KeyDown,
	"arrowleft": tcell.KeyLeft, "arrowright": tcell.KeyRight,
}

// ParseKeyName turns an emulator key name into the tcell keys it stands for (a name can
// stand for two: Backspace has two codes, Ctrl+Enter also arrives as Ctrl+M, Shift+Tab
// as Backtab). ok is false for a name the console cannot deliver.
func ParseKeyName(name string) (keys []KeyRef, ok bool) {
	parts := strings.Split(strings.TrimSpace(name), "+")
	if len(parts) == 0 {
		return nil, false
	}
	base := parts[len(parts)-1]
	var mod tcell.ModMask
	for _, p := range parts[:len(parts)-1] {
		switch strings.ToLower(p) {
		case "ctrl", "control":
			mod |= tcell.ModCtrl
		case "alt":
			mod |= tcell.ModAlt
		case "shift":
			mod |= tcell.ModShift
		default: // Meta and anything else
			return nil, false
		}
	}
	lower := strings.ToLower(base)
	switch {
	case len(base) == 1 && base[0] >= 'A' && base[0] <= 'Z' || len(base) == 1 && base[0] >= 'a' && base[0] <= 'z':
		// Only a Ctrl chord is a key; Alt+letter and plain letters are text to the console.
		if mod != tcell.ModCtrl {
			return nil, false
		}
		idx := tcell.Key(strings.ToUpper(base)[0] - 'A' + 1)
		return []KeyRef{{tcell.KeyCtrlSpace + idx, 0}}, true
	case lower == "backspace":
		return []KeyRef{{tcell.KeyBackspace, mod}, {tcell.KeyBackspace2, mod}}, true
	case lower == "tab" && mod == tcell.ModShift:
		return []KeyRef{{tcell.KeyBacktab, 0}, {tcell.KeyTab, tcell.ModShift}}, true
	case lower == "enter" && mod == tcell.ModCtrl:
		return []KeyRef{{tcell.KeyEnter, tcell.ModCtrl}, {tcell.KeyCtrlM, 0}}, true
	case strings.HasPrefix(lower, "f") && len(lower) > 1:
		n, err := strconv.Atoi(lower[1:])
		if err != nil || n < 1 || n > 24 {
			return nil, false
		}
		return []KeyRef{{tcell.KeyF1 + tcell.Key(n-1), mod}}, true
	}
	if k, found := namedKeys[lower]; found {
		return []KeyRef{{k, mod}}, true
	}
	return nil, false
}

// applyDefaults applies the palette, key overrides and the watermark of d to the screen.
func (a *App) applyDefaults(d sites.Defaults) {
	ApplyColors(d.Colors)
	ApplyKeyOverrides(d.Keys)
	a.screen.Watermark, a.screen.WatermarkPos = "", ""
	if d.Watermark != nil {
		a.screen.Watermark, a.screen.WatermarkPos = d.Watermark.Text, d.Watermark.Position
	}
}

// DescribeOverrides is the :settings page summary of what the defaults block overrides.
func DescribeOverrides(d sites.Defaults) string {
	var parts []string
	if len(d.Colors) > 0 {
		parts = append(parts, fmt.Sprintf("%d colours", len(d.Colors)))
	}
	if len(d.Keys) > 0 {
		parts = append(parts, fmt.Sprintf("%d keys", len(d.Keys)))
	}
	if d.Watermark != nil && d.Watermark.Text != "" {
		parts = append(parts, "watermark")
	}
	if len(parts) == 0 {
		return "none (use :import to bring the VS Code extension's over)"
	}
	return strings.Join(parts, ", ") + " (from :import)"
}
