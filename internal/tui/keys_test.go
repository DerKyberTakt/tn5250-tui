package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
)

func TestKeyTable(t *testing.T) {
	cases := []struct {
		name string
		ev   *tcell.EventKey
		act  string
		text string
	}{
		{"Enter", key(tcell.KeyEnter, 0, 0), "enter", ""},
		{"Tab", key(tcell.KeyTab, 0, 0), "tab", ""},
		{"Backtab", key(tcell.KeyBacktab, 0, tcell.ModShift), "backtab", ""},
		{"Shift+Tab", key(tcell.KeyTab, 0, tcell.ModShift), "backtab", ""},
		{"Esc", key(tcell.KeyEscape, 0, 0), "attn", ""},
		{"Shift+Esc", key(tcell.KeyEscape, 0, tcell.ModShift), "sysreq", ""},
		{"F1", key(tcell.KeyF1, 0, 0), "pf1", ""},
		{"F12", key(tcell.KeyF12, 0, 0), "pf12", ""},
		{"Shift+F1", key(tcell.KeyF1, 0, tcell.ModShift), "pf13", ""},
		{"Shift+F12", key(tcell.KeyF12, 0, tcell.ModShift), "pf24", ""},
		{"F13", key(tcell.KeyF13, 0, 0), "pf13", ""},
		{"F24", key(tcell.KeyF24, 0, 0), "pf24", ""},
		{"Up", key(tcell.KeyUp, 0, 0), "up", ""},
		{"Shift+Up still up", key(tcell.KeyUp, 0, tcell.ModShift), "up", ""},
		{"Ctrl+Left", key(tcell.KeyLeft, 0, tcell.ModCtrl), "wordLeft", ""},
		{"Home", key(tcell.KeyHome, 0, 0), "home", ""},
		{"End", key(tcell.KeyEnd, 0, 0), "endOfField", ""},
		{"Insert", key(tcell.KeyInsert, 0, 0), "insert", ""},
		{"Delete", key(tcell.KeyDelete, 0, 0), "delete", ""},
		{"Backspace", key(tcell.KeyBackspace, 0, 0), "backspace", ""},
		{"Backspace2", key(tcell.KeyBackspace2, 0, 0), "backspace", ""},
		{"Ctrl+Backspace", key(tcell.KeyBackspace2, 0, tcell.ModCtrl), "eraseInput", ""},
		{"PgUp", key(tcell.KeyPgUp, 0, 0), "pageUp", ""},
		{"PgDn", key(tcell.KeyPgDn, 0, 0), "pageDown", ""},
		{"Ctrl+R", key(tcell.KeyCtrlR, 0, 0), "reset", ""},
		{"Ctrl+E", key(tcell.KeyCtrlE, 0, 0), "eraseEof", ""},
		{"Ctrl+A", key(tcell.KeyCtrlA, 0, 0), "attn", ""},
		{"Ctrl+X", key(tcell.KeyCtrlX, 0, 0), "sysreq", ""},
		{"Ctrl+K", key(tcell.KeyCtrlK, 0, 0), ActCommandBar, ""},
		// What the backends really post: the Windows console sends Ctrl+letter as a rune
		// with ModCtrl, a Unix terminal sends the raw control byte; tcell turns both into
		// KeyCtrlX with ModCtrl set.
		{"Ctrl+K (Windows)", tcell.NewEventKey(tcell.KeyRune, 'k', tcell.ModCtrl), ActCommandBar, ""},
		{"Ctrl+K (raw 0x0b)", tcell.NewEventKey(tcell.KeyRune, 0x0b, 0), ActCommandBar, ""},
		{"Ctrl+X (Windows)", tcell.NewEventKey(tcell.KeyRune, 'x', tcell.ModCtrl), "sysreq", ""},
		{"Ctrl+R (Windows)", tcell.NewEventKey(tcell.KeyRune, 'r', tcell.ModCtrl), "reset", ""},
		{"Ctrl+E (Windows)", tcell.NewEventKey(tcell.KeyRune, 'e', tcell.ModCtrl), "eraseEof", ""},
		{"Ctrl+A (Windows)", tcell.NewEventKey(tcell.KeyRune, 'a', tcell.ModCtrl), "attn", ""},
		{"Ctrl+Q (Windows)", tcell.NewEventKey(tcell.KeyRune, 'q', tcell.ModCtrl), ActQuit, ""},
		{"Ctrl+Enter (Windows: Ctrl+M)", tcell.NewEventKey(tcell.KeyRune, 'm', tcell.ModCtrl), "fieldExit", ""},
		{"Ctrl+J (terminal)", tcell.NewEventKey(tcell.KeyCtrlJ, 0, tcell.ModCtrl), "fieldExit", ""},
		{"Ctrl+Backspace (Windows: DEL+Ctrl)", tcell.NewEventKey(tcell.KeyRune, 0x7f, tcell.ModCtrl), "eraseInput", ""},
		{"Ctrl+K (terminal)", tcell.NewEventKey(tcell.KeyCtrlSpace+tcell.Key(0x0b), 0, tcell.ModCtrl), ActCommandBar, ""},
		{"Enter (Windows)", tcell.NewEventKey(tcell.KeyRune, 0x0d, 0), "enter", ""},
		{"Tab (raw)", tcell.NewEventKey(tcell.KeyRune, 0x09, 0), "tab", ""},
		{"Esc (raw)", tcell.NewEventKey(tcell.KeyRune, 0x1b, 0), "attn", ""},
		{"Ctrl+Q", key(tcell.KeyCtrlQ, 0, 0), ActQuit, ""},
		{"Ctrl+Enter", key(tcell.KeyEnter, 0, tcell.ModCtrl), "fieldExit", ""},
		{"Shift+Enter", key(tcell.KeyEnter, 0, tcell.ModShift), "fieldExit", ""},
		{"Ctrl+J (LF)", key(tcell.KeyCtrlJ, 0, 0), "fieldExit", ""},
		{"letter", runeKey('a'), "", "a"},
		{"colon", runeKey(':'), "", ":"},
		{"umlaut", runeKey('ä'), "", "ä"},
		{"AltGr+q (Ctrl+Alt)", key(tcell.KeyRune, '@', tcell.ModCtrl|tcell.ModAlt), "", "@"},
	}
	for _, c := range cases {
		act, text, ok := KeyAction(c.ev)
		if !ok || act != c.act || text != c.text {
			t.Errorf("%s: got (%q,%q,%v); want (%q,%q)", c.name, act, text, ok, c.act, c.text)
		}
	}
	for name, ev := range map[string]*tcell.EventKey{
		"Alt+a":       key(tcell.KeyRune, 'a', tcell.ModAlt),
		"Ctrl+Home":   key(tcell.KeyHome, 0, tcell.ModCtrl),
		"Ctrl+F1":     key(tcell.KeyF1, 0, tcell.ModCtrl),
		"unknown key": key(tcell.KeyF40, 0, 0),
	} {
		if act, text, ok := KeyAction(ev); ok {
			t.Errorf("%s should be unbound, got (%q,%q)", name, act, text)
		}
	}
}

func TestEveryPFKeyBound(t *testing.T) {
	for n := 1; n <= 24; n++ {
		ev := key(tcell.KeyF1+tcell.Key(n-1), 0, 0)
		if act, _, ok := KeyAction(ev); !ok || act != fmt.Sprintf("pf%d", n) {
			t.Errorf("F%d -> %q", n, act)
		}
	}
	for _, b := range KeyTable {
		if strings.HasPrefix(b.Action, "app:") { // app pseudo actions (command bar, quit, tabs)
			continue
		}
		if _, ok := LookupAction(b.Action); !ok {
			t.Errorf("key table action %q is not an engine action", b.Action)
		}
	}
}

func TestLookupAction(t *testing.T) {
	if a, ok := LookupAction("FIELDEXIT"); !ok || a != "fieldExit" {
		t.Errorf("got %q %v", a, ok)
	}
	if _, ok := LookupAction("nope"); ok {
		t.Error("nope found")
	}
}

func TestHandleKeyReachesEngine(t *testing.T) {
	r := newRig(t, nil)
	r.connect(t, "PUB400.COM:23")
	e := r.engine()
	e.clear()
	r.a.handleKey(key(tcell.KeyF1, 0, 0))
	r.a.handleKey(key(tcell.KeyF1, 0, tcell.ModShift))
	r.a.handleKey(key(tcell.KeyEnter, 0, 0))
	r.a.handleKey(key(tcell.KeyTab, 0, 0))
	r.a.handleKey(key(tcell.KeyEscape, 0, 0))
	typeRunes(r.a, "ab")
	got := joinCalls(e.Calls())
	want := "press:pf1 press:pf13 press:enter press:tab press:attn type:a type:b"
	if got != want {
		t.Errorf("calls = %q; want %q", got, want)
	}
}

func TestColonOpensBarOutsideField(t *testing.T) {
	r := newRig(t, nil)
	r.connect(t, "PUB400.COM:23")
	e := r.engine()
	e.clear()
	// cursor in the user field: ':' is typed
	r.a.handleKey(runeKey(':'))
	if r.a.barOpen || joinCalls(e.Calls()) != "type::" {
		t.Fatalf("in field: bar=%v calls=%v", r.a.barOpen, e.Calls())
	}
	// cursor on a protected spot: ':' opens the bar
	e.mu.Lock()
	e.snap.Cursor.Row, e.snap.Cursor.Col = 0, 0
	e.mu.Unlock()
	e.clear()
	r.a.handleKey(runeKey(':'))
	if !r.a.barOpen || len(e.Calls()) != 0 {
		t.Fatalf("outside field: bar=%v calls=%v", r.a.barOpen, e.Calls())
	}
	// keys now belong to the bar
	if ev := r.a.handleKey(key(tcell.KeyF1, 0, 0)); ev == nil {
		t.Error("bar open: key must pass through to the input field")
	}
	r.a.closeCommandBar()
	// Ctrl+K opens it anywhere
	e.mu.Lock()
	e.snap.Cursor.Row, e.snap.Cursor.Col = 5, 40
	e.mu.Unlock()
	r.a.handleKey(key(tcell.KeyCtrlK, 0, 0))
	if !r.a.barOpen {
		t.Error("Ctrl+K did not open the bar")
	}
}

func TestBarRunsCommandOnEnter(t *testing.T) {
	r := newRig(t, nil)
	r.connect(t, "PUB400.COM:23")
	e := r.engine()
	e.clear()
	r.a.openCommandBar()
	r.a.bar.SetText("key pf3")
	r.a.barDone(tcell.KeyEnter)
	if r.a.barOpen || !hasCall(e.Calls(), "press:pf3") {
		t.Fatalf("bar=%v calls=%v", r.a.barOpen, e.Calls())
	}
	r.a.openCommandBar()
	r.a.bar.SetText("key pf4")
	r.a.barDone(tcell.KeyEscape)
	if r.a.barOpen || hasCall(e.Calls(), "press:pf4") {
		t.Fatalf("Esc must close without running: %v", e.Calls())
	}
}

func TestQuitPrompt(t *testing.T) {
	r := newRig(t, nil)
	r.a.handleKey(key(tcell.KeyCtrlQ, 0, 0))
	if r.a.topOverlay() != "quit" {
		t.Fatalf("overlay %q", r.a.topOverlay())
	}
	r.a.closeNamed("quit")
	if r.a.topOverlay() != "" {
		t.Fatal("quit prompt not closed")
	}
	r.a.handleKey(key(tcell.KeyCtrlC, 0, 0))
	if r.a.topOverlay() != "quit" {
		t.Fatal("Ctrl+C must ask first")
	}
}
