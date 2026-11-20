package viewer

import (
	"bufio"
	"encoding/json"
	"errors"
	"net"
	"sync"
	"time"
)

// Source is what the server shows: the MCP server's session manager behind an interface
// so this package needs nothing from it.
type Source interface {
	// Sessions lists the sessions.
	Sessions() []SessionInfo
	// Frame returns the current screen of a session; ok is false for an unknown one.
	Frame(session string) (Frame, bool)
	// Subscribe returns a channel that receives a value after every change of the
	// session (coalesced), a cancel function, and ok=false for an unknown session.
	Subscribe(session string) (ch <-chan struct{}, cancel func(), ok bool)
	// End signs a session off (when asked and a command line is on its screen) and closes
	// it. It is the one request that changes anything; the token is what authorises it.
	End(session string, signoff bool) (signedOff bool, err error)
}

// Server accepts viewers on a loopback listener.
type Server struct {
	ln    net.Listener
	token string
	src   Source

	mu     sync.Mutex
	closed bool
	conns  map[net.Conn]struct{}
}

// Listen starts a server on 127.0.0.1 with a random port and the given token.
func Listen(token string, src Source) (*Server, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	s := &Server{ln: ln, token: token, src: src, conns: map[net.Conn]struct{}{}}
	go s.accept()
	return s, nil
}

// Addr is the listening address (127.0.0.1:port).
func (s *Server) Addr() string { return s.ln.Addr().String() }

// Close stops the listener and drops every viewer.
func (s *Server) Close() {
	s.mu.Lock()
	s.closed = true
	conns := make([]net.Conn, 0, len(s.conns))
	for c := range s.conns {
		conns = append(conns, c)
	}
	s.mu.Unlock()
	_ = s.ln.Close()
	for _, c := range conns {
		_ = c.Close()
	}
}

func (s *Server) accept() {
	for {
		c, err := s.ln.Accept()
		if err != nil {
			return
		}
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			_ = c.Close()
			return
		}
		s.conns[c] = struct{}{}
		s.mu.Unlock()
		go s.serve(c)
	}
}

func (s *Server) forget(c net.Conn) {
	s.mu.Lock()
	delete(s.conns, c)
	s.mu.Unlock()
	_ = c.Close()
}

// maxLine is the longest request line the server reads, before and after the hello. The
// token and every request are tiny; an endless line must not be buffered.
const maxLine = 4096

// errLineTooLong is returned by readLine for a line longer than maxLine.
var errLineTooLong = errors.New("viewer: request line too long")

// readLine reads one newline-terminated line of at most maxLine bytes (the reader's buffer
// size); a longer line is an error and the caller closes the connection.
func readLine(r *bufio.Reader) ([]byte, error) {
	line, err := r.ReadSlice('\n')
	if errors.Is(err, bufio.ErrBufferFull) {
		return nil, errLineTooLong
	}
	if err != nil {
		return nil, err
	}
	return append([]byte(nil), line...), nil
}

// serve runs one viewer connection: a hello with the token first, then list / watch
// requests; a watch streams frames until the viewer sends another request or leaves.
func (s *Server) serve(c net.Conn) {
	defer s.forget(c)
	r := bufio.NewReaderSize(c, maxLine)
	var wmu sync.Mutex
	send := func(m Msg) error {
		data, err := json.Marshal(m)
		if err != nil {
			return err
		}
		wmu.Lock()
		defer wmu.Unlock()
		_ = c.SetWriteDeadline(time.Now().Add(10 * time.Second))
		_, err = c.Write(append(data, '\n'))
		return err
	}
	_ = c.SetReadDeadline(time.Now().Add(10 * time.Second))
	var hello Msg
	if line, err := readLine(r); err != nil || json.Unmarshal(line, &hello) != nil || hello.T != "hello" || hello.Token != s.token {
		_ = send(Msg{T: "error", Text: "bad token"})
		return
	}
	_ = c.SetReadDeadline(time.Time{})
	_ = send(Msg{T: "sessions", Sessions: s.src.Sessions()})

	var stopWatch func()
	defer func() {
		if stopWatch != nil {
			stopWatch()
		}
	}()
	for {
		line, err := readLine(r)
		if err != nil {
			return
		}
		var m Msg
		if json.Unmarshal(line, &m) != nil {
			_ = send(Msg{T: "error", Text: "bad request"})
			continue
		}
		if stopWatch != nil {
			stopWatch()
			stopWatch = nil
		}
		switch m.T {
		case "list":
			_ = send(Msg{T: "sessions", Sessions: s.src.Sessions()})
		case "watch":
			ch, cancel, ok := s.src.Subscribe(m.Session)
			if !ok {
				_ = send(Msg{T: "error", Text: "no session " + m.Session})
				continue
			}
			done := make(chan struct{})
			var once sync.Once
			stopWatch = func() {
				once.Do(func() { cancel(); close(done) })
			}
			session := m.Session
			go func() {
				push := func() bool {
					f, ok := s.src.Frame(session)
					if !ok {
						_ = send(Msg{T: "closed", Session: session})
						return false
					}
					return send(Msg{T: "frame", Frame: &f}) == nil
				}
				if !push() {
					return
				}
				for {
					select {
					case <-done:
						return
					case _, ok := <-ch:
						if !ok {
							_ = send(Msg{T: "closed", Session: session})
							return
						}
						if !push() {
							return
						}
					}
				}
			}()
		case "end":
			signedOff, err := s.src.End(m.Session, m.Signoff)
			if err != nil {
				_ = send(Msg{T: "error", Text: err.Error()})
				continue
			}
			_ = send(Msg{T: "ended", Session: m.Session, Signoff: signedOff})
		case "bye":
			return
		default:
			_ = send(Msg{T: "error", Text: "unknown request " + m.T})
		}
	}
}

// ErrBadToken is returned by Dial when the server refuses the token.
var ErrBadToken = errors.New("the server refused the token")
