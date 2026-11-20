package viewer

import (
	"bytes"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/DerKyberTakt/tn5250-tui/internal/sites"
	"github.com/DerKyberTakt/tn5250-tui/internal/tn5250"
)

// fakeSource holds one session whose screen the test changes.
type fakeSource struct {
	mu   sync.Mutex
	snap tn5250.Snapshot
	oia  tn5250.OIA
	subs []chan struct{}
	gone bool
}

func newFake() *fakeSource {
	s := tn5250.Snapshot{Rows: 24, Cols: 80, Seq: 1}
	s.Cells = make([]tn5250.Cell, 24*80)
	for i := range s.Cells {
		s.Cells[i] = tn5250.Cell{Ch: ' ', Attr: 0x20}
	}
	for i, r := range "Sign On" {
		s.Cells[i+30] = tn5250.Cell{Ch: r, Attr: 0x22}
	}
	s.Cells[5*80+20] = tn5250.Cell{Ch: ' ', Attr: 0x24, InField: true}
	s.Fields = []tn5250.FieldInfo{{Start: 5*80 + 20, Length: 10}}
	s.Cursor.Row, s.Cursor.Col = 5, 20
	return &fakeSource{snap: s, oia: tn5250.OIA{Connected: true, InputInhibited: "none", DeviceName: "QPADEV0001"}}
}

func (f *fakeSource) Sessions() []SessionInfo {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.gone {
		return nil
	}
	return []SessionInfo{{Session: "s1", Host: "PUB400.COM", Device: "QPADEV0001", Connected: true, Seq: f.snap.Seq}}
}

func (f *fakeSource) Frame(session string) (Frame, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if session != "s1" || f.gone {
		return Frame{}, false
	}
	return EncodeFrame("s1", f.snap, f.oia), true
}

func (f *fakeSource) Subscribe(session string) (<-chan struct{}, func(), bool) {
	if session != "s1" {
		return nil, nil, false
	}
	ch := make(chan struct{}, 1)
	f.mu.Lock()
	f.subs = append(f.subs, ch)
	f.mu.Unlock()
	return ch, func() {}, true
}

func (f *fakeSource) change(text string) {
	f.mu.Lock()
	for i, r := range text {
		f.snap.Cells[10*80+i] = tn5250.Cell{Ch: r, Attr: 0x20}
	}
	f.snap.Seq++
	subs := f.subs
	f.mu.Unlock()
	for _, ch := range subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

func (f *fakeSource) close() {
	f.mu.Lock()
	f.gone = true
	subs := f.subs
	f.subs = nil
	f.mu.Unlock()
	for _, ch := range subs {
		close(ch)
	}
}

func TestFrameRoundTrip(t *testing.T) {
	f := newFake()
	fr := EncodeFrame("s1", f.snap, f.oia)
	s := fr.Snapshot()
	if s.Rows != 24 || s.Cols != 80 || s.Seq != 1 || s.Cursor.Row != 5 || s.Cursor.Col != 20 {
		t.Fatalf("header: %+v", s)
	}
	if c := s.Cells[30]; c.Ch != 'S' || c.Attr != 0x22 {
		t.Errorf("cell 30: %+v", c)
	}
	if c := s.Cells[5*80+20]; !c.InField || c.Attr != 0x24 {
		t.Errorf("field cell: %+v", c)
	}
	if len(s.Fields) != 1 || s.Fields[0].Start != 5*80+20 {
		t.Errorf("fields: %+v", s.Fields)
	}
	if o := fr.TUIOIA(); !o.Connected || o.DeviceName != "QPADEV0001" {
		t.Errorf("oia: %+v", o)
	}
	if e := (Frame{}).Snapshot(); e.Rows != 0 || e.Cells != nil {
		t.Errorf("empty frame: %+v", e)
	}
}

func TestServerClientWatch(t *testing.T) {
	src := newFake()
	srv, err := Listen("tok", src)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	if _, err := Dial(srv.Addr(), "wrong", time.Second); err != ErrBadToken {
		t.Fatalf("bad token: %v", err)
	}
	cl, err := Dial(srv.Addr(), "tok", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()
	if len(cl.sessions) != 1 || cl.sessions[0].Session != "s1" {
		t.Fatalf("greeting: %+v", cl.sessions)
	}
	list, err := cl.Sessions()
	if err != nil || len(list) != 1 || list[0].Host != "PUB400.COM" {
		t.Fatalf("list: %v %+v", err, list)
	}
	frames, stop, errc := cl.Watch("s1")
	first := <-frames
	if first.Seq != 1 || first.Text[0][30:37] != "Sign On" {
		t.Fatalf("first frame: %+v", first)
	}
	src.change("HELLO")
	second := <-frames
	if second.Seq != 2 || second.Text[10][:5] != "HELLO" {
		t.Fatalf("second frame: seq %d %q", second.Seq, second.Text[10][:5])
	}
	stop()
	for range frames {
	}
	if err := <-errc; err != nil {
		t.Fatalf("stop: %v", err)
	}
	// A watch of an unknown session is an error; a closed session ends the stream.
	_, _, errc = cl.Watch("nope")
	if err := <-errc; err == nil {
		t.Fatal("unknown session accepted")
	}
	frames, _, errc = cl.Watch("s1")
	<-frames
	src.close()
	for range frames {
	}
	if err := <-errc; err != nil {
		t.Fatalf("closed: %v", err)
	}
}

func TestViewerRenders(t *testing.T) {
	src := newFake()
	srv, err := Listen("tok", src)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	cl, err := Dial(srv.Addr(), "tok", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()
	sim := tcell.NewSimulationScreen("UTF-8")
	if err := sim.Init(); err != nil {
		t.Fatal(err)
	}
	sim.SetSize(80, 25)
	v := New(cl, "test")
	v.SetScreen(sim)
	done := make(chan error, 1)
	go func() { done <- v.Run("s1") }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		v.mu.Lock()
		have := v.haveFrm
		v.mu.Unlock()
		if have {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no frame")
		}
		time.Sleep(10 * time.Millisecond)
	}
	src.change("HELLO")
	time.Sleep(100 * time.Millisecond)
	left, _, _ := v.oiaSource()
	if left[:9] != "WATCH s1 " {
		t.Errorf("oia: %q", left)
	}
	snap, ok := v.screenSource()
	if !ok || snap.Rows != 24 || string(snap.Cells[10*80].Ch) != "H" {
		t.Errorf("screen: ok=%v %+v", ok, snap.Rows)
	}
	v.Stop()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestTokenFiles(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(sites.EnvConfig, filepath.Join(dir, "sites.json"))
	if TokenDir() != filepath.Join(dir, "agent") {
		t.Fatalf("dir: %s", TokenDir())
	}
	src := newFake()
	srv, _ := Listen("tok", src)
	defer srv.Close()
	live := &TokenFile{PID: 1, Addr: srv.Addr(), Token: "tok", Started: time.Now(), Label: "live"}
	if _, err := live.Write(); err != nil {
		t.Fatal(err)
	}
	dead := &TokenFile{PID: 2, Addr: "127.0.0.1:1", Token: "x", Started: time.Now().Add(-time.Hour)}
	if _, err := dead.Write(); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(TokenDir(), "junk.json"), []byte("{"), 0o600)
	all, err := ListTokens(false)
	if err != nil || len(all) != 2 || all[0].PID != 1 {
		t.Fatalf("unprobed: %v %+v", err, all)
	}
	alive, err := ListTokens(true)
	if err != nil || len(alive) != 1 || alive[0].PID != 1 || alive[0].Label != "live" {
		t.Fatalf("probed: %v %+v", err, alive)
	}
	if _, err := os.Stat(dead.path); !os.IsNotExist(err) {
		t.Error("dead entry not removed")
	}
	live.Remove()
	if rest, _ := ListTokens(false); len(rest) != 0 {
		t.Errorf("remove: %+v", rest)
	}
	if n := NewToken(); len(n) != 32 {
		t.Errorf("token: %q", n)
	}
	if s := Describe(*live); s == "" {
		t.Error("describe")
	}
}

// A request line without a newline is not buffered without limit: before and after the hello
// the server closes the connection once the line passes maxLine.
func TestOversizedRequestLineClosesConnection(t *testing.T) {
	srv, err := Listen("tok", newFake())
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	flood := func(t *testing.T, c net.Conn) {
		t.Helper()
		_ = c.SetDeadline(time.Now().Add(3 * time.Second))
		junk := bytes.Repeat([]byte("a"), 2*maxLine)
		_, _ = c.Write(junk) // may fail once the server has closed
		if _, err := io.Copy(io.Discard, c); err != nil && !errors.Is(err, io.EOF) {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				t.Fatal("the server kept the connection open after an oversized line")
			}
		}
	}
	t.Run("before the hello", func(t *testing.T) {
		c, err := net.Dial("tcp", srv.Addr())
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		flood(t, c)
	})
	t.Run("after the hello", func(t *testing.T) {
		c, err := net.Dial("tcp", srv.Addr())
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		if _, err := c.Write([]byte(`{"t":"hello","token":"tok"}` + "\n")); err != nil {
			t.Fatal(err)
		}
		flood(t, c)
	})
}

// A host-set device name with terminal escapes reaches the listing without control characters.
func TestHostStringsAreSanitized(t *testing.T) {
	if got := Sanitize("QPADEV\x1b]0;pwned\x07\u009b2JX\x7f é"); got != "QPADEV]0;pwned2JX é" {
		t.Errorf("Sanitize: %q", got)
	}
	src := newFake()
	src.snap.Rows = 24
	srv, err := Listen("tok", &evilSource{fakeSource: src})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	cl, err := Dial(srv.Addr(), "tok", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()
	ss, err := cl.Sessions()
	if err != nil || len(ss) != 1 {
		t.Fatalf("%v %v", err, ss)
	}
	for _, v := range []string{ss[0].Host, ss[0].Device, ss[0].Inhibited, cl.sessions[0].Device} {
		if strings.ContainsAny(v, "\x1b\x07\u009b") {
			t.Errorf("control character survived: %q", v)
		}
	}
}

type evilSource struct{ *fakeSource }

func (e *evilSource) Sessions() []SessionInfo {
	return []SessionInfo{{Session: "s1", Host: "h\x1b[2J", Device: "DEV\x1b]0;x\x07", Inhibited: "\u009b", Connected: true}}
}
