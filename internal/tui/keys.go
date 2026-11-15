package tui

import (
	"strconv"
	"strings"

	"github.com/gdamore/tcell/v2"
)

// App-level pseudo actions used in the key table (not engine actions).
const (
	ActCommandBar = "app:command"
	ActQuit       = "app:quit"
	ActNewTab     = "app:newtab"
	ActCloseTab   = "app:closetab"
	ActNextTab    = "app:nexttab"
	ActPrevTab    = "app:prevtab"
	ActSplit      = "app:split"
)

// Actions lists every engine action name (the engine contract). The command
// bar's `key` command and the bare `:<action>` form accept these, case
// insensitively.
var Actions = []string{
	"enter", "fieldExit", "fieldPlus", "fieldMinus", "tab", "backtab",
	"left", "right", "up", "down", "home", "newline", "backspace", "delete",
	"insert", "endOfField", "eraseEof", "eraseInput", "dup", "fieldMark",
	"reset", "attn", "sysreq", "clear", "help", "pageUp", "pageDown", "print",
	"recordBackspace", "testRequest", "fastUp", "fastDown", "fastLeft",
	"fastRight", "wordLeft", "wordRight", "beginningOfField", "deleteWord",
	"eraseField",
	"pf1", "pf2", "pf3", "pf4", "pf5", "pf6", "pf7", "pf8", "pf9", "pf10",
	"pf11", "pf12", "pf13", "pf14", "pf15", "pf16", "pf17", "pf18", "pf19",
	"pf20", "pf21", "pf22", "pf23", "pf24",
}

// LookupAction returns the canonical engine action for name (any case).
func LookupAction(name string) (string, bool) {
	for _, a := range Actions {
		if strings.EqualFold(a, name) {
			return a, true
		}
	}
	return "", false
}

// Binding maps one tcell key (plus modifiers) to an action.
type Binding struct {
	Key    tcell.Key
	Mod    tcell.ModMask
	Action string
	Desc   string
}

// KeyTable is THE key mapping: change keys here. The F1..F24 and Shift+F1..F12
// bindings are appended by init. Rune keys are not in the table (see KeyAction).
var KeyTable = []Binding{
	{tcell.KeyEnter, 0, "enter", "Enter"},
	{tcell.KeyEnter, tcell.ModCtrl, "fieldExit", "Field Exit"},
	{tcell.KeyEnter, tcell.ModShift, "fieldExit", "Field Exit"},
	{tcell.KeyEnter, tcell.ModCtrl | tcell.ModShift, "fieldExit", "Field Exit"},
	// The Windows console delivers Ctrl+Enter as Ctrl+M.
	{tcell.KeyCtrlM, 0, "fieldExit", "Field Exit"},
	{tcell.KeyCtrlJ, 0, "fieldExit", "Field Exit (Ctrl+Enter sends LF on many terminals)"},
	{tcell.KeyTab, 0, "tab", "Tab"},
	{tcell.KeyBacktab, 0, "backtab", "Backtab"},
	{tcell.KeyTab, tcell.ModShift, "backtab", "Backtab"},
	{tcell.KeyUp, 0, "up", "Cursor up"},
	{tcell.KeyDown, 0, "down", "Cursor down"},
	{tcell.KeyLeft, 0, "left", "Cursor left"},
	{tcell.KeyRight, 0, "right", "Cursor right"},
	{tcell.KeyLeft, tcell.ModCtrl, "wordLeft", "Word left"},
	{tcell.KeyRight, tcell.ModCtrl, "wordRight", "Word right"},
	{tcell.KeyHome, 0, "home", "Home"},
	{tcell.KeyEnd, 0, "endOfField", "End of field"},
	{tcell.KeyInsert, 0, "insert", "Insert mode"},
	{tcell.KeyDelete, 0, "delete", "Delete"},
	{tcell.KeyBackspace, 0, "backspace", "Backspace"},
	{tcell.KeyBackspace2, 0, "backspace", "Backspace"},
	{tcell.KeyBackspace, tcell.ModCtrl, "eraseInput", "Erase Input"},
	{tcell.KeyBackspace2, tcell.ModCtrl, "eraseInput", "Erase Input"},
	// tcell folds Backspace2 (DEL) onto Backspace, so Ctrl+Backspace arrives on that code.
	{tcell.KeyBackspace, tcell.ModCtrl, "eraseInput", "Erase Input"},
	{tcell.KeyPgUp, 0, "pageUp", "Roll up (Page Up)"},
	{tcell.KeyPgDn, 0, "pageDown", "Roll down (Page Down)"},
	{tcell.KeyEscape, 0, "attn", "Attention"},
	{tcell.KeyEscape, tcell.ModShift, "sysreq", "System Request"},
	{tcell.KeyCtrlA, 0, "attn", "Attention"},
	{tcell.KeyCtrlX, 0, "sysreq", "System Request"},
	{tcell.KeyCtrlR, 0, "reset", "Reset"},
	{tcell.KeyCtrlE, 0, "eraseEof", "Erase to end of field"},
	{tcell.KeyCtrlK, 0, ActCommandBar, "Command bar"},
	{tcell.KeyCtrlQ, 0, ActQuit, "Quit"},
	{tcell.KeyCtrlC, 0, ActQuit, "Quit"},
	// Tabs (sessions). Alt+1..9 is handled in handleKey: it is a rune chord.
	{tcell.KeyCtrlT, 0, ActNewTab, "New tab"},
	{tcell.KeyCtrlW, 0, ActCloseTab, "Close tab"},
	{tcell.KeyPgUp, tcell.ModCtrl, ActPrevTab, "Previous tab"},
	{tcell.KeyPgDn, tcell.ModCtrl, ActNextTab, "Next tab"},
	{tcell.KeyCtrlBackslash, 0, ActSplit, "Split view / other pane"},
}

func init() {
	for n := 1; n <= 24; n++ {
		key := tcell.KeyF1 + tcell.Key(n-1)
		KeyTable = append(KeyTable, Binding{key, 0, "pf" + strconv.Itoa(n), "F" + strconv.Itoa(n)})
		if n <= 12 {
			KeyTable = append(KeyTable, Binding{key, tcell.ModShift, "pf" + strconv.Itoa(n+12), "Shift+F" + strconv.Itoa(n)})
		}
	}
}

// KeyAction translates a key event. It returns an engine action, an app
// pseudo action (ActCommandBar, ActQuit) or, for a printable key, the text
// to type. ok is false when the key is not bound.
func KeyAction(ev *tcell.EventKey) (action, text string, ok bool) {
	mod := ev.Modifiers()
	if ev.Key() == tcell.KeyRune {
		r := ev.Rune()
		// Ctrl alone or Alt alone is a chord, not text; Ctrl+Alt is AltGr on Windows.
		if (mod&tcell.ModCtrl != 0) != (mod&tcell.ModAlt != 0) {
			return "", "", false
		}
		if r < ' ' || r == 0x7f {
			return "", "", false
		}
		return "", string(r), true
	}
	mask := tcell.ModShift | tcell.ModCtrl | tcell.ModAlt | tcell.ModMeta
	m := mod & mask
	// tcell reports a Ctrl+letter chord as KeyCtrlA..KeyCtrlZ (64 + the letter) with the
	// Ctrl modifier set as well, on Windows and on a terminal alike. The table lists
	// those keys by their code alone, so the modifier is dropped. A raw control byte
	// (what the simulation screen injects) is the same chord by another number and is
	// folded onto the same code; Enter, Tab, Esc and Backspace are typed without Ctrl,
	// so a Ctrl on them is a real chord and stays.
	k := ev.Key()
	if isRawControl(k) {
		k = tcell.KeyCtrlSpace + k
	}
	if k >= tcell.KeyCtrlSpace && k <= tcell.KeyCtrlUnderscore {
		m &^= tcell.ModCtrl
	}
	for _, b := range KeyTable {
		if b.Key == k && b.Mod == m {
			return b.Action, "", true
		}
	}
	// A bound key with extra Shift is still the same key (e.g. Shift+Arrow).
	if m&^tcell.ModShift == 0 {
		for _, b := range KeyTable {
			if b.Key == k && b.Mod == 0 {
				return b.Action, "", true
			}
		}
	}
	return "", "", false
}

// isRawControl reports whether k is a raw control byte (KeyNUL..KeyUS) other than the
// ones typed without Ctrl (Enter, Tab, Esc, Backspace).
func isRawControl(k tcell.Key) bool {
	switch k {
	case tcell.KeyEnter, tcell.KeyTab, tcell.KeyEscape, tcell.KeyBackspace:
		return false
	}
	return k < tcell.Key(' ')
}

// HelpKeys returns "key  description" lines for the help modal, built from
// KeyTable's descriptions (one line per action, first binding wins).
func HelpKeys() [][2]string {
	return [][2]string{
		{"F1-F12", "PF1-PF12"},
		{"Shift+F1-F12", "PF13-PF24 (also F13-F24 where the terminal sends them)"},
		{"Enter", "Enter"},
		{"Ctrl+Enter / Shift+Enter / Ctrl+J", "Field Exit (where the terminal delivers it), else :fieldexit"},
		{"Tab / Shift+Tab", "next / previous field"},
		{"Arrows, Home, End", "cursor, first field, end of field"},
		{"Insert, Delete, Backspace", "insert mode, delete, backspace"},
		{"PgUp / PgDn", "Roll up / Roll down"},
		{"Esc, Ctrl+A", "Attention"},
		{"Ctrl+X, Shift+Esc", "System Request (Shift+Esc where the terminal reports Shift with Esc)"},
		{"Ctrl+R", "Reset (clears an error)"},
		{"Ctrl+E", "Erase to end of field"},
		{"Ctrl+Backspace", "Erase Input"},
		{"Ctrl+K, or : outside a field", "command bar"},
		{"Ctrl+T / Ctrl+W", "new tab (site list) / close tab"},
		{"Ctrl+PgUp / Ctrl+PgDn, Alt+1-9", "previous / next tab, tab by number"},
		{"Ctrl+\\", "split view: open the next tab beside this one; when split, keyboard to the other pane"},
		{"Ctrl+Q, Ctrl+C", "quit (asks first)"},
	}
}
