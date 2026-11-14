package tn5250

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/DerKyberTakt/tn5250-tui/internal/ebcdic"
)

// ESC is the byte that starts every display command.
const ESC = 0x04

// Display commands (the byte after ESC).
const (
	// CmdWTD is Write To Display.
	CmdWTD = 0x11
	// CmdClearUnit clears the unit and selects 24x80.
	CmdClearUnit = 0x40
	// CmdClearUnitAlt clears the unit and selects the size its parameter names.
	CmdClearUnitAlt = 0x20
	// CmdClearFormatTable clears the format table.
	CmdClearFormatTable = 0x50
	// CmdReadInputFields reads all input fields (flat format).
	CmdReadInputFields = 0x42
	// CmdReadMDTFields reads the modified fields.
	CmdReadMDTFields = 0x52
	// CmdReadMDTFieldsAlt reads the modified fields (alternate format).
	CmdReadMDTFieldsAlt = 0x82
	// CmdReadImmediate answers at once with all fields.
	CmdReadImmediate = 0x72
	// CmdReadImmediateAlt answers at once (alternate format).
	CmdReadImmediateAlt = 0x83
	// CmdReadScreen answers the screen image.
	CmdReadScreen = 0x62
	// CmdReadScreenExt answers the screen image, row by row, nulls trimmed.
	CmdReadScreenExt = 0x64
	// CmdReadScreenToPrint is Read Screen after the Print key (answered like Read Screen).
	CmdReadScreenToPrint = 0x66
	// CmdReadScreenExtToPrint is Read Screen Extended after the Print key.
	CmdReadScreenExtToPrint = 0x6e
	// CmdSaveScreen saves the screen image.
	CmdSaveScreen = 0x02
	// CmdSavePartialScreen saves the screen image for a window.
	CmdSavePartialScreen = 0x03
	// CmdRestoreScreen restores a saved image.
	CmdRestoreScreen = 0x12
	// CmdRestorePartialScreen restores a saved partial image.
	CmdRestorePartialScreen = 0x13
	// CmdRoll rolls a region of rows.
	CmdRoll = 0x23
	// CmdWriteErrorCode writes the error message on the error row.
	CmdWriteErrorCode = 0x21
	// CmdWriteErrorCodeWindow is Write Error Code with from/to columns.
	CmdWriteErrorCodeWindow = 0x22
	// CmdWSF is Write Structured Field.
	CmdWSF = 0xf3
	// CmdCopyToPrinter is ignored.
	CmdCopyToPrinter = 0x16
)

// Write To Display orders.
const (
	oSOH  = 0x01
	oRA   = 0x02
	oEA   = 0x03
	oTD   = 0x10
	oSBA  = 0x11
	oWEA  = 0x12
	oIC   = 0x13
	oMC   = 0x14
	oWDSF = 0x15
	oSF   = 0x1d
)

// response is a record the terminal must send back, before GDS framing.
type response struct {
	opcode byte
	data   []byte
	// secret lists the byte ranges of data that came from non-display fields.
	secret [][2]int
}

// processor executes host display commands against the screen buffer and terminal state. One
// instance per session; process handles the payload of one GDS record.
type processor struct {
	screen       *screen
	state        *terminalState
	terminalType string
	cp           *ebcdic.CodePage
	alarm        func()
	trace        func(string)

	// addr is the current write address for WTD orders.
	addr int
	// cursorSet: an IC/MC order positioned the cursor since the last read command.
	cursorSet bool
	// secretRanges are the byte ranges [start, end) of the last read response that came from
	// non-display fields (passwords): a trace must not keep them. Cleared by every response
	// builder.
	secretRanges [][2]int
}

func newProcessor(s *screen, st *terminalState, terminalType string, cp *ebcdic.CodePage, alarm func(), trace func(string)) *processor {
	return &processor{screen: s, state: st, terminalType: terminalType, cp: cp, alarm: alarm, trace: trace}
}

// reset puts the processor back to its connection-start state.
func (p *processor) reset() {
	p.addr = 0
	p.cursorSet = false
	p.secretRanges = nil
}

func (p *processor) tracef(line string) {
	if p.trace != nil {
		p.trace(line)
	}
}

// reader walks a command stream; reads past the end return zeros.
type reader struct {
	pos int
	buf []byte
}

func (r *reader) eof() bool      { return r.pos >= len(r.buf) }
func (r *reader) remaining() int { return max(0, len(r.buf)-r.pos) }
func (r *reader) peek() byte {
	if r.pos < len(r.buf) {
		return r.buf[r.pos]
	}
	return 0
}

func (r *reader) peekBytes(n int) []byte {
	start := min(r.pos, len(r.buf))
	return r.buf[start:min(len(r.buf), start+n)]
}

func (r *reader) u8() byte {
	b := r.peek()
	r.pos++
	return b
}

func (r *reader) u16() int {
	hi := int(r.u8())
	return hi<<8 | int(r.u8())
}

func (r *reader) bytes(n int) []byte {
	start := min(r.pos, len(r.buf))
	end := min(len(r.buf), r.pos+n)
	if end < start {
		end = start
	}
	r.pos += n
	return r.buf[start:end]
}

func (r *reader) skipToESC() {
	for !r.eof() && r.peek() != ESC {
		r.pos++
	}
}

// process executes the payload of one GDS record and returns the records to send back.
func (p *processor) process(data []byte) []response {
	r := &reader{buf: data}
	var out []response
	s := p.screen
	// Anything the host writes ends the keyboard's "past the end of the field" state.
	p.state.cursorPastEnd = false
	p.state.autoAdvanced = false
	for !r.eof() {
		if r.u8() != ESC {
			continue
		}
		if r.eof() {
			break
		}
		cmd := r.u8()
		if p.trace != nil {
			p.tracef("cmd " + strconv.FormatInt(int64(cmd), 16) + " @" + strconv.Itoa(r.pos))
		}
		switch cmd {
		case CmdWTD:
			cc1 := r.u8()
			cc2 := r.u8()
			p.applyCC1(cc1)
			// The host is repainting: an error line written by Write Error Code goes back to
			// what it covered (the host does not rewrite that row for a message it did not
			// write itself), and the cursor returns to where the operator was.
			p.restoreErrorLine()
			p.writeOrders(r)
			p.applyCC2(cc2)
		case CmdClearUnit:
			p.clearUnit(24, 80)
		case CmdClearUnitAlt:
			param := r.u8()
			if param == 0x00 || param == 0x31 {
				p.clearUnit(27, 132)
			} else {
				p.clearUnit(24, 80)
			}
		case CmdClearFormatTable:
			s.clearFormatTable()
		case CmdReadInputFields, CmdReadMDTFields, CmdReadMDTFieldsAlt:
			cc1 := r.u8()
			cc2 := r.u8()
			p.applyCC1(cc1)
			p.applyCC2(cc2)
			kind := readMDTAlt
			switch cmd {
			case CmdReadInputFields:
				kind = readInput
			case CmdReadMDTFields:
				kind = readMDT
			}
			p.state.pendingRead = kind
			p.state.inputInhibited = inhibitNone
			// Home unless an Insert Cursor placed the cursor. A cursor at the top-left corner
			// outside any input field counts as not placed: a restored image of a screen that
			// was written but never read carries IC 1,1, and the window it came back from must
			// not leave the operator there.
			at0 := s.fieldAt(0)
			// After a Write Error Code the operator stays where the error happened.
			afterError := p.state.savedCursor >= 0
			if !afterError && (!p.cursorSet || (s.cursor == 0 && !(at0 != nil && !at0.bypass()))) {
				p.homeCursor()
			}
			p.cursorSet = false
		case CmdReadImmediate:
			out = append(out, p.readReply(readInput, 0x00))
		case CmdReadImmediateAlt:
			out = append(out, p.readReply(readMDTAlt, 0x00))
		case CmdReadScreen, CmdReadScreenToPrint:
			// Without an answer the host keeps the keyboard locked (X SYSTEM) after the Print key.
			data := append([]byte(nil), s.buffer...)
			hidden := s.hiddenCells()
			out = append(out, response{opcode: OpNoop, data: data, secret: rangesOf(hidden)})
		case CmdReadScreenExt, CmdReadScreenExtToPrint:
			data, secret := p.readScreenExtended()
			out = append(out, response{opcode: OpNoop, data: data, secret: secret})
		case CmdSaveScreen:
			data, secret := p.saveScreen(CmdRestoreScreen)
			out = append(out, response{opcode: OpSaveScreen, data: data, secret: secret})
		case CmdSavePartialScreen:
			// flags, top row, left column, depth, width: the host wants the area a window will
			// cover. The whole screen comes back (tn5250 does the same); the restore then
			// repaints everything, which is a superset of what was asked for.
			r.bytes(5)
			data, secret := p.saveScreen(CmdRestorePartialScreen)
			out = append(out, response{opcode: OpSaveScreen, data: data, secret: secret})
		case CmdRestoreScreen:
			// The saved image that follows replaces everything, including the format table
			// left behind by whatever screen was shown meanwhile.
			s.clear()
			p.addr = 0
			p.state.savedCursor = -1
			p.state.savedErrorRow = nil
		case CmdRestorePartialScreen:
			// The image that follows is a full write of what the host saved.
		case CmdRoll:
			b := r.u8()
			top := int(r.u8())
			bottom := int(r.u8())
			n := int(b & 0x1f)
			if b&0x80 != 0 {
				n = -n
			}
			s.roll(n, top-1, bottom-1)
		case CmdWriteErrorCode:
			p.writeErrorCode(r, 1, s.cols)
		case CmdWriteErrorCodeWindow:
			// The message goes on the error row (the window's message line, from the SOH of
			// its Write to Display) between the given columns, inside the window's border.
			from := int(r.u8())
			to := int(r.u8())
			p.writeErrorCode(r, from, to)
		case CmdWSF:
			// One WSF carries one or more structured fields (LL, class, type, data), each
			// walked by its length: a X'04' inside one is data, not the next ESC.
			for r.remaining() >= 4 && r.peek() != ESC {
				ll := r.u16()
				cls := r.u8()
				typ := r.u8()
				if ll < 4 {
					break
				}
				r.bytes(min(ll-4, r.remaining()))
				if cls == 0xd9 && typ == 0x70 {
					out = append(out, response{opcode: OpNoop, data: p.queryReply()})
				}
			}
		default: // CmdCopyToPrinter and anything unknown
			r.skipToESC()
		}
	}
	return out
}

// rangesOf turns a mask into the [start, end) ranges of its true cells.
func rangesOf(mask []bool) [][2]int {
	var out [][2]int
	for i := 0; i < len(mask); {
		if !mask[i] {
			i++
			continue
		}
		j := i
		for j < len(mask) && mask[j] {
			j++
		}
		out = append(out, [2]int{i, j})
		i = j
	}
	return out
}

// takeSecretRanges returns the ranges of the last read response, handed over once.
func (p *processor) takeSecretRanges() [][2]int {
	r := p.secretRanges
	p.secretRanges = nil
	return r
}

// readReply builds a read response record for an immediate read.
func (p *processor) readReply(kind readKind, aid byte) response {
	data := p.readResponse(kind, aid)
	return response{opcode: OpPutGet, data: data, secret: p.takeSecretRanges()}
}

// aidOnly is the response of a command attention key: cursor and AID, no field data.
func (p *processor) aidOnly(aid byte) []byte {
	s := p.screen
	p.secretRanges = nil
	return []byte{byte(s.rowOf(s.cursor) + 1), byte(s.colOf(s.cursor) + 1), aid}
}

// readResponse builds the AID response for a read of the given kind: cursor row and column, the
// AID, then the fields. Read Input Fields returns every input field at its full length (nulls
// as blanks); the MDT reads return SBA + data for each modified field, trailing nulls (and for
// the plain MDT read, blanks) removed. The data of non-display fields goes into secretRanges.
func (p *processor) readResponse(kind readKind, aid byte) []byte {
	s := p.screen
	out := []byte{byte(s.rowOf(s.cursor) + 1), byte(s.colOf(s.cursor) + 1), aid}
	var secret [][2]int
	for _, f := range s.fields {
		if f.bypass() {
			continue
		}
		bytes := s.fieldBytes(f)
		nondisplay := f.nondisplay()
		if kind == readInput {
			if nondisplay {
				secret = append(secret, [2]int{len(out), len(out) + len(bytes)})
			}
			for _, b := range bytes {
				if b == 0 {
					b = 0x40
				}
				out = append(out, b)
			}
			continue
		}
		if !f.mdt {
			continue
		}
		end := len(bytes)
		for end > 0 && bytes[end-1] == 0 {
			end--
		}
		if kind == readMDT {
			for end > 0 && bytes[end-1] == 0x40 {
				end--
			}
		}
		out = append(out, oSBA, byte(s.rowOf(f.start)+1), byte(s.colOf(f.start)+1))
		if nondisplay {
			secret = append(secret, [2]int{len(out), len(out) + end})
		}
		for i := 0; i < end; i++ {
			b := bytes[i]
			if b == 0 && kind == readMDT {
				b = 0x40
			}
			out = append(out, b)
		}
	}
	p.secretRanges = secret
	return out
}

func (p *processor) clearUnit(rows, cols int) {
	p.screen.setSize(rows, cols)
	p.addr = 0
	p.cursorSet = false
	st := p.state
	st.inputInhibited = inhibitSystem
	st.insert = false
	st.errorMessage = ""
	st.savedCursor = -1
	st.savedErrorRow = nil
	st.pendingRead = readNone
	st.caKeys = 0
}

func (p *processor) put(b byte) {
	p.screen.buffer[p.addr] = b
	p.addr = (p.addr + 1) % p.screen.size()
}

func (p *processor) writeOrders(r *reader) {
	s := p.screen
	for !r.eof() && r.peek() != ESC {
		b := r.u8()
		if b < 0x20 && p.trace != nil {
			var sb strings.Builder
			for i, x := range r.peekBytes(6) {
				if i > 0 {
					sb.WriteByte(' ')
				}
				sb.WriteString(fmt.Sprintf("%02x", x))
			}
			p.tracef(fmt.Sprintf("  order %x at %d,%d next=%s", b, s.rowOf(p.addr)+1, s.colOf(p.addr)+1, sb.String()))
		}
		switch b {
		case oSBA:
			row, col := int(r.u8()), int(r.u8())
			p.addr = s.addrFromStream(row, col)
		case oIC, oMC:
			row, col := int(r.u8()), int(r.u8())
			s.cursor = s.addrFromStream(row, col)
			p.cursorSet = true
		case oRA:
			row, col := int(r.u8()), int(r.u8())
			end := s.addrFromStream(row, col)
			fill := r.u8()
			p.fillTo(end, fill)
		case oEA:
			row, col := int(r.u8()), int(r.u8())
			end := s.addrFromStream(row, col)
			n := int(r.u8())
			r.bytes(max(0, n-1))
			p.fillTo(end, 0x00)
		case oTD:
			n := r.u16()
			for _, d := range r.bytes(n) {
				p.put(d)
			}
		case oSOH:
			n := int(r.u8())
			data := r.bytes(n)
			s.clearFormatTable()
			// A new header starts a new format: an Insert Cursor received before it (for
			// instance the saved cursor replayed by Restore Screen) no longer places the
			// cursor, so the next read homes to the first input field unless this format
			// carries its own IC. That is what a menu redrawn after F3/F12 relies on.
			p.cursorSet = false
			if len(data) > 3 {
				if errRow := int(data[3]); errRow > 0 && errRow <= s.rows {
					s.errorRow = errRow - 1
				}
			}
			// Command key switches (bytes 4-6: F17-24, F9-16, F1-8; F1 = low bit of the last
			// byte; a set bit = the key returns no data, i.e. a DDS CA key). Absent = all CF.
			if len(data) >= 7 {
				p.state.caKeys = uint32(data[4])<<16 | uint32(data[5])<<8 | uint32(data[6])
			} else {
				p.state.caKeys = 0
			}
		case oSF:
			p.startField(r)
		case oWEA:
			r.bytes(2)
		case oWDSF:
			// Structured fields belong to the enhanced interface, which this terminal does not
			// announce: parse the length and skip the body.
			ll := r.u16()
			r.bytes(max(0, ll-2))
		default:
			p.put(b)
		}
	}
}

// homeCursor puts the cursor on the first input field.
func (p *processor) homeCursor() {
	s := p.screen
	for _, f := range s.fields {
		if !f.bypass() {
			s.cursor = f.start
			return
		}
	}
}

func (p *processor) fillTo(end int, fill byte) {
	size := p.screen.size()
	for guard := 0; guard < size; guard++ {
		here := p.addr
		p.put(fill)
		if here == end {
			break
		}
	}
}

func (p *processor) startField(r *reader) {
	var ffw uint16
	hasFFW := false
	var fcws []uint16
	if r.peek()&0xc0 == 0x40 {
		ffw = uint16(r.u16())
		hasFFW = true
		for r.peek()&0xc0 == 0x80 {
			fcws = append(fcws, uint16(r.u16()))
		}
	}
	attr := r.u8()
	n := r.u16()
	p.put(attr)
	if hasFFW {
		p.screen.addField(newField(p.addr, n, ffw, fcws, attr))
	}
}

func (p *processor) applyCC1(cc1 byte) {
	op := cc1 & 0xe0
	if op == 0 {
		return
	}
	p.state.inputInhibited = inhibitSystem
	fields := p.screen.fields
	resetMDT := func(all bool) {
		for _, f := range fields {
			if all || !f.bypass() {
				f.mdt = false
			}
		}
	}
	nullFields := func(mdtOnly bool) {
		for _, f := range fields {
			if f.bypass() {
				continue
			}
			if mdtOnly && !f.mdt {
				continue
			}
			b := p.screen.fieldBytes(f)
			for i := range b {
				b[i] = 0
			}
		}
	}
	switch op {
	case 0x40:
		resetMDT(false)
	case 0x60:
		resetMDT(true)
	case 0x80:
		nullFields(true)
	case 0xa0:
		resetMDT(false)
		nullFields(false)
	case 0xc0:
		// Null the modified fields, then reset the MDTs: the other order would find no
		// modified field left to null (the TypeScript engine had it that way round).
		nullFields(true)
		resetMDT(false)
	case 0xe0:
		resetMDT(true)
		nullFields(false)
	}
	// 0x20: lock only
}

func (p *processor) applyCC2(cc2 byte) {
	if cc2&0x08 != 0 {
		p.state.inputInhibited = inhibitNone
	}
	if cc2&0x04 != 0 && p.alarm != nil {
		p.alarm()
	}
	if cc2&0x02 != 0 {
		p.state.messageWaiting = true
	}
	if cc2&0x01 != 0 {
		p.state.messageWaiting = false
	}
}

// restoreErrorLine undoes a Write Error Code: the saved error row and cursor come back, the
// message goes.
func (p *processor) restoreErrorLine() {
	s := p.screen
	if p.state.savedErrorRow != nil {
		copy(s.buffer[s.errorRow*s.cols:], p.state.savedErrorRow)
		p.state.savedErrorRow = nil
		p.state.errorMessage = ""
	}
	if p.state.savedCursor >= 0 {
		s.cursor = p.state.savedCursor
		p.state.savedCursor = -1
	}
}

// writeErrorCode writes the error message on the error row, columns from to to (1-based,
// inclusive); the whole row is saved for the restore.
func (p *processor) writeErrorCode(r *reader, from, to int) {
	s := p.screen
	rowStart := s.errorRow * s.cols
	first := min(s.cols-1, max(0, from-1))
	end := min(s.cols, max(first+1, to))
	if p.state.savedCursor < 0 {
		p.state.savedCursor = s.cursor
		p.state.savedErrorRow = append([]byte(nil), s.buffer[rowStart:rowStart+s.cols]...)
	}
	for i := rowStart + first; i < rowStart+end; i++ {
		s.buffer[i] = 0
	}
	p.addr = rowStart + first
	p.writeOrders(r)
	p.state.errorMessage = strings.TrimSpace(s.rowText(s.errorRow, p.cp))
	p.state.inputInhibited = inhibitError
}

// readScreenExtended answers Read Screen Extended: every row with trailing nulls removed,
// each followed by X'FF'.
func (p *processor) readScreenExtended() ([]byte, [][2]int) {
	s := p.screen
	hidden := s.hiddenCells()
	var out []byte
	var mask []bool
	for row := 0; row < s.rows; row++ {
		end := (row + 1) * s.cols
		for end > row*s.cols && s.buffer[end-1] == 0 {
			end--
		}
		for i := row * s.cols; i < end; i++ {
			out = append(out, s.buffer[i])
			mask = append(mask, hidden[i])
		}
		out = append(out, 0xff)
		mask = append(mask, false)
	}
	return out, rangesOf(mask)
}

// saveScreen builds ESC RESTORE + a WTD that rebuilds the current screen, as the host expects
// for Save Screen. The second result lists the byte ranges that hold non-display field text.
func (p *processor) saveScreen(restore byte) ([]byte, [][2]int) {
	s := p.screen
	// Record the screen size too: the host may switch between 24x80 and 27x132 in between.
	// (A partial restore keeps the unit as it is.)
	var out []byte
	out = append(out, ESC, restore)
	if restore == CmdRestoreScreen {
		if s.cols == 132 {
			out = append(out, ESC, CmdClearUnitAlt, 0x00)
		} else {
			out = append(out, ESC, CmdClearUnit)
		}
	}
	// The header carries the error row and the command key switches of this screen.
	ca := p.state.caKeys
	out = append(out, ESC, CmdWTD, 0x00, 0x00,
		oSOH, 7, 0x00, 0x00, 0x00, byte(s.errorRow+1), byte(ca>>16), byte(ca>>8), byte(ca))
	out = append(out, oSBA, 1, 1)
	mask := make([]bool, len(out))
	cells, cellMask := p.dumpCells(0, s.size())
	out = append(out, cells...)
	mask = append(mask, cellMask...)
	out = append(out, oIC, byte(s.rowOf(s.cursor)+1), byte(s.colOf(s.cursor)+1))
	mask = append(mask, false, false, false)
	return out, rangesOf(mask)
}

// dumpCells returns the cells of [from, to) as WTD data, with a Start of Field order where a
// field begins, and a mask of the bytes that are non-display field text.
func (p *processor) dumpCells(from, to int) ([]byte, []bool) {
	s := p.screen
	byAttr := map[int]*field{}
	for _, f := range s.fields {
		byAttr[f.start-1] = f
	}
	hidden := s.hiddenCells()
	var out []byte
	var mask []bool
	add := func(secret bool, bs ...byte) {
		for _, b := range bs {
			out = append(out, b)
			mask = append(mask, secret)
		}
	}
	for i := from; i < to; i++ {
		b := s.buffer[i]
		if f, ok := byAttr[i]; ok && isAttribute(b) {
			add(false, oSF, byte(f.ffw>>8), byte(f.ffw))
			for _, w := range f.fcws {
				add(false, byte(w>>8), byte(w))
			}
			add(false, b, byte(f.length>>8), byte(f.length))
		} else {
			add(hidden[i], b)
		}
	}
	return out, mask
}

// queryReply is the Query reply for WSF class D9 type 70, as a plain (non-enhanced) display: 61
// bytes, with the screen size in the model (3477 FC = 27x132, 3179 02 = 24x80). The enhanced
// interface flags (byte 53 bit 6, byte 54 bit 0) stay off, so the host draws windows, menus and
// scroll bars as text.
func (p *processor) queryReply() []byte {
	wide := strings.HasPrefix(strings.ToUpper(p.terminalType), "IBM-3477")
	out := make([]byte, 61)
	copy(out, []byte{0x00, 0x00, 0x88, 0x00, 0x3a, 0xd9, 0x70, 0x80, 0x06, 0x00, 0x01, 0x01, 0x00})
	out[29] = 0x01
	machine, model := "3179", "02"
	if wide {
		machine, model = "3477", "FC"
	}
	copy(out[30:], p.cp.EncodeString(machine))
	out[34] = 0x00
	copy(out[35:], p.cp.EncodeString(model))
	out[37] = 0x02
	copy(out[40:], []byte{0x00, 0x61, 0x50, 0x00})
	out[44] = 0xff
	out[45] = 0xff
	out[49] = 0x7b
	if wide {
		out[50] = 0x31
	} else {
		out[50] = 0x11
	}
	return out
}
