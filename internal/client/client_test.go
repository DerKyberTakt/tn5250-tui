package client

import (
	"bytes"
	"context"
	"crypto/tls"
	"net"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func echoServer(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		buf := make([]byte, 1024)
		for {
			n, err := c.Read(buf)
			if n > 0 {
				_, _ = c.Write(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()
	t.Cleanup(func() { ln.Close() })
	return ln.Addr().(*net.TCPAddr).Port
}

func TestEchoAndTrace(t *testing.T) {
	port := echoServer(t)
	got := make(chan []byte, 4)
	closed := make(chan error, 1)
	tr := &syncBuf{}
	c, err := Dial(context.Background(), Options{
		Host: "127.0.0.1", Port: port, TimeoutMs: 2000, KeepAlive: true, Trace: tr,
		OnData:   func(b []byte) { got <- append([]byte(nil), b...) },
		OnClosed: func(err error) { closed <- err },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.WriteTraced([]byte{0x01, 0x02, 0xff}, []byte{0x01, 0x00, 0x00}); err != nil {
		t.Fatal(err)
	}
	select {
	case b := <-got:
		if !bytes.Equal(b, []byte{1, 2, 0xff}) {
			t.Fatalf("echo %x", b)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no echo")
	}
	_ = c.Close()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("close err %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no OnClosed")
	}
	lines := strings.Split(strings.TrimSpace(tr.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("trace %q", tr.String())
	}
	re := regexp.MustCompile(`^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d\.\d{3} (IN |OUT) [0-9a-f]+$`)
	for _, l := range lines {
		if !re.MatchString(l) {
			t.Fatalf("bad trace line %q", l)
		}
	}
	if !strings.HasSuffix(lines[0], "OUT 010000") {
		t.Fatalf("outgoing trace must show the blanked copy: %q", lines[0])
	}
	if !strings.HasSuffix(lines[1], "IN  0102ff") {
		t.Fatalf("incoming trace %q", lines[1])
	}
}

func TestServerCloseReportsNil(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, _ := ln.Accept()
		if c != nil {
			c.Close()
		}
	}()
	closed := make(chan error, 1)
	_, err = Dial(context.Background(), Options{Host: "127.0.0.1", Port: ln.Addr().(*net.TCPAddr).Port,
		OnClosed: func(err error) { closed <- err }})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("err %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no OnClosed")
	}
}

func TestDialRefused(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	if _, err := Dial(context.Background(), Options{Host: "127.0.0.1", Port: port, TimeoutMs: 500}); err == nil {
		t.Fatal("expected error")
	}
	if _, err := Dial(context.Background(), Options{}); err == nil {
		t.Fatal("expected no-host error")
	}
}

func TestSetTraceLater(t *testing.T) {
	port := echoServer(t)
	got := make(chan struct{}, 1)
	c, err := Dial(context.Background(), Options{Host: "127.0.0.1", Port: port, OnData: func([]byte) { got <- struct{}{} }})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	tr := &syncBuf{}
	c.SetTrace(tr)
	_ = c.Write([]byte("hi"))
	<-got
	c.SetTrace(nil)
	if !strings.Contains(tr.String(), "OUT 6869") {
		t.Fatalf("trace %q", tr.String())
	}
}

func TestVerifyPeerNoCert(t *testing.T) {
	if err := verifyPeer(true, "PUB400.COM")(tls.ConnectionState{}); err == nil {
		t.Fatal("no certificate must be refused even when trusting any")
	}
}

// The keep-alive period matches the TypeScript client (15 s).
func TestKeepAliveIntervalIs15Seconds(t *testing.T) {
	if KeepAliveInterval != 15*time.Second {
		t.Errorf("KeepAliveInterval = %v, want 15s", KeepAliveInterval)
	}
}
