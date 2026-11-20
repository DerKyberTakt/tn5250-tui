package viewer

import (
	"bufio"
	"encoding/json"
	"errors"
	"net"
	"sync"
	"time"
)

// Client is a viewer's connection to a server.
type Client struct {
	c  net.Conn
	r  *bufio.Reader
	mu sync.Mutex // one request at a time

	// sessions is the list the server sent with its greeting.
	sessions []SessionInfo
}

// Dial connects, sends the token and reads the greeting (the session list).
func Dial(addr, token string, timeout time.Duration) (*Client, error) {
	c, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return nil, err
	}
	cl := &Client{c: c, r: bufio.NewReader(c)}
	if err := cl.send(Msg{T: "hello", Token: token}); err != nil {
		_ = c.Close()
		return nil, err
	}
	_ = c.SetReadDeadline(time.Now().Add(timeout))
	m, err := cl.read()
	_ = c.SetReadDeadline(time.Time{})
	if err != nil {
		_ = c.Close()
		return nil, err
	}
	if m.T == "error" {
		_ = c.Close()
		if m.Text == "bad token" {
			return nil, ErrBadToken
		}
		return nil, errors.New(m.Text)
	}
	cl.sessions = m.Sessions
	return cl, nil
}

// Close ends the connection.
func (cl *Client) Close() {
	_ = cl.send(Msg{T: "bye"})
	_ = cl.c.Close()
}

func (cl *Client) send(m Msg) error {
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	_, err = cl.c.Write(append(data, '\n'))
	return err
}

func (cl *Client) read() (Msg, error) {
	line, err := cl.r.ReadBytes('\n')
	if err != nil {
		return Msg{}, err
	}
	var m Msg
	if err := json.Unmarshal(line, &m); err != nil {
		return Msg{}, err
	}
	m.Text, m.Sessions = Sanitize(m.Text), sanitizeInfo(m.Sessions)
	return m, nil
}

// Sessions asks the server for its session list (the greeting's list when it was never
// asked again).
func (cl *Client) Sessions() ([]SessionInfo, error) {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	if err := cl.send(Msg{T: "list"}); err != nil {
		return nil, err
	}
	for {
		m, err := cl.read()
		if err != nil {
			return nil, err
		}
		switch m.T {
		case "sessions":
			cl.sessions = m.Sessions
			return m.Sessions, nil
		case "error":
			return nil, errors.New(m.Text)
		}
		// a late frame of an earlier watch: skip
	}
}

// Watch streams the frames of a session until stop is called or the server says the
// session closed (the channel is then closed; err holds the reason, nil for a close).
func (cl *Client) Watch(session string) (frames <-chan Frame, stop func(), errc <-chan error) {
	out := make(chan Frame, 4)
	ec := make(chan error, 1)
	done := make(chan struct{})
	var once sync.Once
	stopf := func() { once.Do(func() { close(done); _ = cl.c.SetReadDeadline(time.Now()) }) }
	go func() {
		defer close(out)
		cl.mu.Lock()
		defer cl.mu.Unlock()
		if err := cl.send(Msg{T: "watch", Session: session}); err != nil {
			ec <- err
			return
		}
		for {
			m, err := cl.read()
			if err != nil {
				select {
				case <-done:
					_ = cl.c.SetReadDeadline(time.Time{})
					ec <- nil
				default:
					ec <- err
				}
				return
			}
			switch m.T {
			case "frame":
				if m.Frame != nil && m.Frame.Session == session { // frames of an earlier watch may still arrive
					select {
					case out <- *m.Frame:
					case <-done:
						_ = cl.c.SetReadDeadline(time.Time{})
						ec <- nil
						return
					}
				}
			case "closed":
				if m.Session != session {
					continue
				}
				ec <- nil
				return
			case "error":
				ec <- errors.New(m.Text)
				return
			}
		}
	}()
	return out, stopf, ec
}
