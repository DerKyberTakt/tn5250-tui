package tn5250

import (
	"strconv"
	"strings"
	"unicode"

	"github.com/DerKyberTakt/tn5250-tui/internal/ebcdic"
)

// Action is a key action the keyboard understands.
type Action string

// The key actions of Session.Press.
const (
	ActionEnter            Action = "enter"            // ActionEnter: Enter (AID X'F1')
	ActionFieldExit        Action = "fieldExit"        // ActionFieldExit: Field Exit
	ActionFieldPlus        Action = "fieldPlus"        // ActionFieldPlus: Field +
	ActionFieldMinus       Action = "fieldMinus"       // ActionFieldMinus: Field - (numeric fields)
	ActionTab              Action = "tab"              // ActionTab: Tab to the next field
	ActionBacktab          Action = "backtab"          // ActionBacktab: Tab to the previous field (or the start of this one)
	ActionLeft             Action = "left"             // ActionLeft: cursor left
	ActionRight            Action = "right"            // ActionRight: cursor right
	ActionUp               Action = "up"               // ActionUp: cursor up
	ActionDown             Action = "down"             // ActionDown: cursor down
	ActionHome             Action = "home"             // ActionHome: cursor to the first input field
	ActionNewline          Action = "newline"          // ActionNewline: cursor to the start of the next row (or the next field)
	ActionBackspace        Action = "backspace"        // ActionBackspace: delete the character before the cursor
	ActionDelete           Action = "delete"           // ActionDelete: delete the character under the cursor
	ActionInsert           Action = "insert"           // ActionInsert: toggle insert mode
	ActionEndOfField       Action = "endOfField"       // ActionEndOfField: cursor after the last typed character
	ActionEraseEOF         Action = "eraseEof"         // ActionEraseEOF: erase to the end of the field
	ActionEraseInput       Action = "eraseInput"       // ActionEraseInput: erase every input field
	ActionDup              Action = "dup"              // ActionDup: Dup character
	ActionFieldMark        Action = "fieldMark"        // ActionFieldMark: Field Mark character
	ActionReset            Action = "reset"            // ActionReset: clear an operator error, leave insert mode
	ActionAttn             Action = "attn"             // ActionAttn: Attention (out of band)
	ActionSysReq           Action = "sysreq"           // ActionSysReq: System Request (out of band)
	ActionClear            Action = "clear"            // ActionClear: Clear (AID)
	ActionHelp             Action = "help"             // ActionHelp: Help (AID)
	ActionPageUp           Action = "pageUp"           // ActionPageUp: Roll Down / Page Up (AID)
	ActionPageDown         Action = "pageDown"         // ActionPageDown: Roll Up / Page Down (AID)
	ActionPrint            Action = "print"            // ActionPrint: Print (AID)
	ActionRecordBackspace  Action = "recordBackspace"  // ActionRecordBackspace: Record Backspace (AID)
	ActionTestRequest      Action = "testRequest"      // ActionTestRequest: Test Request (out of band)
	ActionFastUp           Action = "fastUp"           // ActionFastUp: cursor three rows up
	ActionFastDown         Action = "fastDown"         // ActionFastDown: cursor three rows down
	ActionFastLeft         Action = "fastLeft"         // ActionFastLeft: cursor three cells left
	ActionFastRight        Action = "fastRight"        // ActionFastRight: cursor three cells right
	ActionWordLeft         Action = "wordLeft"         // ActionWordLeft: cursor to the previous word start
	ActionWordRight        Action = "wordRight"        // ActionWordRight: cursor to the next word start
	ActionBeginningOfField Action = "beginningOfField" // ActionBeginningOfField: cursor to the start of the field
	ActionDeleteWord       Action = "deleteWord"       // ActionDeleteWord: delete the word under the cursor
	ActionEraseField       Action = "eraseField"       // ActionEraseField: erase the whole field
)

// PF returns the action of function key n (1-24), "" for any other n.
func PF(n int) Action {
	if n < 1 || n > 24 {
		return ""
	}
	return Action("pf" + strconv.Itoa(n))
}

// Attention identifiers (AIDs) sent with a read response.
const (
	// AIDEnter is the Enter key.
	AIDEnter = 0xf1
	// AIDClear is the Clear key.
	AIDClear = 0xbd
	// AIDHelp is the Help key.
	AIDHelp = 0xf3
	// AIDPageUp is Roll Down (Page Up).
	AIDPageUp = 0xf4
	// AIDPageDown is Roll Up (Page Down).
	AIDPageDown = 0xf5
	// AIDPrint is the Print key.
	AIDPrint = 0xf6
	// AIDRecordBackspace is Record Backspace.
	AIDRecordBackspace = 0xf8
	// AIDAutoEnter is the Enter an Auto Enter field sends when it is filled.
	AIDAutoEnter = 0x3f
	// AIDPF1 is F1; F2..F12 follow (0x31..0x3c).
	AIDPF1 = 0x31
	// AIDPF13 is F13; F14..F24 follow (0xb1..0xbc).
	AIDPF13 = 0xb1
)

// AIDOf returns the AID byte an action sends, false for actions that send none.
func AIDOf(a Action) (byte, bool) {
	switch a {
	case ActionEnter:
		return AIDEnter, true
	case ActionClear:
		return AIDClear, true
	case ActionHelp:
		return AIDHelp, true
	case ActionPageUp:
		return AIDPageUp, true
	case ActionPageDown:
		return AIDPageDown, true
	case ActionPrint:
		return AIDPrint, true
	case ActionRecordBackspace:
		return AIDRecordBackspace, true
	}
	if strings.HasPrefix(string(a), "pf") {
		n, err := strconv.Atoi(string(a[2:]))
		if err != nil || n < 1 || n > 24 || PF(n) != a {
			return 0, false
		}
		if n <= 12 {
			return byte(AIDPF1 + n - 1), true
		}
		return byte(AIDPF13 + n - 13), true
	}
	return 0, false
}

// pfNumber is the F-key number of an AID (1-24), 0 for other AIDs.
func pfNumber(aid byte) int {
	if aid >= 0x31 && aid <= 0x3c {
		return int(aid) - 0x30
	}
	if aid >= 0xb1 && aid <= 0xbc {
		return int(aid) - 0xb0 + 12
	}
	return 0
}

// OperatorErrors maps the operator error codes 5250 displays show to their texts.
var OperatorErrors = map[string]string{
	"0004": "Entry of data not allowed in this input/output field",
	"0005": "Cursor in protected area of display",
	"0007": "Mandatory enter field requires data",
	"0008": "Field requires alphabetic characters",
	"0009": "Field requires numeric characters",
	"0010": "Only digits allowed in this field",
	"0011": "Key not valid in signed numeric field",
	"0012": "No room to insert data",
	"0014": "Mandatory fill field must be filled",
	"0016": "Field minus not valid in this field",
	"0019": "Dup or Field Mark not allowed in this field",
	"0020": "Field exit required to leave this field",
	"0026": "Field minus requires a numeric last digit",
}

// isOutOfBand: keys the host accepts while the keyboard is locked; they interrupt whatever the
// host is doing.
func isOutOfBand(a Action) bool {
	return a == ActionAttn || a == ActionSysReq || a == ActionTestRequest
}

func isMovement(a Action) bool {
	switch a {
	case ActionTab, ActionBacktab, ActionLeft, ActionRight, ActionUp, ActionDown, ActionHome,
		ActionNewline, ActionEndOfField, ActionFastUp, ActionFastDown, ActionFastLeft,
		ActionFastRight, ActionWordLeft, ActionWordRight, ActionBeginningOfField:
		return true
	}
	return false
}

// fastStep is the number of cells a fast cursor key moves per press (ACS "fast cursor").
const fastStep = 3

const (
	ebcdicNull      = 0x00
	ebcdicBlank     = 0x40
	ebcdicZero      = 0xf0
	ebcdicMinus     = 0x60
	ebcdicDup       = 0x1c
	ebcdicFieldMark = 0x1e
)

// keyboard holds the local keyboard semantics: typing, field editing and AID keys.
type keyboard struct {
	screen *screen
	state  *terminalState
	proc   *processor
	cp     *ebcdic.CodePage
	// send writes a record to the host (opcode, flags1, data), before GDS framing.
	send func(opcode, flags1 byte, data []byte)

	// pastEnd and autoAdvanced are the state at the moment the current key arrived (see press).
	pastEnd      bool
	autoAdvanced bool
}

func newKeyboard(s *screen, st *terminalState, p *processor, cp *ebcdic.CodePage, send func(opcode, flags1 byte, data []byte)) *keyboard {
	return &keyboard{screen: s, state: st, proc: p, cp: cp, send: send}
}

// typeText types characters as if pressed one by one.
func (k *keyboard) typeText(text string) {
	for _, ch := range text {
		k.typeChar(ch)
	}
}

// clearError leaves the operator error state (X II), as Reset does, without touching insert mode.
func (k *keyboard) clearError() {
	if k.state.inputInhibited == inhibitError {
		insert := k.state.insert
		k.reset()
		k.state.insert = insert
	}
}

func (k *keyboard) press(action Action) {
	if action == ActionReset {
		k.reset()
		return
	}
	// Moving the cursor is a natural way out of an operator error.
	if k.state.inputInhibited == inhibitError && isMovement(action) {
		k.clearError()
	}
	// Attention, System Request and Test Request go to the host out of band: they are the way
	// to interrupt a running job, so a locked keyboard (X SYSTEM) must not swallow them.
	if k.state.inputInhibited == inhibitSystem && isOutOfBand(action) {
		k.outOfBand(action)
		return
	}
	if k.state.inputInhibited != inhibitNone {
		return
	}
	// Every key ends the past-the-end / just-auto-advanced states; the handlers below still
	// see what they were when the key arrived.
	k.pastEnd = k.state.cursorPastEnd
	k.autoAdvanced = k.state.autoAdvanced
	k.state.cursorPastEnd = false
	k.state.autoAdvanced = false
	s := k.screen
	switch action {
	case ActionFieldExit, ActionFieldPlus, ActionFieldMinus:
		k.fieldExit(action)
	case ActionEndOfField:
		k.endOfField()
	case ActionTab:
		k.moveToField(k.nextField(s.cursor))
	case ActionBacktab:
		k.backtab()
	case ActionLeft:
		k.moveCursor(-1)
	case ActionRight:
		k.moveCursor(1)
	case ActionUp:
		k.moveCursor(-s.cols)
	case ActionDown:
		k.moveCursor(s.cols)
	case ActionFastLeft:
		k.moveCursor(-fastStep)
	case ActionFastRight:
		k.moveCursor(fastStep)
	case ActionFastUp:
		k.moveCursor(-fastStep * s.cols)
	case ActionFastDown:
		k.moveCursor(fastStep * s.cols)
	case ActionWordLeft:
		k.wordStart(-1)
	case ActionWordRight:
		k.wordStart(1)
	case ActionBeginningOfField:
		k.beginningOfField()
	case ActionHome:
		k.moveToField(k.firstField())
	case ActionNewline:
		k.newline()
	case ActionBackspace:
		k.backspace()
	case ActionDelete:
		k.deleteChar()
	case ActionDeleteWord:
		k.deleteWord()
	case ActionEraseField:
		k.eraseField()
	case ActionInsert:
		k.state.insert = !k.state.insert
	case ActionEraseEOF:
		k.eraseEOF()
	case ActionEraseInput:
		k.eraseInput()
	case ActionDup:
		k.dup()
	case ActionFieldMark:
		k.fieldMark()
	case ActionAttn, ActionSysReq, ActionTestRequest:
		k.outOfBand(action)
	default:
		if aid, ok := AIDOf(action); ok {
			k.sendAid(aid)
		}
	}
}

// ---- helpers ---------------------------------------------------------------

// outOfBand sends a flag-only record for Attention, System Request or Test Request.
func (k *keyboard) outOfBand(a Action) {
	var flag byte = FlagTRQ
	switch a {
	case ActionAttn:
		flag = FlagATN
	case ActionSysReq:
		flag = FlagSRQ
	}
	k.send(OpNoop, flag, nil)
}

func (k *keyboard) error(code string) {
	k.state.inputInhibited = inhibitError
	k.state.errorMessage = strings.TrimSpace(code + " " + OperatorErrors[code])
}

func (k *keyboard) reset() {
	if k.state.inputInhibited == inhibitError {
		k.state.inputInhibited = inhibitNone
		k.state.errorMessage = ""
		s := k.screen
		if k.state.savedErrorRow != nil {
			copy(s.buffer[s.errorRow*s.cols:], k.state.savedErrorRow)
			k.state.savedErrorRow = nil
		}
		if k.state.savedCursor >= 0 {
			s.cursor = k.state.savedCursor
			k.state.savedCursor = -1
		}
	}
	k.state.insert = false
}

func (k *keyboard) inputFields() []*field {
	var out []*field
	for _, f := range k.screen.fields {
		if !f.bypass() {
			out = append(out, f)
		}
	}
	return out
}

func (k *keyboard) currentField() *field {
	f := k.screen.fieldAt(k.screen.cursor)
	if f != nil && !f.bypass() {
		return f
	}
	return nil
}

// stops are the places Tab / Field Advance may stop: the input fields in screen order.
func (k *keyboard) stops() []*field { return k.inputFields() }

func (k *keyboard) firstField() *field {
	if st := k.stops(); len(st) > 0 {
		return st[0]
	}
	return nil
}

// progressionTarget is the field a cursor progression FCW on the field the cursor leaves
// points at, if any. The host numbers every field (bypass ones included) in screen order.
func (k *keyboard) progressionTarget(from int) *field {
	f := k.screen.fieldAt(from)
	if f == nil || f.bypass() {
		return nil
	}
	n := f.cursorProgression()
	if n == 0 || n > len(k.screen.fields) {
		return nil
	}
	if target := k.screen.fields[n-1]; !target.bypass() {
		return target
	}
	return nil
}

func (k *keyboard) nextField(from int) *field {
	if t := k.progressionTarget(from); t != nil {
		return t
	}
	stops := k.stops()
	for _, st := range stops {
		if st.start > from && !st.contains(from) {
			return st
		}
	}
	if len(stops) > 0 {
		return stops[0]
	}
	return nil
}

func (k *keyboard) prevField(from int) *field {
	stops := k.stops()
	// Backward out of a field that is the target of a cursor progression: back to its source.
	for i, f := range k.screen.fields {
		if f.contains(from) {
			for _, src := range k.screen.fields {
				if !src.bypass() && src.cursorProgression() == i+1 {
					return src
				}
			}
			break
		}
	}
	for i := len(stops) - 1; i >= 0; i-- {
		if stops[i].start < from && !stops[i].contains(from) {
			return stops[i]
		}
	}
	if len(stops) > 0 {
		return stops[len(stops)-1]
	}
	return nil
}

// moveToField moves to the first cell of a field.
func (k *keyboard) moveToField(f *field) {
	if f == nil {
		return
	}
	k.screen.cursor = f.start
}

// moveCursor moves the cursor by delta cells, wrapping around the screen.
func (k *keyboard) moveCursor(delta int) {
	size := k.screen.size()
	k.screen.cursor = ((k.screen.cursor+delta)%size + size) % size
}

// isBlankCell is a cell that separates words: null, blank or a field attribute.
func (k *keyboard) isBlankCell(pos int) bool {
	b := k.screen.buffer[pos]
	return b == ebcdicNull || b == ebcdicBlank || (b >= 0x20 && b <= 0x3f)
}

// wordStart is Tab Word / Backtab Word: the start of the next (or previous) word anywhere on
// the screen, wrapping.
func (k *keyboard) wordStart(dir int) {
	s := k.screen
	n := s.size()
	p := s.cursor
	for i := 0; i < n; i++ {
		p = (p + dir + n) % n
		if !k.isBlankCell(p) && k.isBlankCell((p-1+n)%n) {
			s.cursor = p
			return
		}
	}
}

func (k *keyboard) beginningOfField() {
	f := k.currentField()
	if f == nil {
		k.error("0005")
		return
	}
	k.screen.cursor = f.start
}

func (k *keyboard) backtab() {
	f := k.currentField()
	if f != nil && k.screen.cursor > f.start {
		k.screen.cursor = f.start
		return
	}
	k.moveToField(k.prevField(k.screen.cursor))
}

func (k *keyboard) newline() {
	s := k.screen
	row := (s.rowOf(s.cursor) + 1) % s.rows
	s.cursor = s.addr(row, 0)
	if k.currentField() == nil {
		var next *field
		for _, st := range k.stops() {
			if st.start >= s.cursor {
				next = st
				break
			}
		}
		if next == nil {
			next = k.firstField()
		}
		k.moveToField(next)
	}
}

// ---- editing within a field ------------------------------------------------

// edit is the field under the cursor as the operator edits it: bs is its data cells, idx the
// cursor offset in them.
type edit struct {
	f   *field
	bs  []byte
	idx int
}

func (k *keyboard) editField() (edit, bool) {
	f := k.currentField()
	if f == nil {
		return edit{}, false
	}
	return edit{f: f, bs: k.screen.fieldBytes(f), idx: k.screen.cursor - f.start}, true
}

// shiftRight moves the cell contents right by one from idx (the last cell is lost).
func shiftRight(bs []byte, from int) {
	for i := len(bs) - 1; i > from; i-- {
		bs[i] = bs[i-1]
	}
}

// shiftLeft moves the cell contents left by one from idx; the last cell becomes null.
func shiftLeft(bs []byte, from int) {
	for i := from; i < len(bs)-1; i++ {
		bs[i] = bs[i+1]
	}
	if len(bs) > 0 {
		bs[len(bs)-1] = ebcdicNull
	}
}

func fillCells(bs []byte, from int, b byte) {
	for i := max(0, from); i < len(bs); i++ {
		bs[i] = b
	}
}

func (k *keyboard) typeChar(ch rune) {
	if k.state.inputInhibited != inhibitNone {
		return
	}
	s := k.screen
	e, ok := k.editField()
	if !ok {
		k.error("0005")
		return
	}
	f, bs, idx := e.f, e.bs, e.idx
	// An I/O field (DDS data type I) takes data from a feature device only, never the keyboard.
	if f.shift() == shiftInputOutput {
		k.error("0004")
		return
	}
	// The field is full and the cursor sits past its last position: the operator must leave the
	// field first (Field Exit, Tab, an arrow key).
	if k.state.cursorPastEnd {
		k.error("0020")
		return
	}
	if !k.validChar(f, ch) {
		return
	}
	if f.monocase() {
		ch = unicode.ToUpper(ch)
	}
	b := k.cp.Encode(ch)
	cursor := s.cursor
	k.state.autoAdvanced = false
	if k.state.insert {
		last := bs[len(bs)-1]
		if last != ebcdicNull && last != ebcdicBlank {
			k.error("0012")
			return
		}
		shiftRight(bs, idx)
	}
	s.buffer[cursor] = b
	f.mdt = true
	if idx+1 < len(bs) {
		s.cursor = f.start + idx + 1
		return
	}
	if f.fieldExitRequired() {
		// Shown on the last cell, logically past it: Field Exit keeps this character. A field
		// that is also Auto Enter (DDS CHECK(FE) with CHECK(ER)) sends its Enter from that Field
		// Exit, not from the last character: the operator still gets to correct it.
		k.state.cursorPastEnd = true
		return
	}
	if f.autoEnter() {
		k.sendAid(AIDAutoEnter)
		return
	}
	next := k.nextField(f.start + len(bs) - 1)
	if next != nil {
		k.state.autoAdvanced = true
	}
	k.moveToField(next)
}

// endOfField puts the cursor just after the last typed character of the field.
func (k *keyboard) endOfField() {
	s := k.screen
	e, ok := k.editField()
	if !ok {
		k.error("0005")
		return
	}
	end := len(e.bs)
	for end > 0 && (e.bs[end-1] == ebcdicNull || e.bs[end-1] == ebcdicBlank) {
		end--
	}
	if end >= len(e.bs) {
		// Full: the last cell, past the end (a Field Exit now keeps the text).
		s.cursor = e.f.start + len(e.bs) - 1
		k.state.cursorPastEnd = true
	} else {
		s.cursor = e.f.start + end
	}
}

func (k *keyboard) validChar(f *field, ch rune) bool {
	digit := ch >= '0' && ch <= '9'
	switch f.shift() {
	case shiftAlphaOnly:
		if !(unicode.IsLetter(ch) || strings.ContainsRune(" ,.-", ch)) {
			k.error("0008")
			return false
		}
	case shiftNumericOnly:
		if !digit && !strings.ContainsRune(" +-.,", ch) {
			k.error("0009")
			return false
		}
	case shiftDigitsOnly:
		if !digit {
			k.error("0010")
			return false
		}
	case shiftSignedNumeric:
		if !digit {
			k.error("0011")
			return false
		}
	}
	return true
}

// lastN is the last n elements of b (all of them when it is shorter).
func lastN(b []byte, n int) []byte {
	if n <= 0 {
		return nil
	}
	if len(b) > n {
		return b[len(b)-n:]
	}
	return b
}

func (k *keyboard) fieldExit(kind Action) {
	s := k.screen
	f := k.currentField()
	if f == nil {
		k.error("0005")
		return
	}
	if kind == ActionFieldMinus && !f.numeric() {
		k.error("0016")
		return
	}
	// Pasted text that exactly filled the previous field already moved here by itself; the
	// paste's own Field Exit must not wipe this field.
	if kind == ActionFieldExit && k.autoAdvanced && s.cursor == f.start {
		return
	}
	bs := s.fieldBytes(f)
	// Past the end of a full field the character under the cursor is kept.
	from := s.cursor
	if k.pastEnd {
		from++
	}
	for i := max(from-f.start, 0); i < len(bs); i++ {
		bs[i] = ebcdicNull
	}
	var content []byte
	for _, b := range bs {
		if b != ebcdicNull {
			content = append(content, b)
		}
	}
	nonBlank := func() []byte {
		var out []byte
		for _, b := range content {
			if b != ebcdicBlank {
				out = append(out, b)
			}
		}
		return out
	}
	switch {
	case f.shift() == shiftSignedNumeric:
		// Digits right-adjusted in positions 1..n-1; the last position holds the sign.
		digits := nonBlank()
		var fill byte = ebcdicBlank
		if f.adjust() == adjustRightZero {
			fill = ebcdicZero
		}
		if n := len(bs); n > 0 {
			for i := 0; i < n-1; i++ {
				bs[i] = fill
			}
			t := lastN(digits, n-1)
			copy(bs[n-1-len(t):], t)
			bs[n-1] = ebcdicBlank
			if kind == ActionFieldMinus {
				bs[n-1] = ebcdicMinus
			}
		}
	case f.adjust() == adjustRightZero || f.adjust() == adjustRightBlank:
		var fill byte = ebcdicBlank
		if f.adjust() == adjustRightZero {
			fill = ebcdicZero
		}
		trimmed := nonBlank()
		for i := range bs {
			bs[i] = fill
		}
		t := lastN(trimmed, len(bs))
		copy(bs[len(bs)-len(t):], t)
		if kind == ActionFieldMinus {
			k.negateLastDigit(bs)
		}
	case f.adjust() == adjustMandatoryFill && len(content) > 0 && len(content) < len(bs):
		k.error("0014")
		return
	case kind == ActionFieldMinus:
		k.negateLastDigit(bs)
	}
	f.mdt = true
	if k.state.inputInhibited == inhibitError {
		return
	}
	if f.autoEnter() {
		k.sendAid(AIDAutoEnter)
		return
	}
	k.moveToField(k.nextField(f.end() - 1))
}

// negateLastDigit is Field Minus on a numeric field: the last digit's zone becomes D (negative).
func (k *keyboard) negateLastDigit(bs []byte) {
	i := len(bs) - 1
	for i >= 0 && (bs[i] == ebcdicNull || bs[i] == ebcdicBlank) {
		i--
	}
	if i < 0 || bs[i] < ebcdicZero || bs[i] > 0xf9 {
		k.error("0026")
		return
	}
	bs[i] = 0xd0 | (bs[i] & 0x0f)
}

func (k *keyboard) backspace() {
	s := k.screen
	e, ok := k.editField()
	if !ok {
		return
	}
	// Past the end of a full field the cursor is after the last character: Backspace removes
	// that character (PC style, like everywhere else in the field).
	if k.pastEnd {
		e.bs[len(e.bs)-1] = ebcdicNull
		e.f.mdt = true
		return
	}
	if e.idx > 0 {
		s.cursor = e.f.start + e.idx - 1
		shiftLeft(e.bs, e.idx-1)
		e.f.mdt = true
	}
	// At the start of a field, or outside any field, Backspace does nothing.
}

func (k *keyboard) deleteChar() {
	e, ok := k.editField()
	if !ok {
		k.error("0005")
		return
	}
	shiftLeft(e.bs, e.idx)
	e.f.mdt = true
}

// deleteWord removes the word under the cursor and the blanks after it; the rest of the field
// shifts left.
func (k *keyboard) deleteWord() {
	e, ok := k.editField()
	if !ok {
		k.error("0005")
		return
	}
	blank := func(i int) bool { return e.bs[i] == ebcdicNull || e.bs[i] == ebcdicBlank }
	end := e.idx
	for end < len(e.bs) && !blank(end) {
		end++
	}
	for end < len(e.bs) && blank(end) {
		end++
	}
	if end == e.idx {
		return
	}
	for n := 0; n < end-e.idx; n++ {
		shiftLeft(e.bs, e.idx)
	}
	e.f.mdt = true
}

// eraseField clears the whole current field, cursor back to its start.
func (k *keyboard) eraseField() {
	e, ok := k.editField()
	if !ok {
		k.error("0005")
		return
	}
	fillCells(e.bs, 0, ebcdicNull)
	e.f.mdt = true
	k.screen.cursor = e.f.start
}

func (k *keyboard) eraseEOF() {
	e, ok := k.editField()
	if !ok {
		k.error("0005")
		return
	}
	from := e.idx
	if k.pastEnd {
		from++
	}
	fillCells(e.bs, from, ebcdicNull)
	e.f.mdt = true
}

func (k *keyboard) eraseInput() {
	for _, f := range k.inputFields() {
		bs := k.screen.fieldBytes(f)
		for i := range bs {
			bs[i] = ebcdicNull
		}
		f.mdt = false
	}
	k.moveToField(k.firstField())
}

func (k *keyboard) dup() {
	e, ok := k.editField()
	if !ok || !e.f.dupEnable() {
		k.error("0019")
		return
	}
	from := e.idx
	if k.pastEnd {
		from++
	}
	fillCells(e.bs, from, ebcdicDup)
	e.f.mdt = true
	k.moveToField(k.nextField(e.f.start + len(e.bs) - 1))
}

func (k *keyboard) fieldMark() {
	f := k.currentField()
	if f == nil {
		k.error("0019")
		return
	}
	k.screen.buffer[k.screen.cursor] = ebcdicFieldMark
	f.mdt = true
	if k.screen.cursor+1 < f.end() {
		k.screen.cursor++
	}
}

// sendAid sends the AID response for the pending read.
func (k *keyboard) sendAid(aid byte) {
	s := k.screen
	// A command attention key (DDS CAnn) returns the AID only: what was typed is not sent, so
	// the program cannot act on it, and mandatory-enter checks do not apply.
	pf := pfNumber(aid)
	attention := pf > 0 && k.state.caKeys&(1<<(pf-1)) != 0
	var missing *field
	if !attention {
		for _, f := range k.inputFields() {
			if f.mandatoryEnter() && !f.mdt {
				missing = f
				break
			}
		}
	}
	if missing != nil && aid != AIDClear && aid != AIDHelp {
		s.cursor = missing.start
		k.error("0007")
		return
	}
	kind := readMDT
	switch k.state.pendingRead {
	case readInput:
		kind = readInput
	case readMDTAlt:
		kind = readMDTAlt
	}
	var data []byte
	if attention {
		data = k.proc.aidOnly(aid)
	} else {
		data = k.proc.readResponse(kind, aid)
	}
	k.state.pendingRead = readNone
	k.state.inputInhibited = inhibitSystem
	k.send(OpPutGet, 0, data)
}
