package tn5250

import (
	"bytes"
	"testing"
)

// A host that never ends a record, or never ends a subnegotiation, fails the connection
// with a bounded buffer instead of growing it without limit.
func TestTelnetLimits(t *testing.T) {
	flood := func(s *Session, first []byte, total int) {
		s.Feed(first)
		chunk := bytes.Repeat([]byte{'x'}, 64*1024)
		for sent := 0; sent < total; sent += len(chunk) {
			s.Feed(chunk)
		}
	}
	tests := []struct {
		name  string
		first []byte
		total int
	}{
		{"record without IAC EOR", nil, 4 << 20},
		{"subnegotiation without IAC SE", []byte{IAC, TelnetSB, OptNewEnviron}, 4 << 20},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var errs []error
			s, err := New(Options{}, Events{Error: func(e error) { errs = append(errs, e) }})
			if err != nil {
				t.Fatal(err)
			}
			flood(s, tc.first, tc.total)
			if len(errs) != 1 {
				t.Fatalf("want exactly one error event, got %v", errs)
			}
			if s.Failed() == nil {
				t.Fatal("Failed must report the overflow")
			}
			if n := len(s.telnet.rec) + len(s.telnet.pending); n > maxRecord {
				t.Errorf("%d bytes still buffered", n)
			}
			s.Reset()
			if s.Failed() != nil {
				t.Error("Reset must clear the failure for the next connection")
			}
		})
	}
	// Ordinary traffic is untouched: a record just under the cap and a small SB go through.
	s, _ := New(Options{}, Events{})
	s.Feed([]byte{IAC, TelnetSB, OptTerminalType, ttSEND, IAC, TelnetSE})
	s.Feed(bytes.Repeat([]byte{'y'}, maxRecord-10))
	if s.Failed() != nil {
		t.Fatalf("unexpected failure: %v", s.Failed())
	}
}
