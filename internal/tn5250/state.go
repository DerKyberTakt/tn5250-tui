package tn5250

// readKind is which response format the host expects next (set by a Read command).
type readKind int

const (
	readNone readKind = iota
	readInput
	readMDT
	readMDTAlt
)

// Values of OIA.InputInhibited.
const (
	inhibitNone   = "none"
	inhibitSystem = "system"
	inhibitError  = "error"
)

// terminalState is the terminal status that is not part of the screen buffer.
type terminalState struct {
	inputInhibited string
	messageWaiting bool
	insert         bool
	// pendingRead is set by a Read command: which response format the host expects next.
	pendingRead readKind
	// errorMessage is the text for the message line, from one of two sources: a host Write
	// Error Code (which writes the error row itself; the text is read back from it) or an
	// operator error (code and text, shown by the OIA only; the error row is not written).
	errorMessage string
	// savedCursor is the cursor saved by Write Error Code, restored on Reset (-1 = none).
	savedCursor int
	// savedErrorRow holds the error row contents saved by Write Error Code (nil = none).
	savedErrorRow []byte
	// cursorPastEnd: the last cell of a field-exit-required field was just filled; the cursor
	// is shown on that cell but is logically past it, so Field Exit / Erase EOF keep the
	// character and typing is refused (0020) until the cursor moves.
	cursorPastEnd bool
	// autoAdvanced: the last keystroke filled a field and moved to the next one by itself.
	autoAdvanced bool
	// caKeys: function keys the host declared as command attention keys (DDS CAnn); bit n-1 set
	// means Fn sends only the cursor and AID, no field data. From the Start of Header order's
	// command key bytes; 0 when the header did not carry them.
	caKeys uint32
}

func newTerminalState() *terminalState {
	t := &terminalState{}
	t.reset()
	return t
}

func (t *terminalState) reset() {
	t.inputInhibited = inhibitSystem
	t.messageWaiting = false
	t.insert = false
	t.pendingRead = readNone
	t.errorMessage = ""
	t.savedCursor = -1
	t.savedErrorRow = nil
	t.cursorPastEnd = false
	t.autoAdvanced = false
	t.caKeys = 0
}
