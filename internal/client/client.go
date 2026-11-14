// Package client is the network side of the terminal: one TCP or TLS
// connection with a reader goroutine and an optional hex trace.
package client

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"time"
)

// KeepAliveInterval is the TCP keep-alive probe period (15 s, as the TypeScript client).
const KeepAliveInterval = 15 * time.Second

// Options describes one connection attempt.
type Options struct {
	Host                string
	Port                int
	TLS                 bool
	TrustAnyCertificate bool          // accept any certificate (self-signed hosts)
	TimeoutMs           int           // dial timeout, 0 = 15 s
	KeepAlive           bool          // TCP keep-alive probes
	KeepAliveEvery      time.Duration // probe period; 0 = KeepAliveInterval

	// OnData receives every chunk read from the socket, on the reader goroutine.
	// The slice is only valid during the call.
	OnData func([]byte)
	// OnClosed is called once, on the reader goroutine, when the connection ends.
	// err is nil for a clean close by either side.
	OnClosed func(err error)
	// Trace, when set before Dial, receives the hex trace from the first byte.
	Trace io.Writer
}

// Conn is an open connection.
type Conn struct {
	nc       net.Conn
	opts     Options
	mu       sync.Mutex // guards trace and closed
	trace    io.Writer
	closed   bool
	wmu      sync.Mutex // serialises writes
	doneOnce sync.Once
}

// Dial connects and starts the reader goroutine.
func Dial(ctx context.Context, o Options) (*Conn, error) {
	if o.Host == "" {
		return nil, errors.New("no host")
	}
	if o.Port == 0 {
		o.Port = 23
	}
	timeout := 15 * time.Second
	if o.TimeoutMs > 0 {
		timeout = time.Duration(o.TimeoutMs) * time.Millisecond
	}
	d := &net.Dialer{Timeout: timeout}
	if o.KeepAlive {
		d.KeepAlive = KeepAliveInterval
		if o.KeepAliveEvery > 0 {
			d.KeepAlive = o.KeepAliveEvery
		}
	} else {
		d.KeepAlive = -1
	}
	addr := net.JoinHostPort(o.Host, strconv.Itoa(o.Port))
	var nc net.Conn
	var err error
	if o.TLS {
		td := &tls.Dialer{NetDialer: d, Config: &tls.Config{
			ServerName:         o.Host,
			InsecureSkipVerify: true, // verification happens in VerifyConnection below
			VerifyConnection:   verifyPeer(o.TrustAnyCertificate, o.Host),
			MinVersion:         tls.VersionTLS12,
		}}
		nc, err = td.DialContext(ctx, "tcp", addr)
	} else {
		nc, err = d.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", addr, err)
	}
	c := &Conn{nc: nc, opts: o, trace: o.Trace}
	go c.readLoop()
	return c, nil
}

// verifyPeer accepts any certificate when trustAny, else verifies the chain
// against the system roots and the host name.
func verifyPeer(trustAny bool, host string) func(tls.ConnectionState) error {
	return func(cs tls.ConnectionState) error {
		if len(cs.PeerCertificates) == 0 {
			return errors.New("certificate refused: none presented")
		}
		if trustAny {
			return nil
		}
		roots, err := x509.SystemCertPool()
		if err != nil || roots == nil {
			roots = x509.NewCertPool()
		}
		vo := x509.VerifyOptions{DNSName: host, Roots: roots, Intermediates: x509.NewCertPool()}
		for _, c := range cs.PeerCertificates[1:] {
			vo.Intermediates.AddCert(c)
		}
		if _, err := cs.PeerCertificates[0].Verify(vo); err != nil {
			return fmt.Errorf("certificate refused: %w (use the site option \"Trust any certificate\" for a self-signed host)", err)
		}
		return nil
	}
}

func (c *Conn) readLoop() {
	buf := make([]byte, 64*1024)
	var rerr error
	for {
		n, err := c.nc.Read(buf)
		if n > 0 {
			c.traceLine("IN ", buf[:n])
			if c.opts.OnData != nil {
				c.opts.OnData(buf[:n])
			}
		}
		if err != nil {
			rerr = err
			break
		}
	}
	c.mu.Lock()
	local := c.closed
	c.closed = true
	c.mu.Unlock()
	if local || errors.Is(rerr, io.EOF) || errors.Is(rerr, net.ErrClosed) {
		rerr = nil
	}
	_ = c.nc.Close()
	c.doneOnce.Do(func() {
		if c.opts.OnClosed != nil {
			c.opts.OnClosed(rerr)
		}
	})
}

// SetTrace sets (or with nil clears) the writer that receives one hex line per
// chunk: "<local time, millis> IN |OUT <hex>".
func (c *Conn) SetTrace(w io.Writer) {
	c.mu.Lock()
	c.trace = w
	c.mu.Unlock()
}

func (c *Conn) traceLine(dir string, b []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.trace == nil {
		return
	}
	_, _ = fmt.Fprintf(c.trace, "%s %s %s\n", time.Now().Format("2006-01-02T15:04:05.000"), dir, hex.EncodeToString(b))
}

// Write sends b on the socket and traces it.
func (c *Conn) Write(b []byte) error { return c.WriteTraced(b, nil) }

// WriteTraced sends b on the socket; the trace shows forTrace instead of b
// when forTrace is not nil (the same frame with secrets blanked).
func (c *Conn) WriteTraced(b, forTrace []byte) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if forTrace == nil {
		forTrace = b
	}
	c.traceLine("OUT", forTrace)
	_, err := c.nc.Write(b)
	return err
}

// Close closes the socket; OnClosed fires once with a nil error.
func (c *Conn) Close() error {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	return c.nc.Close()
}

// RemoteAddr returns the peer address.
func (c *Conn) RemoteAddr() net.Addr { return c.nc.RemoteAddr() }
