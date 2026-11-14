// Package fakehost is a TCP server that plays a 5250 host from a capture file, so the
// engine and the TUI can be tested and demoed without an IBM i.
//
// A capture is a list of events: "in" = bytes the real host sent, "out" = what the terminal
// answered. For each connection the server sends every "in" chunk in order and, for every
// "out" chunk, waits until the client has sent matching bytes (byte for byte; the client
// may chunk them differently). After the last event the connection stays open and every
// record the client sends is answered with a canned "Enter received" screen.
package fakehost

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"sync"
	"time"

	"github.com/DerKyberTakt/tn5250-tui/internal/ebcdic"
)

// DefaultTimeout is how long the server waits for the client to send an expected "out" chunk.
const DefaultTimeout = 5 * time.Second

// Telnet bytes.
const (
	iac = 0xff
	eor = 0xef
	sb  = 0xfa
	se  = 0xf0
)

// Event is one capture entry.
type Event struct {
	T   int    `json:"t"`
	Dir string `json:"dir"` // "in" (host to terminal) or "out" (terminal to host)
	Hex string `json:"hex"`
}

// Capture is a recorded session.
type Capture struct {
	Description string  `json:"description"`
	Port        int     `json:"port"`
	Term        string  `json:"term"`
	Events      []Event `json:"events"`

	data [][]byte // decoded hex per event (filled by prepare)
}

// LoadCapture reads a capture JSON file.
func LoadCapture(path string) (Capture, error) {
	var c Capture
	b, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return c, fmt.Errorf("%s: %w", path, err)
	}
	if err := c.prepare(); err != nil {
		return c, fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}

// prepare decodes and validates the events.
func (c *Capture) prepare() error {
	c.data = make([][]byte, len(c.Events))
	for i, e := range c.Events {
		if e.Dir != "in" && e.Dir != "out" {
			return fmt.Errorf("event %d: dir %q is not in/out", i, e.Dir)
		}
		b, err := hex.DecodeString(e.Hex)
		if err != nil {
			return fmt.Errorf("event %d: %w", i, err)
		}
		c.data[i] = b
	}
	return nil
}

// Options tune a server.
type Options struct {
	// Timeout for each expected "out" chunk (default DefaultTimeout).
	Timeout time.Duration
	// Lenient records a mismatch or timeout but keeps replaying instead of closing the
	// connection. Useful for demos where the client negotiates differently from the capture.
	Lenient bool
}

// Mismatch describes one place where the client did not send what the capture expected.
type Mismatch struct {
	Conn   int    // connection number, from 1
	Event  int    // index in Capture.Events
	Offset int    // first differing byte inside the expected chunk
	Want   []byte // the expected chunk
	Got    []byte // what had arrived (may be shorter than Want on a timeout)
}

func (m Mismatch) String() string {
	return fmt.Sprintf("conn %d event %d: first difference at offset %d; want %x got %x",
		m.Conn, m.Event, m.Offset, m.Want, m.Got)
}

// Server is a running fake host.
type Server struct {
	// Logf receives log lines (default log.Printf). Set it before clients connect.
	Logf func(format string, args ...any)

	ln      net.Listener
	cap     Capture
	opt     Options
	mu      sync.Mutex
	conns   map[net.Conn]struct{}
	nconn   int
	mism    []Mismatch
	wg      sync.WaitGroup
	closing bool
}

// Start listens on listenAddr (use "127.0.0.1:0" for a free port) and serves the capture
// strictly: a mismatch closes the connection.
func Start(listenAddr string, capture Capture) (*Server, error) {
	return StartWith(listenAddr, capture, Options{})
}

// StartWith is Start with options.
func StartWith(listenAddr string, capture Capture, opt Options) (*Server, error) {
	if capture.data == nil || len(capture.data) != len(capture.Events) {
		if err := capture.prepare(); err != nil {
			return nil, err
		}
	}
	if opt.Timeout <= 0 {
		opt.Timeout = DefaultTimeout
	}
	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return nil, err
	}
	s := &Server{Logf: log.Printf, ln: ln, cap: capture, opt: opt, conns: map[net.Conn]struct{}{}}
	s.wg.Add(1)
	go s.accept()
	return s, nil
}

// Addr is the address the server listens on.
func (s *Server) Addr() string { return s.ln.Addr().String() }

// Close stops listening, closes every connection and waits for the goroutines.
func (s *Server) Close() error {
	s.mu.Lock()
	s.closing = true
	for c := range s.conns {
		_ = c.Close()
	}
	s.mu.Unlock()
	err := s.ln.Close()
	s.wg.Wait()
	return err
}

// Mismatches returns a copy of the mismatches recorded so far (all connections).
func (s *Server) Mismatches() []Mismatch {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Mismatch(nil), s.mism...)
}

func (s *Server) logf(format string, args ...any) {
	if s.Logf != nil {
		s.Logf(format, args...)
	}
}

func (s *Server) accept() {
	defer s.wg.Done()
	for {
		c, err := s.ln.Accept()
		if err != nil {
			return
		}
		s.mu.Lock()
		if s.closing {
			s.mu.Unlock()
			_ = c.Close()
			return
		}
		s.nconn++
		n := s.nconn
		s.conns[c] = struct{}{}
		s.wg.Add(1)
		s.mu.Unlock()
		go func() {
			defer s.wg.Done()
			s.serve(c, n)
			_ = c.Close()
			s.mu.Lock()
			delete(s.conns, c)
			s.mu.Unlock()
		}()
	}
}

func (s *Server) record(m Mismatch) {
	s.mu.Lock()
	s.mism = append(s.mism, m)
	s.mu.Unlock()
	s.logf("fakehost: %s", m)
}

// serve replays the capture on one connection, then answers records with the canned screen.
func (s *Server) serve(c net.Conn, n int) {
	var pending []byte // bytes received but not yet matched
	buf := make([]byte, 4096)
	for i, ev := range s.cap.Events {
		want := s.cap.data[i]
		if ev.Dir == "in" {
			if _, err := c.Write(want); err != nil {
				return
			}
			continue
		}
		deadline := time.Now().Add(s.opt.Timeout)
		for len(pending) < len(want) {
			// Fail early on a wrong prefix instead of waiting out the timeout.
			if !bytes.Equal(pending, want[:len(pending)]) {
				break
			}
			_ = c.SetReadDeadline(deadline)
			r, err := c.Read(buf)
			pending = append(pending, buf[:r]...)
			if err != nil {
				if ne, ok := err.(net.Error); ok && ne.Timeout() {
					break
				}
				return // closed
			}
		}
		_ = c.SetReadDeadline(time.Time{})
		if len(pending) >= len(want) && bytes.Equal(pending[:len(want)], want) {
			pending = pending[len(want):]
			continue
		}
		k := len(pending)
		if k > len(want) {
			k = len(want)
		}
		off := 0
		for off < k && pending[off] == want[off] {
			off++
		}
		got := append([]byte(nil), pending...)
		s.record(Mismatch{Conn: n, Event: i, Offset: off, Want: want, Got: got})
		if !s.opt.Lenient {
			return
		}
		pending = nil
	}

	// After the last event: answer every record the client sends.
	var p recordParser
	p.feed(pending, func() { _, _ = c.Write(EnterScreen()) })
	for {
		r, err := c.Read(buf)
		if r > 0 {
			ok := true
			p.feed(buf[:r], func() {
				if _, werr := c.Write(EnterScreen()); werr != nil {
					ok = false
				}
			})
			if !ok {
				return
			}
		}
		if err != nil {
			if err != io.EOF {
				s.mu.Lock()
				closing := s.closing
				s.mu.Unlock()
				if !closing {
					s.logf("fakehost: conn %d: %v", n, err)
				}
			}
			return
		}
	}
}

// recordParser splits a client byte stream on IAC EOR, skipping telnet commands.
type recordParser struct {
	state int // 0 data, 1 after IAC, 2 after IAC WILL/WONT/DO/DONT, 3 inside SB, 4 IAC inside SB
	data  int // data bytes seen in the current record
}

func (p *recordParser) feed(b []byte, onRecord func()) {
	for _, x := range b {
		switch p.state {
		case 0:
			if x == iac {
				p.state = 1
			} else {
				p.data++
			}
		case 1:
			switch {
			case x == iac:
				p.data++
				p.state = 0
			case x == eor:
				p.state = 0
				if p.data > 0 {
					onRecord()
				}
				p.data = 0
			case x == sb:
				p.state = 3
			case x >= 0xfb && x <= 0xfe:
				p.state = 2
			default:
				p.state = 0
			}
		case 2:
			p.state = 0
		case 3:
			if x == iac {
				p.state = 4
			}
		case 4:
			if x == se {
				p.state = 0
			} else {
				p.state = 3
			}
		}
	}
}

// EnterScreen is the canned reply: a GDS record with Clear Unit and a Write To Display
// that puts "FAKE HOST  Enter received" at row 1 column 1, followed by IAC EOR.
func EnterScreen() []byte {
	cp := ebcdic.MustGet(37)
	data := []byte{0x04, 0x40} // ESC CLEAR_UNIT
	data = append(data, 0x04, 0x11, 0x00, 0x00)
	data = append(data, 0x11, 0x01, 0x01) // SBA row 1 col 1
	data = append(data, cp.EncodeString("FAKE HOST  Enter received")...)
	n := 10 + len(data)
	rec := []byte{byte(n >> 8), byte(n), 0x12, 0xa0, 0x00, 0x00, 0x04, 0x00, 0x00, 0x03}
	rec = append(rec, data...)
	out := make([]byte, 0, len(rec)+8)
	for _, b := range rec {
		out = append(out, b)
		if b == iac {
			out = append(out, iac)
		}
	}
	return append(out, iac, eor)
}
