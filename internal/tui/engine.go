// Package tui is the console front end: a tview application with a custom
// primitive for the 5250 screen, an OIA status line, a command bar, a site
// manager and a login form. It talks to the 5250 engine through the small
// Engine interface so it builds and tests without the engine package.
package tui

// Engine is the part of the 5250 engine contract the terminal needs. The
// real engine is adapted to it in cmd/. All calls are made while holding the
// App mutex, so an implementation need not be goroutine safe.
type Engine interface {
	// Feed delivers bytes read from the socket.
	Feed(b []byte)
	// Press performs a key action (see Actions).
	Press(action string)
	// Type enters text into the field at the cursor.
	Type(text string)
	// SetCursor moves the cursor (0-based row and column).
	SetCursor(row, col int)
	// Snapshot returns a copy of the screen state.
	Snapshot() Snapshot
	// OIA returns the operator information area for the connection state.
	OIA(connected bool) OIA
	// Reset puts the engine back to its start state (after a disconnect).
	Reset()
}

// PcCommand is a PC command the host asked the terminal to run (STRPCCMD / PC Organizer).
type PcCommand struct {
	Command string
	// Wait is true for PAUSE(*YES).
	Wait bool
}

// PcCommandSource is implemented by an engine that recognises STRPCCMD requests. The App
// checks for it with a type assertion, so an engine without it simply never sees one.
type PcCommandSource interface {
	// PcCommand reports the PC command on the current screen, when the host waits for a read.
	PcCommand() (PcCommand, bool)
}

// SignonProbe describes the current screen for the code page hint after a refused sign-on.
type SignonProbe struct {
	// Screen is true when the screen is a sign-on screen.
	Screen bool
	// VariantChars lists the characters of the password field that differ between code
	// pages. It never holds the password.
	VariantChars []string
	// CodePage is the CCSID in use.
	CodePage int
}

// SignonProber is implemented by an engine that can probe a sign-on screen (type assertion,
// as PcCommandSource).
type SignonProber interface {
	SignonProbe() SignonProbe
}

// Cell is one screen position. Attr is the 5250 attribute byte in effect
// (0x20..0x3f; decode it with DecodeAttr).
type Cell struct {
	Ch   rune
	Attr byte
	// Attribute marks the attribute byte's own position (shown as a plain blank).
	Attribute bool
	// Null marks a null cell; outside a field it shows nothing, not even an underline.
	Null bool
	// InField marks a data cell of a field.
	InField bool
}

// FieldInfo describes one input or bypass field. Start is the offset
// (row*Cols+col) of the first data position of the field.
type FieldInfo struct {
	Start      int
	Length     int
	Bypass     bool
	Nondisplay bool
	MDT        bool
}

// Snapshot is a copy of the screen. Cells holds Rows*Cols entries.
type Snapshot struct {
	Rows, Cols int
	Cells      []Cell
	Fields     []FieldInfo
	Cursor     struct{ Row, Col int }
	Seq        uint64
}

// OIA is the operator information area.
type OIA struct {
	Connected      bool
	InputInhibited string // "none", "system" or "error"
	Insert         bool
	MessageWaiting bool
	DeviceName     string
	Message        string
	QueuedKeys     int
}

// Attr is a decoded 5250 attribute byte.
type Attr struct {
	Color            string // green, white, red, turquoise, yellow, pink, blue
	Reverse          bool
	Underline        bool
	Blink            bool
	ColumnSeparators bool
	Nondisplay       bool
}

// DecodeAttr decodes a 5250 field attribute byte: 0x01 reverse image, 0x02
// colour variant, 0x04 underline, 0x18 colour group (0 green/white, 1 red
// (blinking for the variant), 2 turquoise/yellow, 3 pink/blue); all three
// low bits set means non-display.
func DecodeAttr(b byte) Attr {
	a := Attr{
		Reverse:   b&0x01 != 0,
		Underline: b&0x04 != 0,
	}
	variant := b&0x02 != 0
	switch (b >> 3) & 0x03 {
	case 0:
		a.Color = "green"
		if variant {
			a.Color = "white"
		}
	case 1:
		a.Color = "red"
		a.Blink = variant
	case 2:
		a.Color = "turquoise"
		if variant {
			a.Color = "yellow"
		} else if !a.Underline {
			a.ColumnSeparators = true // 0x30/0x31 are the column-separator attributes
		}
	case 3:
		a.Color = "pink"
		if variant {
			a.Color = "blue"
		}
	}
	a.Nondisplay = b&0x07 == 0x07
	return a
}

// At returns the cell at row, col (0-based), or a blank cell outside the grid.
func (s *Snapshot) At(row, col int) Cell {
	if row < 0 || col < 0 || row >= s.Rows || col >= s.Cols || row*s.Cols+col >= len(s.Cells) {
		return Cell{Ch: ' '}
	}
	return s.Cells[row*s.Cols+col]
}

// InputFields returns the fields that accept input (not bypass), in order.
func (s *Snapshot) InputFields() []FieldInfo {
	var out []FieldInfo
	for _, f := range s.Fields {
		if !f.Bypass {
			out = append(out, f)
		}
	}
	return out
}

// CursorInField reports whether the cursor sits inside an input field.
func (s *Snapshot) CursorInField() bool {
	off := s.Cursor.Row*s.Cols + s.Cursor.Col
	for _, f := range s.Fields {
		if !f.Bypass && off >= f.Start && off < f.Start+f.Length {
			return true
		}
	}
	return false
}

// Text returns the screen as text, rows joined by newlines, trailing blanks
// trimmed. Non-display fields are already blank in the snapshot.
func (s *Snapshot) Text() string {
	var out []byte
	for r := 0; r < s.Rows; r++ {
		row := make([]rune, s.Cols)
		for c := 0; c < s.Cols; c++ {
			ch := s.At(r, c).Ch
			if ch < ' ' {
				ch = ' '
			}
			row[c] = ch
		}
		end := len(row)
		for end > 0 && row[end-1] == ' ' {
			end--
		}
		out = append(out, []byte(string(row[:end]))...)
		if r < s.Rows-1 {
			out = append(out, '\n')
		}
	}
	return string(out)
}
