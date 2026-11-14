package tui

import (
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"github.com/rivo/uniseg"

	"github.com/DerKyberTakt/tn5250-tui/internal/highlight"
)

// The ACS palette on black.
var (
	colGreen     = tcell.NewRGBColor(0x00, 0xd0, 0x60)
	colWhite     = tcell.NewRGBColor(0xf0, 0xf0, 0xf0)
	colRed       = tcell.NewRGBColor(0xff, 0x50, 0x50)
	colTurquoise = tcell.NewRGBColor(0x20, 0xd0, 0xd0)
	colYellow    = tcell.NewRGBColor(0xf0, 0xe0, 0x40)
	colPink      = tcell.NewRGBColor(0xff, 0x70, 0xff)
	colBlue      = tcell.NewRGBColor(0x6a, 0x9c, 0xff)
	colBlack     = tcell.NewRGBColor(0, 0, 0)
	colDim       = tcell.NewRGBColor(0x50, 0x50, 0x50)
)

// PaletteColor maps an Attr colour name to its terminal colour.
func PaletteColor(name string) tcell.Color {
	switch name {
	case "white":
		return colWhite
	case "red":
		return colRed
	case "turquoise":
		return colTurquoise
	case "yellow":
		return colYellow
	case "pink":
		return colPink
	case "blue":
		return colBlue
	}
	return colGreen
}

// CellStyle is the terminal style for a 5250 attribute byte.
func CellStyle(attr byte) tcell.Style {
	a := DecodeAttr(attr)
	st := tcell.StyleDefault.Background(colBlack).Foreground(PaletteColor(a.Color))
	if a.Reverse {
		st = st.Reverse(true)
	}
	if a.Underline {
		st = st.Underline(true)
	}
	if a.Blink {
		st = st.Blink(true)
	}
	return st
}

// Screen is the primitive that draws the 5250 grid.
type Screen struct {
	*tview.Box
	source func() (Snapshot, bool)
	// Separators (off by default: the dots broke up turquoise labels) draws column separators as a dim dot on blank separator cells
	// when the whole screen fits (no horizontal scroll).
	Separators bool
	// Highlight colours the source lines of an SEU edit/browse screen (CL, DDS, RPG, SQL)
	// like the emulators do; the host's colours stay everywhere else.
	Highlight bool

	scrollX, scrollY int

	// The tokens of the last highlighted snapshot, keyed by its sequence number.
	hlSeq          uint64
	hlRows, hlCols int
	hlToks         []highlight.Tok
}

// tokenColours maps the highlighter's token kinds to the palette, as the emulators do.
var tokenColours = map[highlight.Tok]tcell.Color{
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

// tokens returns one token per cell when Highlight is on and the snapshot is an SEU
// screen, else nil. The result is cached per snapshot sequence (a sequence of 0 is
// recomputed every time).
func (s *Screen) tokens(snap Snapshot) []highlight.Tok {
	if !s.Highlight {
		s.hlToks = nil
		s.hlSeq = 0
		return nil
	}
	if snap.Seq != 0 && snap.Seq == s.hlSeq && snap.Rows == s.hlRows && snap.Cols == s.hlCols {
		return s.hlToks
	}
	rowText := func(r int) string {
		buf := make([]rune, snap.Cols)
		for c := 0; c < snap.Cols; c++ {
			ch := snap.At(r, c).Ch
			if ch < ' ' {
				ch = ' '
			}
			buf[c] = ch
		}
		return string(buf)
	}
	fields := make([]highlight.Field, len(snap.Fields))
	for i, f := range snap.Fields {
		fields[i] = highlight.Field{Start: f.Start, Length: f.Length, Bypass: f.Bypass}
	}
	toks, ok := highlight.ScreenFields(snap.Rows, snap.Cols, rowText, fields)
	if !ok {
		toks = nil
	}
	s.hlSeq, s.hlRows, s.hlCols, s.hlToks = snap.Seq, snap.Rows, snap.Cols, toks
	return toks
}

// NewScreen returns a screen primitive that reads its content from source;
// ok=false means "no session" and a hint is shown instead of a grid.
func NewScreen(source func() (Snapshot, bool)) *Screen {
	s := &Screen{Box: tview.NewBox(), source: source}
	s.SetBackgroundColor(colBlack)
	return s
}

// scroll keeps pos visible in a window of size win over total cells.
func scroll(cur, pos, win, total int) int {
	if win >= total {
		return 0
	}
	if pos < cur {
		cur = pos
	}
	if pos >= cur+win {
		cur = pos - win + 1
	}
	if cur > total-win {
		cur = total - win
	}
	if cur < 0 {
		cur = 0
	}
	return cur
}

// Draw implements tview.Primitive.
func (s *Screen) Draw(scr tcell.Screen) {
	s.Box.DrawForSubclass(scr, s)
	x, y, w, h := s.GetInnerRect()
	if w <= 0 || h <= 0 {
		return
	}
	snap, ok := s.source()
	if s.HasFocus() {
		scr.HideCursor()
	}
	if !ok || snap.Rows == 0 || snap.Cols == 0 {
		msg := "Not connected.  Ctrl+K then :sites or :connect host[:port]  (:help)"
		mw := uniseg.StringWidth(msg)
		off := 0
		if w > mw {
			off = (w - mw) / 2
		}
		putString(scr, x+off, y+h/2, w, msg, tcell.StyleDefault.Background(colBlack).Foreground(colDim))
		return
	}
	s.scrollX = scroll(s.scrollX, snap.Cursor.Col, w, snap.Cols)
	s.scrollY = scroll(s.scrollY, snap.Cursor.Row, h, snap.Rows)
	ox, oy := 0, 0 // screen origin inside the box
	if w > snap.Cols {
		ox = (w - snap.Cols) / 2
	}
	if h > snap.Rows {
		oy = (h - snap.Rows) / 2
	}
	fits := w >= snap.Cols
	toks := s.tokens(snap)
	for r := 0; r < snap.Rows; r++ {
		sy := y + oy + r - s.scrollY
		if sy < y || sy >= y+h {
			continue
		}
		for c := 0; c < snap.Cols; c++ {
			sx := x + ox + c - s.scrollX
			if sx < x || sx >= x+w {
				continue
			}
			cell := snap.At(r, c)
			a := DecodeAttr(cell.Attr)
			ch := cell.Ch
			if ch < ' ' || a.Nondisplay {
				ch = ' '
			}
			st := CellStyle(cell.Attr)
			// The attribute position is a plain blank, and a null outside any field shows
			// nothing at all: no underline, no reverse image (the 5250 rule the emulator
			// learned on day one; the sign-on screen's rows would otherwise be underlined
			// from the field's end to the right edge).
			if cell.Attribute || (cell.Null && !cell.InField) {
				st = tcell.StyleDefault.Background(colBlack).Foreground(colBlack)
			} else if a.Nondisplay {
				st = tcell.StyleDefault.Background(colBlack).Foreground(colBlack)
			} else if a.ColumnSeparators && s.Separators && fits && ch == ' ' && !a.Reverse {
				ch = '·'
				st = tcell.StyleDefault.Background(colBlack).Foreground(colDim)
			} else if toks != nil {
				if col, ok := tokenColours[toks[r*snap.Cols+c]]; ok {
					st = st.Foreground(col)
				}
			}
			scr.SetContent(sx, sy, ch, nil, st)
		}
	}
	if s.HasFocus() {
		cx := x + ox + snap.Cursor.Col - s.scrollX
		cy := y + oy + snap.Cursor.Row - s.scrollY
		if cx >= x && cx < x+w && cy >= y && cy < y+h {
			scr.ShowCursor(cx, cy)
		}
	}
}
