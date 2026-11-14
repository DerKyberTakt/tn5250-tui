package tn5250

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DerKyberTakt/tn5250-tui/internal/ebcdic"
)

var cp37 = ebcdic.MustGet(37)

// e is the EBCDIC (code page 37) hex of a string.
func e(s string) string { return hex.EncodeToString(cp37.EncodeString(s)) }

func h2b(t testing.TB, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(strings.ReplaceAll(s, " ", ""))
	if err != nil {
		t.Fatalf("bad hex %q: %v", s, err)
	}
	return b
}

func b2h(b []byte) string { return hex.EncodeToString(b) }

// hx formats n as one byte of hex; hx2 as two.
func hx(n int) string  { return fmt.Sprintf("%02x", n) }
func hx2(n int) string { return fmt.Sprintf("%04x", n) }

// sba is the Set Buffer Address order for a 1-based row and column.
func sba(row, col int) string { return "11" + hx(row) + hx(col) }

type captureEvent struct {
	T   int    `json:"t"`
	Dir string `json:"dir"`
	Hex string `json:"hex"`
}

type capture struct {
	Description string         `json:"description"`
	Term        string         `json:"term"`
	Events      []captureEvent `json:"events"`
}

func loadCapture(t testing.TB, name string) capture {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var c capture
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatal(err)
	}
	return c
}

func (c capture) inbound(t testing.TB) []byte {
	var out []byte
	for _, ev := range c.Events {
		if ev.Dir == "in" {
			out = append(out, h2b(t, ev.Hex)...)
		}
	}
	return out
}

func (c capture) outboundHex() string {
	var sb strings.Builder
	for _, ev := range c.Events {
		if ev.Dir == "out" {
			sb.WriteString(ev.Hex)
		}
	}
	return sb.String()
}

// sent is one record the engine wrote to the socket.
type sent struct {
	opcode byte
	flags1 byte
	data   string // hex of the payload
}

// harness is a session whose socket is a list of parsed outbound records.
type harness struct {
	t       testing.TB
	session *Session
	sent    []sent
	wire    []string // hex of every Send, in order
	trace   []string // hex of the trace copy of every Send (the wire copy when nil)
	updates int
	alarms  int
	errs    []error
}

func newHarness(t testing.TB, o Options) *harness {
	t.Helper()
	h := &harness{t: t}
	s, err := New(o, Events{
		Send: func(b, forTrace []byte) {
			h.wire = append(h.wire, b2h(b))
			if forTrace != nil {
				h.trace = append(h.trace, b2h(forTrace))
			} else {
				h.trace = append(h.trace, b2h(b))
			}
			// Skip telnet negotiation replies (they do not end with IAC EOR).
			if len(b) >= 12 && b[len(b)-2] == 0xff && b[len(b)-1] == 0xef {
				rec, err := ParseRecord(unescapeIAC(b[:len(b)-2]))
				if err != nil {
					t.Errorf("outbound record: %v", err)
					return
				}
				h.sent = append(h.sent, sent{rec.Opcode, rec.Flags1, b2h(rec.Data)})
			}
		},
		Update: func() { h.updates++ },
		Alarm:  func() { h.alarms++ },
		Error:  func(err error) { h.errs = append(h.errs, err) },
	})
	if err != nil {
		t.Fatal(err)
	}
	h.session = s
	return h
}

func unescapeIAC(b []byte) []byte {
	var out []byte
	for i := 0; i < len(b); i++ {
		out = append(out, b[i])
		if b[i] == 0xff && i+1 < len(b) && b[i+1] == 0xff {
			i++
		}
	}
	return out
}

// host feeds a host record (the payload after the GDS header) as it arrives on the wire.
func (h *harness) host(payloadHex string) {
	h.t.Helper()
	h.session.Feed(frame(h.t, payloadHex))
}

// frame wraps a host payload in a GDS header and IAC EOR.
func frame(t testing.TB, payloadHex string) []byte {
	payload := h2b(t, payloadHex)
	total := 10 + len(payload)
	out := []byte{byte(total >> 8), byte(total), 0x12, 0xa0, 0, 0, 4, 0, 0, OpPutGet}
	out = append(out, payload...)
	return append(out, 0xff, 0xef)
}

func (h *harness) screen() *screen { return h.session.screen }
func (h *harness) state() *terminalState {
	return h.session.state
}

// fieldText is the decoded text of field i (nulls as blanks).
func (h *harness) fieldText(i int) string {
	return cp37.DecodeBytes(h.session.screen.fieldBytes(h.session.screen.fields[i]))
}

func (h *harness) rowText(row int) string { return h.session.screen.rowText(row, cp37) }
