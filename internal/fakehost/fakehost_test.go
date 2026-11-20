package fakehost

import (
	"bytes"
	"io"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DerKyberTakt/tn5250-tui/internal/ebcdic"
)

func loadTestCapture(t *testing.T) Capture {
	t.Helper()
	c, err := LoadCapture(filepath.Join("..", "..", "testdata", "pub400-signon-3477fc.json"))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func start(t *testing.T, cp Capture, opt Options) *Server {
	t.Helper()
	opt.Logf = func(string, ...any) {}
	s, err := StartWith("127.0.0.1:0", cp, opt)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func dial(t *testing.T, s *Server) net.Conn {
	t.Helper()
	c, err := net.Dial("tcp", s.Addr())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func readN(t *testing.T, c net.Conn, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := io.ReadFull(c, b); err != nil {
		t.Fatalf("read %d bytes: %v", n, err)
	}
	return b
}

// play acts as the client for the whole capture. With merge, every run of "out" chunks
// goes out in a single write.
func play(t *testing.T, c net.Conn, cp Capture, merge bool) {
	t.Helper()
	var outs []byte
	flush := func() {
		if len(outs) > 0 {
			if _, err := c.Write(outs); err != nil {
				t.Fatal(err)
			}
			outs = nil
		}
	}
	for i, e := range cp.Events {
		d := cp.data[i]
		if e.Dir == "in" {
			flush()
			if got := readN(t, c, len(d)); !bytes.Equal(got, d) {
				t.Fatalf("event %d: host sent %x want %x", i, got, d)
			}
			continue
		}
		outs = append(outs, d...)
		if !merge {
			flush()
		}
	}
	flush()
}

func TestLoadCapture(t *testing.T) {
	cp := loadTestCapture(t)
	if len(cp.Events) == 0 || cp.Events[0].Dir != "in" {
		t.Fatalf("unexpected capture: %+v", cp.Events)
	}
	if _, err := LoadCapture("no-such-file.json"); err == nil {
		t.Fatal("want an error for a missing file")
	}
}

func TestReplayReachesFirstGDSRecord(t *testing.T) {
	cp := loadTestCapture(t)
	s := start(t, cp, Options{})
	c := dial(t, s)

	first := cp.data[0]
	if got := readN(t, c, len(first)); !bytes.Equal(got, first) {
		t.Fatalf("first in = %x want %x", got, first)
	}
	if _, err := c.Write(cp.data[1]); err != nil {
		t.Fatal(err)
	}
	for i := 2; i < len(cp.Events); i++ {
		d := cp.data[i]
		if cp.Events[i].Dir == "out" {
			if _, err := c.Write(d); err != nil {
				t.Fatal(err)
			}
			continue
		}
		got := readN(t, c, len(d))
		if len(got) > 4 && got[2] == 0x12 && got[3] == 0xa0 {
			if int(got[0])<<8|int(got[1]) < 10 {
				t.Fatalf("bad GDS length in %x", got[:4])
			}
			if m := s.Mismatches(); len(m) != 0 {
				t.Fatalf("mismatches: %v", m)
			}
			return
		}
	}
	t.Fatal("never saw a GDS record (0x12 0xa0)")
}

func TestReplayWithRechunkedClientBytes(t *testing.T) {
	cp := loadTestCapture(t)
	s := start(t, cp, Options{})
	c := dial(t, s)
	play(t, c, cp, true)
	// Replay is complete; any record now gets the canned screen.
	if _, err := c.Write([]byte{0x00, 0x0a, 0x12, 0xa0, 0x00, 0x00, 0x04, 0x00, 0x00, 0x03, 0xff, 0xef}); err != nil {
		t.Fatal(err)
	}
	want := EnterScreen()
	if got := readN(t, c, len(want)); !bytes.Equal(got, want) {
		t.Fatalf("canned screen = %x want %x", got, want)
	}
	if m := s.Mismatches(); len(m) != 0 {
		t.Fatalf("mismatches: %v", m)
	}
}

func TestMismatchClosesAndIsRecorded(t *testing.T) {
	cp := loadTestCapture(t)
	s := start(t, cp, Options{})
	c := dial(t, s)
	readN(t, c, len(cp.data[0]))
	// expected fffb27; send fffb28 (differs at offset 2)
	if _, err := c.Write([]byte{0xff, 0xfb, 0x28}); err != nil {
		t.Fatal(err)
	}
	_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := c.Read(make([]byte, 16)); err != io.EOF {
		t.Fatalf("want EOF after a mismatch, got %v", err)
	}
	m := s.Mismatches()
	if len(m) != 1 || m[0].Event != 1 || m[0].Offset != 2 {
		t.Fatalf("mismatches = %v", m)
	}
	if !strings.Contains(m[0].String(), "offset 2") {
		t.Fatalf("String() = %q", m[0])
	}
}

func TestTimeoutRecordsMismatch(t *testing.T) {
	cp := loadTestCapture(t)
	s := start(t, cp, Options{Timeout: 150 * time.Millisecond})
	c := dial(t, s)
	readN(t, c, len(cp.data[0]))
	_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := c.Read(make([]byte, 16)); err != io.EOF {
		t.Fatalf("want EOF after a timeout, got %v", err)
	}
	if m := s.Mismatches(); len(m) != 1 || m[0].Offset != 0 || len(m[0].Got) != 0 {
		t.Fatalf("mismatches = %v", m)
	}
}

func TestLenientKeepsReplaying(t *testing.T) {
	cp := loadTestCapture(t)
	s := start(t, cp, Options{Lenient: true, Timeout: 150 * time.Millisecond})
	c := dial(t, s)
	// Say nothing: every "out" times out, yet all "in" chunks still arrive.
	for i, e := range cp.Events {
		if e.Dir == "in" {
			readN(t, c, len(cp.data[i]))
		}
	}
	if len(s.Mismatches()) == 0 {
		t.Fatal("want recorded mismatches")
	}
}

func TestEnterScreenFraming(t *testing.T) {
	b := EnterScreen()
	if !bytes.HasSuffix(b, []byte{0xff, 0xef}) {
		t.Fatalf("no IAC EOR: %x", b)
	}
	rec := b[:len(b)-2]
	if int(rec[0])<<8|int(rec[1]) != len(rec) || rec[2] != 0x12 || rec[3] != 0xa0 || rec[9] != 0x03 {
		t.Fatalf("bad header %x", rec[:10])
	}
	text := ebcdic.MustGet(37).EncodeString("FAKE HOST  Enter received")
	if !bytes.Contains(rec, text) {
		t.Fatal("text missing")
	}
	if !bytes.Contains(rec, []byte{0x04, 0x40, 0x04, 0x11, 0x00, 0x00, 0x11, 0x01, 0x01}) {
		t.Fatal("orders missing")
	}
}

func TestRecordParser(t *testing.T) {
	var p recordParser
	n := 0
	// negotiation noise, then a record containing an escaped 0xff, split across feeds
	p.feed([]byte{0xff, 0xfb, 0x19, 0xff, 0xfa, 0x18, 0x00, 0xff, 0xf0, 0x00, 0x05}, func() { n++ })
	p.feed([]byte{0xff, 0xff, 0xff, 0xef}, func() { n++ })
	p.feed([]byte{0xff, 0xef}, func() { n++ }) // empty record: ignored
	if n != 1 {
		t.Fatalf("records = %d want 1", n)
	}
}
