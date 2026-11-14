package tn5250

import (
	"strings"

	"github.com/DerKyberTakt/tn5250-tui/internal/ebcdic"
)

// PcCommand is a PC command the host asked the terminal to run (STRPCCMD, "PC Organizer").
// The engine only detects it; running it is up to the caller (this terminal never does).
type PcCommand struct {
	// Command is the command text.
	Command string
	// Wait is true for PAUSE(*YES): the host expects Enter only after the command finished.
	Wait bool
}

// pcoHeader is what STRPCCMD writes at the start of the hidden field: X'80FC' "PCO "
// X'8380A180'.
var pcoHeader = []byte{0x80, 0xfc, 0xd7, 0xc3, 0xd6, 0x40, 0x83, 0x80, 0xa1, 0x80}

const (
	// pcoFlagNoPause is EBCDIC 'a' in the flag byte: PAUSE(*NO).
	pcoFlagNoPause = 0x81
	// pcoMaxCommand is the longest command: 123 characters before IBM i 7.1, 1023 since.
	pcoMaxCommand = 1023
)

// detectPcCommand finds the PC command the host just wrote, if the screen is a PC Organizer
// request. STRPCCMD writes a non-display field at row 1 column 1 (so the field's data starts
// at offset 1) whose data starts with pcoHeader, then a flag byte and the command, and reads
// the screen. A PC Organizer-aware emulator runs the command and answers Enter; anything else
// shows the host's fallback text ("Required PC program (PCO.EXE) is not active").
func detectPcCommand(s *screen, cp *ebcdic.CodePage) (PcCommand, bool) {
	var f *field
	for _, c := range s.fields {
		if c.start == 1 {
			f = c
			break
		}
	}
	if f == nil || !f.nondisplay() || f.length <= len(pcoHeader)+1 {
		return PcCommand{}, false
	}
	data := s.fieldBytes(f)
	if len(data) <= len(pcoHeader) {
		return PcCommand{}, false
	}
	for i, b := range pcoHeader {
		if data[i] != b {
			return PcCommand{}, false
		}
	}
	wait := data[len(pcoHeader)] != pcoFlagNoPause
	start := len(pcoHeader) + 1
	end := min(len(data), start+pcoMaxCommand)
	for end > start && (data[end-1] == 0x00 || data[end-1] == 0x40) {
		end--
	}
	var sb strings.Builder
	for _, b := range data[start:end] {
		// The control bytes between words decode to blanks, not the hollow box.
		r := cp.Decode(b)
		if r < 0x20 || r == 0x7f || r == ebcdic.Unprintable {
			r = ' '
		}
		sb.WriteRune(r)
	}
	command := strings.TrimSpace(sb.String())
	if command == "" {
		return PcCommand{}, false
	}
	return PcCommand{Command: command, Wait: wait}, true
}

// PcCommand reports the PC command on the current screen: the host wrote a STRPCCMD request
// and is waiting for a read. Answer it with Press(ActionEnter) so its program resumes.
func (s *Session) PcCommand() (PcCommand, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state.pendingRead == readNone {
		return PcCommand{}, false
	}
	return detectPcCommand(s.screen, s.cp)
}
