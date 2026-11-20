package tn5250

import (
	"fmt"
	"sync"

	"github.com/DerKyberTakt/tn5250-tui/internal/ebcdic"
)

// typeAheadLimit is the most keys kept while the keyboard is locked; ACS keeps a similar small
// buffer.
const typeAheadLimit = 256

// Options configures a Session.
type Options struct {
	// ScreenSize is "24x80" (terminal type IBM-3179-2, the default) or "27x132" (IBM-3477-FC).
	ScreenSize string
	// CodePage is the EBCDIC CCSID, default 37.
	CodePage int
	// DeviceName is the requested device name; empty lets the host assign one.
	DeviceName string
	// KeyboardType overrides the KBDTYPE sent with the device name (3 letters); empty = the
	// one for the code page.
	KeyboardType string
	// NoTypeAhead turns type-ahead off. By default (false) keys pressed while the keyboard is
	// locked (X SYSTEM) are kept and replayed when the host unlocks it, as ACS does.
	NoTypeAhead bool
}

// Events are the callbacks of a Session; every field is optional. They run after the session's
// internal lock is released, so they may call Session methods.
type Events struct {
	// Send gets bytes for the socket. forTrace is the same frame with the data of non-display
	// fields (passwords) blanked, for traces and logs; nil means the frame holds no secret.
	Send func(b []byte, forTrace []byte)
	// Update is called when the screen or the status changed.
	Update func()
	// Alarm is called when the host asked for the terminal alarm.
	Alarm func()
	// Trace gets one line per display command or order (debugging).
	Trace func(line string)
	// Error reports a protocol error; the connection should be closed.
	Error func(err error)
}

// OIA is the operator information area: the status line under the screen.
type OIA struct {
	// Connected is the value the caller passed to Session.OIA.
	Connected bool
	// InputInhibited is "none", "system" (X SYSTEM: the host has the keyboard) or "error"
	// (X II: an operator or host error waits for Reset).
	InputInhibited string
	// Insert is true while insert mode is on.
	Insert bool
	// MessageWaiting is the message waiting light (MW).
	MessageWaiting bool
	// DeviceName is the device the host assigned (or the requested one before it answers).
	DeviceName string
	// Message is the operator error or host error line text.
	Message string
	// QueuedKeys is the number of type-ahead keys waiting for the host to unlock the keyboard.
	QueuedKeys int
}

type queuedKey struct {
	press Action // "" for typed text
	text  string
}

// Session is a 5250 display session: telnet framing, data stream processing and the screen
// model, with no I/O of its own. Feed it socket bytes; it emits bytes to send through
// Events.Send.
type Session struct {
	mu   sync.Mutex
	opts Options
	ev   Events
	cp   *ebcdic.CodePage

	terminalType string
	screen       *screen
	state        *terminalState
	proc         *processor
	kb           *keyboard
	telnet       *telnetSession

	deviceName string
	typeAhead  bool
	queue      []queuedKey
	seq        uint64
	pending    []func()
}

// New creates a session. It fails for an unknown screen size or an unsupported code page.
func New(o Options, ev Events) (*Session, error) {
	if o.ScreenSize == "" {
		o.ScreenSize = "24x80"
	}
	if o.ScreenSize != "24x80" && o.ScreenSize != "27x132" {
		return nil, fmt.Errorf("tn5250: unsupported screen size %q (want 24x80 or 27x132)", o.ScreenSize)
	}
	if o.CodePage == 0 {
		o.CodePage = 37
	}
	cp, err := ebcdic.Get(o.CodePage)
	if err != nil {
		return nil, fmt.Errorf("tn5250: %w", err)
	}
	s := &Session{opts: o, ev: ev, cp: cp, typeAhead: !o.NoTypeAhead, deviceName: o.DeviceName}
	s.terminalType = "IBM-3179-2"
	if o.ScreenSize == "27x132" {
		s.terminalType = "IBM-3477-FC"
	}
	s.screen = newScreen()
	s.state = newTerminalState()
	s.proc = newProcessor(s.screen, s.state, s.terminalType, cp, s.alarm, s.traceFunc())
	s.kb = newKeyboard(s.screen, s.state, s.proc, cp, func(opcode, flags1 byte, data []byte) {
		s.sendRecord(opcode, flags1, data, s.proc.takeSecretRanges())
	})
	s.telnet = newTelnetSession(telnetOptions{
		terminalType: s.terminalType,
		deviceName:   o.DeviceName,
		codePage:     o.CodePage,
		keyboardType: o.KeyboardType,
	}, telnetCallbacks{
		send:   s.emitSend,
		record: s.onRecord,
		fail: func(err error) {
			if s.ev.Error != nil {
				fn := s.ev.Error
				s.pending = append(s.pending, func() { fn(err) })
			}
		},
		environment: func(vars map[string]string) {
			if name := vars["DEVNAME"]; name != "" {
				s.deviceName = name
			}
		},
	})
	return s, nil
}

// do runs f under the lock, then the events it queued with the lock released.
func (s *Session) do(f func()) {
	s.mu.Lock()
	f()
	ev := s.pending
	s.pending = nil
	s.mu.Unlock()
	for _, e := range ev {
		e()
	}
}

func (s *Session) emitSend(b, forTrace []byte) {
	if s.ev.Send == nil {
		return
	}
	send := s.ev.Send
	s.pending = append(s.pending, func() { send(b, forTrace) })
}

func (s *Session) alarm() {
	if s.ev.Alarm == nil {
		return
	}
	alarm := s.ev.Alarm
	s.pending = append(s.pending, alarm)
}

func (s *Session) traceFunc() func(string) {
	if s.ev.Trace == nil {
		return nil
	}
	trace := s.ev.Trace
	return func(line string) { s.pending = append(s.pending, func() { trace(line) }) }
}

// notify counts a change (Snapshot.Seq) and queues the Update event.
func (s *Session) notify() {
	s.seq++
	if s.ev.Update != nil {
		s.pending = append(s.pending, s.ev.Update)
	}
}

// Failed reports why the connection must be closed (the host broke a framing limit, such as
// a record or subnegotiation that never ends), or nil. Feed drops everything after that.
func (s *Session) Failed() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.telnet.failed
}

// Feed takes bytes from the socket.
func (s *Session) Feed(b []byte) {
	s.do(func() { s.telnet.feed(b) })
}

// immediate actions act at once even while the keyboard is locked.
func immediate(a Action) bool {
	return a == ActionReset || a == ActionAttn || a == ActionSysReq || a == ActionTestRequest
}

// Press presses a key. Actions that are not valid (unknown names) do nothing.
func (s *Session) Press(a Action) {
	s.do(func() {
		if a == ActionReset {
			s.queue = nil
		}
		if !immediate(a) && s.enqueue(queuedKey{press: a}) {
			return
		}
		s.kb.press(a)
		s.notify()
	})
}

// Type types text into the field at the cursor, as if pressed key by key.
func (s *Session) Type(text string) {
	s.do(func() {
		if s.enqueue(queuedKey{text: text}) {
			return
		}
		s.kb.typeText(text)
		s.notify()
	})
}

// SetCursor moves the cursor (0-based row and column); it also leaves an operator error state.
// A position outside the screen is ignored.
func (s *Session) SetCursor(row, col int) {
	s.do(func() {
		if row < 0 || col < 0 || row >= s.screen.rows || col >= s.screen.cols {
			return
		}
		s.kb.clearError()
		s.screen.cursor = s.screen.addr(row, col)
		s.state.cursorPastEnd = false
		s.state.autoAdvanced = false
		s.notify()
	})
}

// enqueue keeps a key while the host has the keyboard locked; false if it should run now.
func (s *Session) enqueue(k queuedKey) bool {
	if !s.typeAhead || s.state.inputInhibited != inhibitSystem {
		return false
	}
	if len(s.queue) < typeAheadLimit {
		s.queue = append(s.queue, k)
	}
	s.notify()
	return true
}

// drainQueue replays type-ahead once the host unlocks the keyboard. It stops when a key locks
// it again (an AID key) and leaves the rest for the next unlock; an operator error or host
// error message discards the rest, as ACS does.
func (s *Session) drainQueue() {
	for len(s.queue) > 0 && s.state.inputInhibited == inhibitNone {
		k := s.queue[0]
		s.queue = s.queue[1:]
		if k.press != "" {
			s.kb.press(k.press)
		} else {
			s.kb.typeText(k.text)
		}
	}
	if s.state.inputInhibited == inhibitError {
		s.queue = nil
	}
}

// Snapshot returns the screen as it is now. Characters of non-display fields are blanks.
func (s *Session) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap := s.screen.snapshot(s.cp)
	snap.Seq = s.seq
	return snap
}

// OIA returns the status line; connected is reported back as OIA.Connected.
func (s *Session) OIA(connected bool) OIA {
	s.mu.Lock()
	defer s.mu.Unlock()
	return OIA{
		Connected:      connected,
		InputInhibited: s.state.inputInhibited,
		Insert:         s.state.insert,
		MessageWaiting: s.state.messageWaiting,
		DeviceName:     s.deviceName,
		Message:        s.state.errorMessage,
		QueuedKeys:     len(s.queue),
	}
}

// Reset puts the session back to its start state, for a new connection: the screen is cleared
// to 24x80, the type-ahead queue and the telnet negotiation are forgotten.
func (s *Session) Reset() {
	s.do(func() {
		s.screen.setSize(24, 80)
		s.state.reset()
		s.proc.reset()
		s.queue = nil
		s.deviceName = s.opts.DeviceName
		s.telnet.reset()
		s.notify()
	})
}

// sendRecord sends a record to the host with GDS framing. secret lists the byte ranges of data
// that hold non-display field data: the trace copy of the frame has them blanked.
func (s *Session) sendRecord(opcode, flags1 byte, data []byte, secret [][2]int) {
	frame := BuildRecord(opcode, flags1, data)
	if len(secret) == 0 {
		s.telnet.sendRecord(frame, nil)
		return
	}
	masked := append([]byte(nil), data...)
	for _, r := range secret {
		for i := max(r[0], 0); i < r[1] && i < len(masked); i++ {
			masked[i] = ebcdicBlank
		}
	}
	s.telnet.sendRecord(frame, BuildRecord(opcode, flags1, masked))
}

// onRecord handles one complete record from the host.
func (s *Session) onRecord(b []byte) {
	rec, err := ParseRecord(b)
	if err != nil {
		if s.ev.Error != nil {
			fn := s.ev.Error
			s.pending = append(s.pending, func() { fn(err) })
		}
		return
	}
	switch rec.Opcode {
	case OpMessageLightOn:
		s.state.messageWaiting = true
	case OpMessageLightOff:
		s.state.messageWaiting = false
	case OpCancelInvite:
		s.sendRecord(OpCancelInvite, 0, nil, nil)
	}
	if len(rec.Data) > 0 {
		for _, r := range s.proc.process(rec.Data) {
			s.sendRecord(r.opcode, 0, r.data, r.secret)
		}
	}
	s.drainQueue()
	s.notify()
}
