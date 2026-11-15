package tn5250

import (
	"strconv"
	"strings"
)

// Telnet commands and options used by TN5250 (RFC 854, 1205, 2877, 4777).
const (
	// IAC is the telnet "interpret as command" byte.
	IAC = 255
	// TelnetDONT asks the peer to disable an option.
	TelnetDONT = 254
	// TelnetDO asks the peer to enable an option.
	TelnetDO = 253
	// TelnetWONT refuses to enable an option.
	TelnetWONT = 252
	// TelnetWILL offers to enable an option.
	TelnetWILL = 251
	// TelnetSB starts a sub-negotiation.
	TelnetSB = 250
	// TelnetSE ends a sub-negotiation.
	TelnetSE = 240
	// TelnetEOR is the END-OF-RECORD command (after IAC).
	TelnetEOR = 239

	// OptBinary is the BINARY option.
	OptBinary = 0
	// OptSGA is SUPPRESS-GO-AHEAD.
	OptSGA = 3
	// OptTerminalType is TERMINAL-TYPE.
	OptTerminalType = 24
	// OptEOR is END-OF-RECORD.
	OptEOR = 25
	// OptNewEnviron is NEW-ENVIRON.
	OptNewEnviron = 39
)

const (
	ttIS      = 0
	ttSEND    = 1
	neIS      = 0
	neSEND    = 1
	neINFO    = 2
	neVAR     = 0
	neVALUE   = 1
	neESC     = 2
	neUSERVAR = 3
)

// telnetOptions are the values the negotiation needs.
type telnetOptions struct {
	// terminalType is e.g. IBM-3477-FC (27x132) or IBM-3179-2 (24x80).
	terminalType string
	// deviceName is the requested device name; empty lets the host assign one.
	deviceName string
	// codePage is sent as the CODEPAGE user variable when a device name is requested.
	codePage int
	// keyboardType is a KBDTYPE override (3 letters); empty = the one for the code page.
	keyboardType string
}

// telnetCallbacks is how the telnet layer talks to the rest of the engine.
type telnetCallbacks struct {
	// send writes bytes to the socket; forTrace is the same frame with secrets blanked, when
	// the record held any (nil otherwise).
	send func(b, forTrace []byte)
	// record gets one complete 5250 record (IAC removed).
	record func(rec []byte)
	// environment gets the variables the host reports through NEW-ENVIRON (e.g. DEVNAME).
	environment func(vars map[string]string)
}

// telnetSession is the telnet layer for TN5250: option negotiation, IAC escaping and
// END-OF-RECORD framing. Pure: bytes in, bytes out through callbacks.
type telnetSession struct {
	opts    telnetOptions
	cb      telnetCallbacks
	pending []byte
	rec     []byte
	agreed  map[int]bool
}

func newTelnetSession(o telnetOptions, cb telnetCallbacks) *telnetSession {
	return &telnetSession{opts: o, cb: cb, agreed: map[int]bool{}}
}

// reset forgets the connection state (a new connection starts the negotiation again).
func (t *telnetSession) reset() {
	t.pending = nil
	t.rec = nil
	t.agreed = map[int]bool{}
}

// feed takes bytes from the socket.
func (t *telnetSession) feed(chunk []byte) {
	t.pending = append(t.pending, chunk...)
	t.parse()
}

// frameRecord escapes IAC and appends IAC EOR.
func frameRecord(b []byte) []byte {
	out := make([]byte, 0, len(b)+2)
	for _, x := range b {
		out = append(out, x)
		if x == IAC {
			out = append(out, IAC)
		}
	}
	return append(out, IAC, TelnetEOR)
}

// sendRecord sends one 5250 record: escapes IAC and appends IAC EOR. forTrace is framed the
// same way.
func (t *telnetSession) sendRecord(rec, forTrace []byte) {
	var trace []byte
	if forTrace != nil {
		trace = frameRecord(forTrace)
	}
	t.cb.send(frameRecord(rec), trace)
}

func (t *telnetSession) reply(b ...byte) {
	t.cb.send(b, nil)
}

func (t *telnetSession) parse() {
	buf := t.pending
	i := 0
loop:
	for i < len(buf) {
		b := buf[i]
		if b != IAC {
			t.rec = append(t.rec, b)
			i++
			continue
		}
		if i+1 >= len(buf) {
			break
		}
		cmd := buf[i+1]
		switch cmd {
		case IAC:
			t.rec = append(t.rec, IAC)
			i += 2
		case TelnetDO, TelnetDONT, TelnetWILL, TelnetWONT:
			if i+2 >= len(buf) {
				break loop
			}
			t.negotiate(cmd, buf[i+2])
			i += 3
		case TelnetSB:
			j := i + 2
			for j+1 < len(buf) && !(buf[j] == IAC && buf[j+1] == TelnetSE) {
				j++
			}
			if j+1 >= len(buf) {
				break loop
			}
			t.subnegotiate(buf[i+2 : j])
			i = j + 2
		case TelnetEOR:
			rec := t.rec
			t.rec = nil
			i += 2
			t.cb.record(rec)
		default:
			// Other two-byte commands (NOP, GA, ...) are ignored.
			i += 2
		}
	}
	t.pending = append([]byte(nil), buf[i:]...)
}

func (t *telnetSession) negotiate(cmd, opt byte) {
	switch cmd {
	case TelnetDO:
		ok := opt == OptBinary || opt == OptEOR || opt == OptTerminalType || opt == OptNewEnviron
		if ok {
			t.agreed[int(opt)] = true
			t.reply(IAC, TelnetWILL, opt)
		} else {
			t.reply(IAC, TelnetWONT, opt)
		}
	case TelnetWILL:
		ok := opt == OptBinary || opt == OptEOR || opt == OptSGA
		if ok {
			t.agreed[int(opt)] = true
			t.reply(IAC, TelnetDO, opt)
		} else {
			t.reply(IAC, TelnetDONT, opt)
		}
	}
	// DONT / WONT need no answer.
}

func (t *telnetSession) subnegotiate(sub []byte) {
	if len(sub) == 0 {
		return
	}
	opt := sub[0]
	var verb byte = 255
	if len(sub) > 1 {
		verb = sub[1]
	}
	switch {
	case opt == OptTerminalType && verb == ttSEND:
		out := []byte{IAC, TelnetSB, OptTerminalType, ttIS}
		out = append(out, asciiBytes(t.opts.terminalType)...)
		out = append(out, IAC, TelnetSE)
		t.reply(out...)
	case opt == OptNewEnviron && verb == neSEND:
		out := []byte{IAC, TelnetSB, OptNewEnviron, neIS}
		out = append(out, t.environmentReply()...)
		out = append(out, IAC, TelnetSE)
		t.reply(out...)
	case opt == OptNewEnviron && (verb == neIS || verb == neINFO):
		if t.cb.environment != nil {
			t.cb.environment(parseEnvironment(sub[2:]))
		}
	}
}

func asciiBytes(s string) []byte {
	out := make([]byte, 0, len(s))
	for _, r := range s {
		out = append(out, byte(r)&0x7f)
	}
	return out
}

func (t *telnetSession) environmentReply() []byte {
	uservar := func(out []byte, k, v string) []byte {
		out = append(out, neUSERVAR)
		out = append(out, asciiBytes(k)...)
		out = append(out, neVALUE)
		return append(out, asciiBytes(v)...)
	}
	var out []byte
	if name := strings.ToUpper(strings.TrimSpace(t.opts.deviceName)); name != "" {
		out = uservar(out, "DEVNAME", name)
	}
	// KBDTYPE, CODEPAGE and CHARSET belong together (RFC 4777 section 4: the last two only
	// count beside a KBDTYPE) and go with or without a device name: the host gives the
	// virtual device it creates (QPADEVnnnn too) that keyboard type and character set, which
	// is what turns the job's CCSID into the right letters on the screen. A code page the
	// table does not know sends none of them and the host uses its system values.
	if t.opts.codePage != 0 {
		if nls, ok := lookupNLS(t.opts.codePage, t.opts.keyboardType); ok {
			out = uservar(out, "KBDTYPE", nls.kbdType)
			out = uservar(out, "CODEPAGE", strconv.Itoa(t.opts.codePage))
			out = uservar(out, "CHARSET", nls.charset)
		}
	}
	return out
}

// parseEnvironment parses NEW-ENVIRON "IS"/"INFO" payloads into name -> value.
func parseEnvironment(b []byte) map[string]string {
	vars := map[string]string{}
	var name, value []byte
	inValue := false
	flush := func() {
		if len(name) > 0 {
			vars[string(name)] = string(value)
		}
		name, value, inValue = nil, nil, false
	}
	for i := 0; i < len(b); i++ {
		c := b[i]
		switch {
		case c == neVAR || c == neUSERVAR:
			flush()
		case c == neVALUE:
			inValue = true
		case c == neESC:
			// ESC: next byte is literal.
			i++
			if i < len(b) {
				if inValue {
					value = append(value, b[i])
				} else {
					name = append(name, b[i])
				}
			}
		case inValue:
			value = append(value, c)
		default:
			name = append(name, c)
		}
	}
	flush()
	return vars
}
