package tui

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/DerKyberTakt/tn5250-tui/internal/client"
)

// fakeEngine is a settable-snapshot Engine that records what the TUI does.
type fakeEngine struct {
	mu    sync.Mutex
	snap  Snapshot
	oia   OIA
	calls []string
	send  func(b, forTrace []byte)
	upd   func()
	// onFeed, when set, runs inside Feed (e.g. to answer through send).
	onFeed func(e *fakeEngine, b []byte)
}

func (e *fakeEngine) Feed(b []byte) {
	e.record(fmt.Sprintf("feed:%d", len(b)))
	if e.onFeed != nil {
		e.onFeed(e, b)
	}
}
func (e *fakeEngine) Press(a string) { e.record("press:" + a) }
func (e *fakeEngine) Type(s string)  { e.record("type:" + s) }
func (e *fakeEngine) SetCursor(r, c int) {
	e.record(fmt.Sprintf("cursor:%d,%d", r, c))
	e.mu.Lock()
	e.snap.Cursor.Row, e.snap.Cursor.Col = r, c
	e.mu.Unlock()
}
func (e *fakeEngine) Snapshot() Snapshot { e.mu.Lock(); defer e.mu.Unlock(); return e.snap }
func (e *fakeEngine) OIA(connected bool) OIA {
	e.mu.Lock()
	defer e.mu.Unlock()
	o := e.oia
	o.Connected = connected
	return o
}
func (e *fakeEngine) Reset() { e.record("reset") }

func (e *fakeEngine) record(s string) {
	e.mu.Lock()
	e.calls = append(e.calls, s)
	e.mu.Unlock()
}

func (e *fakeEngine) Calls() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.calls...)
}

func (e *fakeEngine) clear() { e.mu.Lock(); e.calls = nil; e.mu.Unlock() }

// newSnap builds a rows x cols snapshot (blank, attribute 0x20) with text
// placed by put.
func newSnap(rows, cols int) Snapshot {
	s := Snapshot{Rows: rows, Cols: cols, Cells: make([]Cell, rows*cols)}
	for i := range s.Cells {
		s.Cells[i] = Cell{Ch: ' ', Attr: 0x20}
	}
	return s
}

func (s *Snapshot) put(row, col int, text string, attr byte) {
	for i, r := range []rune(text) {
		s.Cells[row*s.Cols+col+i] = Cell{Ch: r, Attr: attr}
	}
}

// signonSnap is a sign-on style screen: user field (visible) and password
// field (non-display).
func signonSnap() Snapshot {
	s := newSnap(24, 80)
	s.put(1, 30, "Sign On", 0x22)
	s.put(5, 20, "User  . . . .", 0x20)
	s.put(7, 20, "Password  . .", 0x20)
	s.Fields = []FieldInfo{
		{Start: 5*80 + 40, Length: 10},
		{Start: 7*80 + 40, Length: 10, Nondisplay: true},
	}
	s.Cursor.Row, s.Cursor.Col = 5, 40
	return s
}

type fakeLink struct {
	mu     sync.Mutex
	writes [][]byte
	traced [][]byte
	closed bool
	trace  io.Writer
}

func (l *fakeLink) Write(b []byte) error { return l.WriteTraced(b, nil) }
func (l *fakeLink) WriteTraced(b, tr []byte) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.writes = append(l.writes, append([]byte(nil), b...))
	l.traced = append(l.traced, append([]byte(nil), tr...))
	return nil
}
func (l *fakeLink) SetTrace(w io.Writer) { l.mu.Lock(); l.trace = w; l.mu.Unlock() }
func (l *fakeLink) Close() error         { l.mu.Lock(); l.closed = true; l.mu.Unlock(); return nil }
func (l *fakeLink) isClosed() bool       { l.mu.Lock(); defer l.mu.Unlock(); return l.closed }

type fakeDialer struct {
	mu    sync.Mutex
	dials []client.Options
	links []*fakeLink
	err   error
}

func (d *fakeDialer) Dial(ctx context.Context, o client.Options) (Link, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.dials = append(d.dials, o)
	if d.err != nil {
		return nil, d.err
	}
	l := &fakeLink{}
	d.links = append(d.links, l)
	return l, nil
}

func (d *fakeDialer) count() int { d.mu.Lock(); defer d.mu.Unlock(); return len(d.dials) }
func (d *fakeDialer) last() (client.Options, *fakeLink) {
	d.mu.Lock()
	defer d.mu.Unlock()
	var l *fakeLink
	if len(d.links) > 0 {
		l = d.links[len(d.links)-1]
	}
	return d.dials[len(d.dials)-1], l
}

// rig is an App wired to a fake engine factory and a fake dialer.
type rig struct {
	a       *App
	d       *fakeDialer
	mu      sync.Mutex
	engines []*fakeEngine
	args    []string // "size/cp/dev" per factory call
	snap    Snapshot // snapshot given to new engines
}

func newRig(t *testing.T, mod func(*AppOptions)) *rig {
	t.Helper()
	t.Setenv("TN5250_TUI_CONFIG", t.TempDir()+"/sites.json")
	r := &rig{d: &fakeDialer{}, snap: signonSnap()}
	opts := AppOptions{
		Dialer: r.d,
		EngineFactory: func(size string, cp int, dev string, send func(b, forTrace []byte), update func()) (Engine, error) {
			e := &fakeEngine{snap: r.snap, send: send, upd: update}
			e.oia.InputInhibited = "none"
			r.mu.Lock()
			r.engines = append(r.engines, e)
			r.args = append(r.args, fmt.Sprintf("%s/%d/%s", size, cp, dev))
			r.mu.Unlock()
			return e, nil
		},
	}
	if mod != nil {
		mod(&opts)
	}
	r.a = New(opts)
	r.a.post = func(f func()) { f() } // no event loop in tests
	return r
}

func (r *rig) engine() *fakeEngine {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.engines) == 0 {
		return nil
	}
	return r.engines[len(r.engines)-1]
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func (r *rig) isConnected() bool {
	r.a.mu.Lock()
	defer r.a.mu.Unlock()
	return r.a.connected
}

func (r *rig) noticeText() string {
	r.a.mu.Lock()
	defer r.a.mu.Unlock()
	return r.a.notice
}

func (r *rig) connect(t *testing.T, target string) {
	t.Helper()
	r.a.RunCommand("connect " + target)
	waitFor(t, "connected", r.isConnected)
}

func key(k tcell.Key, r rune, m tcell.ModMask) *tcell.EventKey { return tcell.NewEventKey(k, r, m) }

func runeKey(r rune) *tcell.EventKey { return key(tcell.KeyRune, r, tcell.ModNone) }

func typeRunes(a *App, s string) {
	for _, r := range s {
		a.handleKey(runeKey(r))
	}
}

func hasCall(calls []string, want string) bool {
	for _, c := range calls {
		if c == want {
			return true
		}
	}
	return false
}

func joinCalls(calls []string) string { return strings.Join(calls, " ") }

type clientOptions = client.Options

// dialerFunc adapts a function to Dialer.
type dialerFunc func(o clientOptions) (Link, error)

func (f dialerFunc) Dial(ctx context.Context, o client.Options) (Link, error) { return f(o) }
