// Package viewer lets a terminal watch the sessions an MCP server (`tn5250-tui mcp`) drives:
// the server listens on the loopback interface with a random port and a token (written to a
// file only the user can read), and `tn5250-tui attach` renders one session live. The wire
// format is JSON lines; a frame is the whole screen (a 27x132 screen is a few KB, fine on
// loopback).
package viewer

import (
	"encoding/base64"
	"strings"

	"github.com/DerKyberTakt/tn5250-tui/internal/tn5250"
	"github.com/DerKyberTakt/tn5250-tui/internal/tui"
)

// Msg is one line on the wire, either way.
type Msg struct {
	T string `json:"t"` // hello, list, watch, sessions, frame, closed, error, bye
	// Token authenticates a hello.
	Token string `json:"token,omitempty"`
	// Session names the session for watch, frame and closed.
	Session string `json:"session,omitempty"`
	// Sessions answers a list.
	Sessions []SessionInfo `json:"sessions,omitempty"`
	// Frame carries a screen.
	Frame *Frame `json:"frame,omitempty"`
	// Text is an error message or a note.
	Text string `json:"text,omitempty"`
}

// SessionInfo describes one session of the server.
type SessionInfo struct {
	Session   string `json:"session"`
	Host      string `json:"host"`
	Device    string `json:"device,omitempty"`
	Connected bool   `json:"connected"`
	Since     string `json:"since,omitempty"`
	Seq       uint64 `json:"seq"`
	Inhibited string `json:"inhibited,omitempty"`
}

// Frame is a screen: the rows as text, the attribute bytes and the cell flags per row
// (base64), the fields, the cursor, the sequence number and the OIA.
type Frame struct {
	Session   string   `json:"session"`
	Rows      int      `json:"rows"`
	Cols      int      `json:"cols"`
	Text      []string `json:"text"`
	Attrs     []string `json:"attrs"`
	Flags     []string `json:"flags"` // bit0 attribute, bit1 null, bit2 in field
	Fields    []Field  `json:"fields,omitempty"`
	CursorRow int      `json:"cursorRow"`
	CursorCol int      `json:"cursorCol"`
	Seq       uint64   `json:"seq"`
	OIA       OIA      `json:"oia"`
}

// Field is one entry of the format table.
type Field struct {
	Start      int  `json:"start"`
	Length     int  `json:"length"`
	Bypass     bool `json:"bypass,omitempty"`
	Nondisplay bool `json:"nondisplay,omitempty"`
	MDT        bool `json:"mdt,omitempty"`
}

// OIA is the status line state.
type OIA struct {
	Connected      bool   `json:"connected"`
	InputInhibited string `json:"inhibited"`
	Insert         bool   `json:"insert,omitempty"`
	MessageWaiting bool   `json:"mw,omitempty"`
	DeviceName     string `json:"device,omitempty"`
	Message        string `json:"message,omitempty"`
	QueuedKeys     int    `json:"queued,omitempty"`
}

// EncodeFrame packs an engine snapshot and OIA for the wire.
func EncodeFrame(session string, s tn5250.Snapshot, o tn5250.OIA) Frame {
	f := Frame{Session: session, Rows: s.Rows, Cols: s.Cols, CursorRow: s.Cursor.Row, CursorCol: s.Cursor.Col, Seq: s.Seq}
	f.Text = make([]string, s.Rows)
	f.Attrs = make([]string, s.Rows)
	f.Flags = make([]string, s.Rows)
	attrs := make([]byte, s.Cols)
	flags := make([]byte, s.Cols)
	var b strings.Builder
	for r := 0; r < s.Rows; r++ {
		b.Reset()
		for c := 0; c < s.Cols; c++ {
			cell := s.Cells[r*s.Cols+c]
			ch := cell.Ch
			if ch < ' ' {
				ch = ' '
			}
			b.WriteRune(ch)
			attrs[c] = cell.Attr
			var fl byte
			if cell.Attribute {
				fl |= 1
			}
			if cell.Null {
				fl |= 2
			}
			if cell.InField {
				fl |= 4
			}
			flags[c] = fl
		}
		f.Text[r] = b.String()
		f.Attrs[r] = base64.StdEncoding.EncodeToString(attrs)
		f.Flags[r] = base64.StdEncoding.EncodeToString(flags)
	}
	for _, fd := range s.Fields {
		f.Fields = append(f.Fields, Field{Start: fd.Start, Length: fd.Length, Bypass: fd.Bypass, Nondisplay: fd.Nondisplay, MDT: fd.MDT})
	}
	f.OIA = OIA{Connected: o.Connected, InputInhibited: o.InputInhibited, Insert: o.Insert, MessageWaiting: o.MessageWaiting,
		DeviceName: o.DeviceName, Message: o.Message, QueuedKeys: o.QueuedKeys}
	return f
}

// Snapshot unpacks the frame for the TUI's screen primitive.
func (f Frame) Snapshot() tui.Snapshot {
	s := tui.Snapshot{Rows: f.Rows, Cols: f.Cols, Seq: f.Seq}
	s.Cursor.Row, s.Cursor.Col = f.CursorRow, f.CursorCol
	if f.Rows <= 0 || f.Cols <= 0 {
		return tui.Snapshot{}
	}
	s.Cells = make([]tui.Cell, f.Rows*f.Cols)
	for r := 0; r < f.Rows; r++ {
		var text []rune
		var attrs, flags []byte
		if r < len(f.Text) {
			text = []rune(f.Text[r])
		}
		if r < len(f.Attrs) {
			attrs, _ = base64.StdEncoding.DecodeString(f.Attrs[r])
		}
		if r < len(f.Flags) {
			flags, _ = base64.StdEncoding.DecodeString(f.Flags[r])
		}
		for c := 0; c < f.Cols; c++ {
			cell := tui.Cell{Ch: ' ', Attr: 0x20}
			if c < len(text) {
				cell.Ch = text[c]
			}
			if c < len(attrs) {
				cell.Attr = attrs[c]
			}
			if c < len(flags) {
				cell.Attribute, cell.Null, cell.InField = flags[c]&1 != 0, flags[c]&2 != 0, flags[c]&4 != 0
			}
			s.Cells[r*f.Cols+c] = cell
		}
	}
	for _, fd := range f.Fields {
		s.Fields = append(s.Fields, tui.FieldInfo{Start: fd.Start, Length: fd.Length, Bypass: fd.Bypass, Nondisplay: fd.Nondisplay, MDT: fd.MDT})
	}
	return s
}

// TUIOIA unpacks the frame's OIA for the TUI's status line.
func (f Frame) TUIOIA() tui.OIA {
	o := f.OIA
	return tui.OIA{Connected: o.Connected, InputInhibited: o.InputInhibited, Insert: o.Insert, MessageWaiting: o.MessageWaiting,
		DeviceName: o.DeviceName, Message: o.Message, QueuedKeys: o.QueuedKeys}
}
