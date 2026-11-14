package tui

import (
	"testing"

	"github.com/gdamore/tcell/v2"
)

func simScreen(t *testing.T, w, h int) tcell.SimulationScreen {
	t.Helper()
	s := tcell.NewSimulationScreen("UTF-8")
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	s.SetSize(w, h)
	t.Cleanup(s.Fini)
	return s
}

func cellAt(s tcell.SimulationScreen, x, y int) (rune, tcell.Color, tcell.AttrMask) {
	cells, w, _ := s.GetContents()
	c := cells[y*w+x]
	fg, _, attr := c.Style.Decompose()
	var r rune = ' '
	if len(c.Runes) > 0 {
		r = c.Runes[0]
	}
	return r, fg, attr
}

func TestScreenDrawsTextAndColours(t *testing.T) {
	snap := newSnap(24, 80)
	snap.put(2, 3, "HELLO", 0x20)  // green
	snap.put(3, 3, "ALERT", 0x29)  // red, reverse
	snap.put(4, 3, "UNDER", 0x36)  // yellow underline
	snap.put(5, 3, "BLINK", 0x2a)  // red blink
	snap.put(6, 3, "SECRET", 0x27) // non-display
	snap.put(7, 3, "WHITE", 0x22)
	snap.put(8, 3, "PINK", 0x38)
	snap.put(9, 3, "BLUE", 0x3a)
	snap.put(10, 3, "TURQ", 0x34)
	snap.Cursor.Row, snap.Cursor.Col = 2, 5
	sim := simScreen(t, 100, 30)
	sc := NewScreen(func() (Snapshot, bool) { return snap, true })
	sc.SetRect(0, 0, 100, 30)
	sc.Focus(nil)
	sc.Draw(sim)
	sim.Show()

	ox, oy := 10, 3 // centred: (100-80)/2, (30-24)/2
	check := func(row, col int, want rune, fg tcell.Color, attr tcell.AttrMask) {
		t.Helper()
		r, f, a := cellAt(sim, ox+col, oy+row)
		if r != want || f != fg || a&attr != attr {
			t.Errorf("cell %d,%d = %q fg %v attr %v; want %q fg %v attr %v", row, col, r, f, a, want, fg, attr)
		}
	}
	check(2, 3, 'H', colGreen, 0)
	check(2, 7, 'O', colGreen, 0)
	check(3, 3, 'A', colRed, tcell.AttrReverse)
	check(4, 3, 'U', colYellow, tcell.AttrUnderline)
	check(5, 3, 'B', colRed, tcell.AttrBlink)
	check(7, 3, 'W', colWhite, 0)
	check(8, 3, 'P', colPink, 0)
	check(9, 3, 'B', colBlue, 0)
	check(10, 3, 'T', colTurquoise, tcell.AttrUnderline)
	if r, _, _ := cellAt(sim, ox+3, oy+6); r != ' ' {
		t.Errorf("non-display text shown: %q", r)
	}
	if x, y, vis := sim.GetCursor(); !vis || x != ox+5 || y != oy+2 {
		t.Errorf("cursor = %d,%d visible=%v; want %d,%d", x, y, vis, ox+5, oy+2)
	}
}

func TestScreenNoCursorWithoutFocus(t *testing.T) {
	snap := newSnap(24, 80)
	sim := simScreen(t, 80, 24)
	sc := NewScreen(func() (Snapshot, bool) { return snap, true })
	sc.SetRect(0, 0, 80, 24)
	sc.Draw(sim)
	sim.Show()
	if _, _, vis := sim.GetCursor(); vis {
		t.Error("cursor visible although the screen has no focus")
	}
}

func TestScreenHorizontalScroll(t *testing.T) {
	snap := newSnap(27, 132)
	snap.put(0, 0, "LEFT", 0x20)
	snap.put(0, 125, "RIGHT", 0x22)
	snap.Cursor.Row, snap.Cursor.Col = 0, 128
	sim := simScreen(t, 80, 27)
	sc := NewScreen(func() (Snapshot, bool) { return snap, true })
	sc.SetRect(0, 0, 80, 27)
	sc.Focus(nil)
	sc.Draw(sim)
	sim.Show()
	// cursor col 128 must be visible: scrollX = 128-80+1 = 49 -> x = 79
	if x, y, vis := sim.GetCursor(); !vis || x != 79 || y != 0 {
		t.Fatalf("cursor %d,%d vis=%v", x, y, vis)
	}
	if r, _, _ := cellAt(sim, 125-49, 0); r != 'R' {
		t.Errorf("RIGHT not scrolled into view: %q", r)
	}
	// moving the cursor back to column 0 scrolls back
	snap.Cursor.Col = 0
	sc.Draw(sim)
	sim.Show()
	if r, _, _ := cellAt(sim, 0, 0); r != 'L' {
		t.Errorf("scroll did not return: %q", r)
	}
}

func TestScreenColumnSeparators(t *testing.T) {
	snap := newSnap(24, 80)
	snap.put(0, 0, "  ", 0x30) // turquoise column separators, blank
	snap.put(0, 2, "AB", 0x30) // text keeps its text
	sim := simScreen(t, 80, 24)
	sc := NewScreen(func() (Snapshot, bool) { return snap, true })
	sc.Separators = true // off by default; this test checks the dots themselves
	sc.SetRect(0, 0, 80, 24)
	sc.Draw(sim)
	sim.Show()
	if r, fg, _ := cellAt(sim, 0, 0); r != '·' || fg != colDim {
		t.Errorf("separator = %q %v", r, fg)
	}
	if r, fg, _ := cellAt(sim, 2, 0); r != 'A' || fg != colTurquoise {
		t.Errorf("text = %q %v", r, fg)
	}
	// no room (scrolling) -> no dots
	sim2 := simScreen(t, 40, 24)
	sc.SetRect(0, 0, 40, 24)
	sc.Draw(sim2)
	sim2.Show()
	if r, _, _ := cellAt(sim2, 0, 0); r == '·' {
		t.Error("separator drawn although the screen scrolls")
	}
	sc.Separators = false
	sc.SetRect(0, 0, 80, 24)
	sc.Draw(sim)
	sim.Show()
	if r, _, _ := cellAt(sim, 0, 0); r == '·' {
		t.Error("separator drawn although disabled")
	}
}

func TestScreenNotConnectedHint(t *testing.T) {
	sim := simScreen(t, 90, 10)
	sc := NewScreen(func() (Snapshot, bool) { return Snapshot{}, false })
	sc.SetRect(0, 0, 90, 10)
	sc.Draw(sim)
	sim.Show()
	cells, w, _ := sim.GetContents()
	row := ""
	for x := 0; x < w; x++ {
		c := cells[5*w+x]
		if len(c.Runes) > 0 {
			row += string(c.Runes[0])
		}
	}
	if !containsStr(row, "Not connected") {
		t.Errorf("hint missing: %q", row)
	}
}

func containsStr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestDecodeAttr(t *testing.T) {
	cases := []struct {
		b    byte
		want Attr
	}{
		{0x20, Attr{Color: "green"}},
		{0x21, Attr{Color: "green", Reverse: true}},
		{0x22, Attr{Color: "white"}},
		{0x24, Attr{Color: "green", Underline: true}},
		{0x26, Attr{Color: "white", Underline: true}},
		{0x27, Attr{Color: "white", Underline: true, Reverse: true, Nondisplay: true}},
		{0x28, Attr{Color: "red"}},
		{0x2a, Attr{Color: "red", Blink: true}},
		{0x2b, Attr{Color: "red", Blink: true, Reverse: true}},
		{0x30, Attr{Color: "turquoise", ColumnSeparators: true}},
		{0x31, Attr{Color: "turquoise", ColumnSeparators: true, Reverse: true}},
		{0x32, Attr{Color: "yellow"}},
		{0x34, Attr{Color: "turquoise", Underline: true}},
		{0x38, Attr{Color: "pink"}},
		{0x3a, Attr{Color: "blue"}},
		{0x3f, Attr{Color: "blue", Underline: true, Reverse: true, Nondisplay: true}},
	}
	for _, c := range cases {
		if got := DecodeAttr(c.b); got != c.want {
			t.Errorf("DecodeAttr(%#x) = %+v; want %+v", c.b, got, c.want)
		}
	}
}

func TestFormatOIA(t *testing.T) {
	l, r := FormatOIA(OIA{Connected: true, InputInhibited: "system", Insert: true, MessageWaiting: true, DeviceName: "QPADEV0001", Message: "Cursor in protected area"}, 4, 9, "")
	want := "Connected  X SYSTEM  INS  MW  QPADEV0001  Cursor in protected area"
	if l != want || r != "5,10" {
		t.Errorf("got %q | %q", l, r)
	}
	l, _ = FormatOIA(OIA{InputInhibited: "error", Message: "x"}, 0, 0, "unknown command: foo")
	if l != "Disconnected  X II  unknown command: foo" {
		t.Errorf("got %q", l)
	}
	l, _ = FormatOIA(OIA{Connected: true, InputInhibited: "none"}, 0, 0, "")
	if l != "Connected" {
		t.Errorf("got %q", l)
	}
}

func TestOIABarDraws(t *testing.T) {
	sim := simScreen(t, 60, 3)
	b := NewOIABar(func() (string, string, bool) { return "Connected  INS", "12,34", false })
	b.SetRect(0, 2, 60, 1)
	b.Draw(sim)
	sim.Show()
	cells, w, _ := sim.GetContents()
	line := ""
	for x := 0; x < w; x++ {
		line += string(cells[2*w+x].Runes)
	}
	if !containsStr(line, "Connected  INS") || !containsStr(line, "12,34") {
		t.Errorf("line %q", line)
	}
}

// TestScreenNullCellsOutsideFields: the 5250 rule from the emulator's first day. An underline
// attribute runs to the next attribute byte, but a null cell outside any field shows nothing,
// and the attribute position itself is a plain blank; nulls inside a field keep the underline.
func TestScreenNullCellsOutsideFields(t *testing.T) {
	snap := newSnap(24, 80)
	// col 0: attribute 0x24 (green underline); cols 1-5: a field of nulls; col 6: attribute 0x24
	// again; cols 7..: nulls outside any field (the sign-on screen's long lines).
	for c := 0; c < 80; c++ {
		snap.Cells[c] = Cell{Ch: ' ', Attr: 0x24, Null: true}
	}
	snap.Cells[0].Attribute, snap.Cells[0].Null = true, false
	snap.Cells[6].Attribute, snap.Cells[6].Null = true, false
	for c := 1; c <= 5; c++ {
		snap.Cells[c].InField = true
	}
	snap.Fields = []FieldInfo{{Start: 1, Length: 5}}
	sim := simScreen(t, 80, 24)
	sc := NewScreen(func() (Snapshot, bool) { return snap, true })
	sc.SetRect(0, 0, 80, 24)
	sc.Draw(sim)
	sim.Show()
	underlined := func(x int) bool {
		cells, w, _ := sim.GetContents()
		_, _, attr := cells[x+0*w].Style.Decompose()
		return attr&tcell.AttrUnderline != 0
	}
	if underlined(0) {
		t.Error("the attribute position must not be underlined")
	}
	if !underlined(1) || !underlined(5) {
		t.Error("null cells inside the field must be underlined")
	}
	if underlined(7) || underlined(79) {
		t.Error("null cells outside any field must not be underlined")
	}
}
