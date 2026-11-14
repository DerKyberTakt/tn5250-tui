package tn5250

import (
	"strings"

	"github.com/DerKyberTakt/tn5250-tui/internal/ebcdic"
)

// Field Format Word bits (SF order).
const (
	// FFWBypass marks a protected (bypass) field.
	FFWBypass = 0x2000
	// FFWDupEnable lets the Dup key work in the field.
	FFWDupEnable = 0x1000
	// FFWMDT is the initial modified data tag.
	FFWMDT = 0x0800
	// FFWShiftMask selects the data type bits.
	FFWShiftMask = 0x0700
	// FFWAutoEnter sends Enter when the field is filled.
	FFWAutoEnter = 0x0080
	// FFWFieldExitRequired requires Field Exit to leave the field.
	FFWFieldExitRequired = 0x0040
	// FFWMonocase upper-cases typed letters.
	FFWMonocase = 0x0020
	// FFWMandatoryEnter requires data in the field before Enter is accepted.
	FFWMandatoryEnter = 0x0008
	// FFWAdjustMask selects the right-adjust / fill bits.
	FFWAdjustMask = 0x0007
)

// Field data types (the shift bits of the FFW, FFW & FFWShiftMask >> 8).
const (
	shiftAlpha         = 0 // alpha shift
	shiftAlphaOnly     = 1
	shiftNumericShift  = 2
	shiftNumericOnly   = 3
	shiftKatakana      = 4
	shiftDigitsOnly    = 5
	shiftInputOutput   = 6 // I/O feature input, never from the keyboard
	shiftSignedNumeric = 7
)

// Field adjust modes.
const (
	adjustNone = iota
	adjustRightZero
	adjustRightBlank
	adjustMandatoryFill
)

// field is an input field from the format table. Addresses are 0-based buffer offsets.
type field struct {
	// start is the buffer offset of the first data position (the attribute sits at start-1).
	start  int
	length int
	ffw    uint16
	fcws   []uint16
	attr   byte
	mdt    bool
}

func newField(start, length int, ffw uint16, fcws []uint16, attr byte) *field {
	return &field{start: start, length: length, ffw: ffw, fcws: fcws, attr: attr, mdt: ffw&FFWMDT != 0}
}

// end is the exclusive end offset.
func (f *field) end() int { return f.start + f.length }

func (f *field) contains(addr int) bool { return addr >= f.start && addr < f.end() }

func (f *field) bypass() bool            { return f.ffw&FFWBypass != 0 }
func (f *field) dupEnable() bool         { return f.ffw&FFWDupEnable != 0 }
func (f *field) autoEnter() bool         { return f.ffw&FFWAutoEnter != 0 }
func (f *field) fieldExitRequired() bool { return f.ffw&FFWFieldExitRequired != 0 }
func (f *field) monocase() bool          { return f.ffw&FFWMonocase != 0 }
func (f *field) mandatoryEnter() bool    { return f.ffw&FFWMandatoryEnter != 0 }
func (f *field) shift() int              { return int(f.ffw&FFWShiftMask) >> 8 }
func (f *field) nondisplay() bool        { return f.attr&0x07 == 0x07 }
func (f *field) numeric() bool {
	s := f.shift()
	return s == shiftNumericOnly || s == shiftDigitsOnly || s == shiftSignedNumeric
}

func (f *field) adjust() int {
	switch f.ffw & FFWAdjustMask {
	case 5:
		return adjustRightZero
	case 6:
		return adjustRightBlank
	case 7:
		return adjustMandatoryFill
	}
	return adjustNone
}

// cursorProgression is the FCW X'88nn': the 1-based number of the field a forward exit goes to
// (0 = none).
func (f *field) cursorProgression() int {
	for _, w := range f.fcws {
		if w&0xff00 == 0x8800 && w&0xff != 0 {
			return int(w & 0xff)
		}
	}
	return 0
}

// screen is the regeneration buffer: one EBCDIC byte per cell. Attribute bytes (0x20..0x3F)
// live inline, exactly as the 5250 data stream places them.
type screen struct {
	rows, cols int
	buffer     []byte
	fields     []*field
	cursor     int
	// errorRow is the 0-based row used for error messages (Write Error Code).
	errorRow int
}

func newScreen() *screen {
	s := &screen{}
	s.setSize(24, 80)
	return s
}

func (s *screen) size() int { return s.rows * s.cols }

func (s *screen) setSize(rows, cols int) {
	s.rows, s.cols = rows, cols
	s.buffer = make([]byte, rows*cols)
	s.fields = nil
	s.cursor = 0
	s.errorRow = rows - 1
}

func (s *screen) clear() {
	for i := range s.buffer {
		s.buffer[i] = 0
	}
	s.fields = nil
	s.cursor = 0
}

// clearFormatTable starts a new format (SOH, Clear Format Table): the fields go.
func (s *screen) clearFormatTable() { s.fields = nil }

func (s *screen) addr(row, col int) int { return row*s.cols + col }
func (s *screen) rowOf(addr int) int    { return addr / s.cols }
func (s *screen) colOf(addr int) int    { return addr % s.cols }

// addrFromStream converts 1-based row/col operands from the data stream to a buffer offset.
func (s *screen) addrFromStream(row, col int) int {
	r := min(max(row, 1), s.rows) - 1
	c := min(max(col, 1), s.cols) - 1
	return s.addr(r, c)
}

// attributeAt is the attribute in effect at an address: the nearest attribute byte at or before it.
func (s *screen) attributeAt(addr int) byte {
	for i := addr; i >= 0; i-- {
		if isAttribute(s.buffer[i]) {
			return s.buffer[i]
		}
	}
	return AttrNormal
}

func (s *screen) fieldAt(addr int) *field {
	for _, f := range s.fields {
		if f.contains(addr) {
			return f
		}
	}
	return nil
}

// addField keeps fields in screen order; a redefinition at the same start replaces.
func (s *screen) addField(f *field) {
	for i, g := range s.fields {
		if g.start == f.start {
			s.fields[i] = f
			return
		}
	}
	pos := len(s.fields)
	for i, g := range s.fields {
		if g.start > f.start {
			pos = i
			break
		}
	}
	s.fields = append(s.fields, nil)
	copy(s.fields[pos+1:], s.fields[pos:])
	s.fields[pos] = f
}

// fieldBytes is the buffer window of a field (clamped to the buffer; it aliases the buffer).
func (s *screen) fieldBytes(f *field) []byte {
	start := min(max(f.start, 0), len(s.buffer))
	end := min(max(f.end(), start), len(s.buffer))
	return s.buffer[start:end]
}

// roll rolls rows top..bottom (0-based, inclusive) by n lines; positive n = up.
func (s *screen) roll(n, top, bottom int) {
	top = max(top, 0)
	bottom = min(bottom, s.rows-1)
	if n == 0 || top > bottom {
		return
	}
	lines := bottom - top + 1
	count := min(abs(n), lines)
	saved := append([]byte(nil), s.buffer[top*s.cols:(bottom+1)*s.cols]...)
	region := s.buffer[top*s.cols : (bottom+1)*s.cols]
	for i := range region {
		region[i] = 0
	}
	if n > 0 {
		copy(region, saved[count*s.cols:])
	} else {
		copy(region[count*s.cols:], saved[:(lines-count)*s.cols])
	}
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// rowText is the plain text of one row (nulls and attribute cells as blanks).
func (s *screen) rowText(row int, cp *ebcdic.CodePage) string {
	return cp.DecodeBytes(s.buffer[row*s.cols : (row+1)*s.cols])
}

// hiddenCells marks the cells of non-display fields (passwords).
func (s *screen) hiddenCells() []bool {
	hidden := make([]bool, len(s.buffer))
	for _, f := range s.fields {
		if !f.nondisplay() {
			continue
		}
		for i := max(f.start, 0); i < f.end() && i < len(hidden); i++ {
			hidden[i] = true
		}
	}
	return hidden
}

// Cell is one screen position of a Snapshot.
type Cell struct {
	// Ch is the character shown. Attribute bytes, null cells and the cells of non-display
	// fields are spaces.
	Ch rune
	// Attr is the 5250 attribute byte in effect at the cell (0x20..0x3f); decode it with
	// DecodeAttr. An attribute cell carries its own attribute.
	Attr byte
}

// FieldInfo describes one input or protected field of the format table.
type FieldInfo struct {
	// Start is the buffer offset (row*Cols+col) of the first data cell.
	Start int
	// Length is the number of data cells.
	Length int
	// Bypass is true for protected fields.
	Bypass bool
	// Nondisplay is true when the field hides its text (a password).
	Nondisplay bool
	// MDT is the modified data tag: the operator (or the host) changed the field.
	MDT bool
}

// Snapshot is the screen at one moment, safe to keep: it shares nothing with the engine.
type Snapshot struct {
	// Rows and Cols give the screen size (24x80 or 27x132).
	Rows, Cols int
	// Cells holds Rows*Cols cells in row-major order.
	Cells []Cell
	// Fields lists the format table in screen order.
	Fields []FieldInfo
	// Cursor is the 0-based cursor position.
	Cursor struct{ Row, Col int }
	// Seq increases on every change of the screen or status (agents poll on it).
	Seq uint64
}

// RowText is the text of one 0-based row (len == Cols runes), "" outside the screen.
func (s Snapshot) RowText(row int) string {
	if row < 0 || row >= s.Rows {
		return ""
	}
	var b strings.Builder
	for _, c := range s.Cells[row*s.Cols : (row+1)*s.Cols] {
		b.WriteRune(c.Ch)
	}
	return b.String()
}

// Text is every row joined with newlines.
func (s Snapshot) Text() string {
	rows := make([]string, s.Rows)
	for r := range rows {
		rows[r] = s.RowText(r)
	}
	return strings.Join(rows, "\n")
}

// snapshot builds the public view. Non-display fields (passwords) leave the engine as blanks.
func (s *screen) snapshot(cp *ebcdic.CodePage) Snapshot {
	hidden := s.hiddenCells()
	snap := Snapshot{Rows: s.rows, Cols: s.cols, Cells: make([]Cell, len(s.buffer))}
	attr := byte(AttrNormal)
	for i, b := range s.buffer {
		c := Cell{Ch: ' '}
		if isAttribute(b) {
			attr = b
		} else if b != 0 && !hidden[i] {
			c.Ch = cp.Decode(b)
		}
		c.Attr = attr
		snap.Cells[i] = c
	}
	snap.Fields = make([]FieldInfo, len(s.fields))
	for i, f := range s.fields {
		snap.Fields[i] = FieldInfo{Start: f.start, Length: f.length, Bypass: f.bypass(), Nondisplay: f.nondisplay(), MDT: f.mdt}
	}
	snap.Cursor.Row = s.rowOf(s.cursor)
	snap.Cursor.Col = s.colOf(s.cursor)
	return snap
}
