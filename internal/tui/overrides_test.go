package tui

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/DerKyberTakt/tn5250-tui/internal/sites"
)

func TestApplyColors(t *testing.T) {
	t.Cleanup(ResetColors)
	skipped := ApplyColors(map[string]string{"green": "#112233", "blue": "nope", "orange": "#000000"})
	if len(skipped) != 2 {
		t.Errorf("skipped %v", skipped)
	}
	want := tcell.NewRGBColor(0x11, 0x22, 0x33)
	if fg, _, _ := CellStyle(0x20).Decompose(); fg != want {
		t.Errorf("green: %v", fg)
	}
	if tokenColours[0] == want || PaletteColor("blue") != builtinPalette["blue"] {
		t.Errorf("blue must stay built-in: %v", PaletteColor("blue"))
	}
	ResetColors()
	if fg, _, _ := CellStyle(0x20).Decompose(); fg != builtinPalette["green"] {
		t.Errorf("reset: %v", fg)
	}
}

func TestParseKeyName(t *testing.T) {
	cases := []struct {
		name string
		want []KeyRef
	}{
		{"Ctrl+Home", []KeyRef{{tcell.KeyHome, tcell.ModCtrl}}},
		{"Alt+F1", []KeyRef{{tcell.KeyF1, tcell.ModAlt}}},
		{"Shift+F12", []KeyRef{{tcell.KeyF12, tcell.ModShift}}},
		{"F24", []KeyRef{{tcell.KeyF24, 0}}},
		{"Shift+Escape", []KeyRef{{tcell.KeyEscape, tcell.ModShift}}},
		{"Ctrl+K", []KeyRef{{tcell.KeyCtrlK, 0}}},
		{"Ctrl+Enter", []KeyRef{{tcell.KeyEnter, tcell.ModCtrl}, {tcell.KeyCtrlM, 0}}},
		{"Shift+Tab", []KeyRef{{tcell.KeyBacktab, 0}, {tcell.KeyTab, tcell.ModShift}}},
		{"Backspace", []KeyRef{{tcell.KeyBackspace, 0}, {tcell.KeyBackspace2, 0}}},
		{"ArrowLeft", []KeyRef{{tcell.KeyLeft, 0}}},
		{"PageDown", []KeyRef{{tcell.KeyPgDn, 0}}},
		{"Alt+A", nil}, {"A", nil}, {"Ctrl+Shift+A", nil}, {"Meta+C", nil}, {"Numpad1", nil},
		{"Space", nil}, {"F25", nil}, {"Ctrl+Pause", nil}, {"", nil},
	}
	for _, c := range cases {
		got, ok := ParseKeyName(c.name)
		if (c.want == nil) == ok {
			t.Errorf("%q: ok=%v got %v", c.name, ok, got)
			continue
		}
		if len(got) != len(c.want) {
			t.Errorf("%q: %v want %v", c.name, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%q: %v want %v", c.name, got, c.want)
			}
		}
	}
}

func TestApplyKeyOverrides(t *testing.T) {
	t.Cleanup(ResetKeyTable)
	skipped := ApplyKeyOverrides(map[string]string{
		"Ctrl+K":   "attn",     // rebinds the command bar key
		"Escape":   "",         // unbinds Attention on Esc
		"Alt+F1":   "help",     // new chord
		"Ctrl+End": "eraseEof", // new chord
		"Alt+A":    "attn",     // not deliverable
		"Ctrl+Z":   "bogus",    // unknown action
	})
	if len(skipped) != 2 || !strings.Contains(skipped[0]+skipped[1], "Alt+A") || !strings.Contains(skipped[0]+skipped[1], "bogus") {
		t.Errorf("skipped %v", skipped)
	}
	check := func(ev *tcell.EventKey, want string) {
		t.Helper()
		a, _, ok := KeyAction(ev)
		if want == "" {
			if ok {
				t.Errorf("%v: bound to %q, want unbound", ev.Name(), a)
			}
			return
		}
		if !ok || a != want {
			t.Errorf("%v: %q ok=%v want %q", ev.Name(), a, ok, want)
		}
	}
	check(tcell.NewEventKey(tcell.KeyRune, 'k', tcell.ModCtrl), "attn") // Windows console form
	check(tcell.NewEventKey(tcell.KeyCtrlK, 0, tcell.ModCtrl), "attn")  // terminal form
	check(tcell.NewEventKey(tcell.KeyEscape, 0, 0), "")
	check(tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModShift), "sysreq") // untouched
	check(tcell.NewEventKey(tcell.KeyF1, 0, tcell.ModAlt), "help")
	check(tcell.NewEventKey(tcell.KeyEnd, 0, tcell.ModCtrl), "eraseEof")
	check(tcell.NewEventKey(tcell.KeyEnd, 0, 0), "endOfField")
	ResetKeyTable()
	check(tcell.NewEventKey(tcell.KeyRune, 'k', tcell.ModCtrl), ActCommandBar)
	check(tcell.NewEventKey(tcell.KeyEscape, 0, 0), "attn")
	// Applying again starts from the built-in table (no leftovers from the first call).
	ApplyKeyOverrides(map[string]string{"F1": "pf2"})
	check(tcell.NewEventKey(tcell.KeyF1, 0, tcell.ModAlt), "") // the earlier Alt+F1 binding is gone
	check(tcell.NewEventKey(tcell.KeyF1, 0, 0), "pf2")
}

func TestScreenWatermarkAndDemo(t *testing.T) {
	snap := newSnap(24, 80)
	snap.put(12, 0, "TEXT", 0x20) // host text at the watermark's row start
	snap.Fields = append(snap.Fields, FieldInfo{Start: 12*80 + 40, Length: 5})
	for c := 40; c < 45; c++ {
		snap.Cells[12*80+c].InField = true
	}
	sim := simScreen(t, 80, 24)
	sc := NewScreen(func() (Snapshot, bool) { return snap, true })
	sc.SetRect(0, 0, 80, 24)
	sc.Watermark, sc.WatermarkPos = "WATERMARK", "center"
	sc.Draw(sim)
	sim.Show()
	// Centred: 9 runes at (80-9)/2 = 35 .. 43; columns 40-43 are a field and stay blank.
	if r, fg, _ := cellAt(sim, 35, 12); r != 'W' || fg != colDim {
		t.Errorf("watermark start: %q %v", r, fg)
	}
	if r, _, _ := cellAt(sim, 40, 12); r != ' ' {
		t.Errorf("field cell overwritten: %q", r)
	}
	if r, _, _ := cellAt(sim, 0, 12); r != 'T' {
		t.Errorf("host text overwritten: %q", r)
	}
	sc.Watermark = ""
	sc.DemoLabel = "DEMO"
	sc.Draw(sim)
	sim.Show()
	// Tiled from row 2 column 0.
	if r, fg, _ := cellAt(sim, 0, 2); r != 'D' || fg != colDemo {
		t.Errorf("demo tile: %q %v", r, fg)
	}
	if r, _, _ := cellAt(sim, 35, 12); r != ' ' {
		t.Errorf("old watermark still drawn: %q", r)
	}
	sc.DemoLabel = ""
	sc.Draw(sim)
	sim.Show()
	if r, _, _ := cellAt(sim, 0, 2); r != ' ' {
		t.Errorf("demo label not cleared: %q", r)
	}
}

func TestApplyDefaultsAndDescribe(t *testing.T) {
	t.Cleanup(ResetColors)
	t.Cleanup(ResetKeyTable)
	f := &sites.File{Defaults: &sites.Defaults{SourceHighlight: true,
		Colors: map[string]string{"red": "#010203"}, Keys: map[string]string{"Alt+F1": "help"},
		Watermark: &sites.Watermark{Text: "HELLO", Position: "topLeft"}}}
	a := New(AppOptions{Sites: f})
	if a.screen.Watermark != "HELLO" || a.screen.WatermarkPos != "topLeft" {
		t.Errorf("watermark not applied: %+v", a.screen)
	}
	if PaletteColor("red") != tcell.NewRGBColor(1, 2, 3) {
		t.Errorf("colour not applied")
	}
	if act, _, ok := KeyAction(tcell.NewEventKey(tcell.KeyF1, 0, tcell.ModAlt)); !ok || act != "help" {
		t.Errorf("key not applied: %q", act)
	}
	if s := DescribeOverrides(f.EffectiveDefaults()); s != "1 colours, 1 keys, watermark (from :import)" {
		t.Errorf("describe: %q", s)
	}
	if s := DescribeOverrides(sites.Defaults{}); !strings.HasPrefix(s, "none") {
		t.Errorf("describe none: %q", s)
	}
	if s := demoLabel(sites.Site{Demo: true}); s != "DEMO" {
		t.Errorf("demo label: %q", s)
	}
	if s := demoLabel(sites.Site{Demo: true, DemoLabel: " TEST "}); s != "TEST" {
		t.Errorf("demo label: %q", s)
	}
}
