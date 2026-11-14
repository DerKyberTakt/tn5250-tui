package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/DerKyberTakt/tn5250-tui/internal/sites"
)

func TestTypedSignonOrder(t *testing.T) {
	e := &fakeEngine{}
	snap := signonSnap()
	if err := typedSignon(e, snap, "USER1", "SECRET"); err != nil {
		t.Fatal(err)
	}
	want := "cursor:5,40 press:eraseEof type:USER1 cursor:7,40 press:eraseEof type:SECRET press:enter"
	if got := joinCalls(e.Calls()); got != want {
		t.Fatalf("calls\n got %q\nwant %q", got, want)
	}
}

func TestTypedSignonRefuses(t *testing.T) {
	e := &fakeEngine{}
	snap := signonSnap()
	snap.Fields[1].Nondisplay = false // second field is visible text: not a password
	if err := typedSignon(e, snap, "U", "P"); err == nil {
		t.Fatal("must refuse when the second field is not non-display")
	}
	one := signonSnap()
	one.Fields = one.Fields[:1]
	if err := typedSignon(e, one, "U", "P"); err == nil {
		t.Fatal("must refuse with fewer than two input fields")
	}
	bypass := signonSnap()
	bypass.Fields[0].Bypass = true // only one input field left
	if err := typedSignon(e, bypass, "U", "P"); err == nil {
		t.Fatal("bypass fields are not input fields")
	}
	if len(e.Calls()) != 0 {
		t.Fatalf("refused sign-on touched the engine: %v", e.Calls())
	}
}

func siteWithLogin(auto bool) *sites.File {
	f := &sites.File{}
	f.Upsert(sites.Site{Name: "pub", Host: "PUB400.COM", Port: 23, User: "USER1", Password: "SECRET", AutoSignon: auto})
	return f
}

func TestAutoSignonOnceAfterUnlock(t *testing.T) {
	r := newRig(t, func(o *AppOptions) { o.Sites = siteWithLogin(true) })
	r.connect(t, "pub")
	e := r.engine()
	e.clear()
	// keyboard still locked: nothing happens
	e.mu.Lock()
	e.oia.InputInhibited = "system"
	e.mu.Unlock()
	r.a.afterUpdate()
	if len(e.Calls()) != 0 {
		t.Fatalf("signed on while locked: %v", e.Calls())
	}
	e.mu.Lock()
	e.oia.InputInhibited = "none"
	e.mu.Unlock()
	r.a.afterUpdate()
	want := "cursor:5,40 press:eraseEof type:USER1 cursor:7,40 press:eraseEof type:SECRET press:enter"
	if got := joinCalls(e.Calls()); got != want {
		t.Fatalf("calls\n got %q\nwant %q", got, want)
	}
	e.clear()
	r.a.afterUpdate()
	if len(e.Calls()) != 0 {
		t.Fatalf("second sign-on: %v", e.Calls())
	}
}

func TestAutoSignonOffAndGuarded(t *testing.T) {
	// AutoSignon false: never
	r := newRig(t, func(o *AppOptions) { o.Sites = siteWithLogin(false) })
	r.connect(t, "pub")
	r.engine().clear()
	r.a.afterUpdate()
	if len(r.engine().Calls()) != 0 {
		t.Fatalf("auto sign-on although off: %v", r.engine().Calls())
	}
	// AutoSignon true but the first screen is not a sign-on screen: refused, and not retried later
	r2 := newRig(t, func(o *AppOptions) { o.Sites = siteWithLogin(true) })
	r2.snap = newSnap(24, 80)
	r2.snap.put(0, 0, "Welcome", 0x20)
	r2.snap.Fields = []FieldInfo{{Start: 80 * 3, Length: 10}}
	r2.connect(t, "pub")
	r2.engine().clear()
	r2.a.afterUpdate()
	e := r2.engine()
	e.mu.Lock()
	e.snap = signonSnap()
	e.mu.Unlock()
	r2.a.afterUpdate()
	if len(e.Calls()) != 0 {
		t.Fatalf("first screen decides once: %v", e.Calls())
	}
}

func TestOneOffUserPasswordEnablesAutoSignon(t *testing.T) {
	r := newRig(t, func(o *AppOptions) {
		o.Target = "PUB400.COM:23"
		o.User, o.Password = "ABC", "PW"
	})
	r.a.startup()
	waitFor(t, "connected", r.isConnected)
	e := r.engine()
	e.clear()
	r.a.afterUpdate()
	if !hasCall(e.Calls(), "type:ABC") || !hasCall(e.Calls(), "type:PW") || !hasCall(e.Calls(), "press:enter") {
		t.Fatalf("calls %v", e.Calls())
	}
}

func TestSignonCommand(t *testing.T) {
	r := newRig(t, func(o *AppOptions) { o.Sites = siteWithLogin(false) })
	r.a.RunCommand("signon")
	if r.noticeText() != "not connected" {
		t.Fatalf("notice %q", r.noticeText())
	}
	r.connect(t, "pub")
	r.engine().clear()
	r.a.RunCommand("signon")
	if !hasCall(r.engine().Calls(), "press:enter") {
		t.Fatalf("calls %v", r.engine().Calls())
	}
	e := r.engine()
	e.mu.Lock()
	e.snap.Fields[1].Nondisplay = false
	e.mu.Unlock()
	e.clear()
	r.a.RunCommand("signon")
	if !strings.Contains(r.noticeText(), "sign-on refused") || len(e.Calls()) != 0 {
		t.Fatalf("notice %q calls %v", r.noticeText(), e.Calls())
	}
	// no credentials on an ad-hoc host
	r.a.RunCommand("connect pub400.com")
	waitFor(t, "second engine", func() bool { return len(r.engines) == 2 })
	waitFor(t, "connected", r.isConnected)
	r.a.RunCommand("signon")
	if !strings.Contains(r.noticeText(), "no user and password") {
		t.Fatalf("notice %q", r.noticeText())
	}
}

func TestEngineSendReachesLinkWithTraceCopy(t *testing.T) {
	r := newRig(t, nil)
	r.connect(t, "pub400.com")
	e := r.engine()
	e.onFeed = func(e *fakeEngine, b []byte) { e.send([]byte{0xff, 0xfb, 0x18}, []byte{0xff, 0xfb, 0x00}) }
	o, l := r.d.last()
	o.OnData([]byte{1, 2, 3})
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.writes) != 1 || string(l.writes[0]) != "\xff\xfb\x18" || string(l.traced[0]) != "\xff\xfb\x00" {
		t.Fatalf("writes %x traced %x", l.writes, l.traced)
	}
	if !hasCall(e.Calls(), "feed:3") {
		t.Fatalf("calls %v", e.Calls())
	}
}

func TestDataBeforeDialReturnsIsReplayedInOrder(t *testing.T) {
	r := newRig(t, nil)
	var seen []string
	r.a.opts.EngineFactory = func(size string, cp int, dev string, send func(b, forTrace []byte), update func()) (Engine, error) {
		e := &fakeEngine{snap: signonSnap()}
		e.onFeed = func(_ *fakeEngine, b []byte) { seen = append(seen, string(b)) }
		r.mu.Lock()
		r.engines = append(r.engines, e)
		r.mu.Unlock()
		return e, nil
	}
	r.a.dialer = dialerFunc(func(o clientOptions) (Link, error) {
		o.OnData([]byte("a")) // host speaks before Dial returns
		o.OnData([]byte("b"))
		o.OnClosed(nil)
		return &fakeLink{}, nil
	})
	r.a.RunCommand("connect pub400.com")
	waitFor(t, "replayed", func() bool {
		r.a.mu.Lock()
		defer r.a.mu.Unlock()
		return len(seen) == 2 && !r.a.connecting
	})
	if strings.Join(seen, "") != "ab" {
		t.Fatalf("order %v", seen)
	}
	if r.isConnected() {
		t.Fatal("connection already closed by the host must not show as connected")
	}
}

func TestDropAndReconnectWithEnter(t *testing.T) {
	r := newRig(t, nil)
	r.connect(t, "pub400.com")
	o, _ := r.d.last()
	o.OnClosed(nil)
	if r.isConnected() {
		t.Fatal("still connected after close")
	}
	left, _, alert := r.a.oiaSource()
	if !strings.HasPrefix(left, "Disconnected") || !strings.Contains(left, "Press Enter to reconnect") || !alert {
		t.Fatalf("OIA %q", left)
	}
	e := r.engine()
	e.clear()
	r.a.handleKey(key(tcell.KeyF1, 0, 0)) // dead: not sent
	if len(e.Calls()) != 0 {
		t.Fatalf("key reached a dead connection: %v", e.Calls())
	}
	r.a.handleKey(key(tcell.KeyEnter, 0, 0))
	waitFor(t, "reconnected", func() bool { return r.d.count() == 2 && r.isConnected() })
	// `:connect` does the same
	o, _ = r.d.last()
	o.OnClosed(errors.New("reset by peer"))
	if l, _, _ := r.a.oiaSource(); !strings.Contains(l, "reset by peer") {
		t.Fatalf("OIA %q", l)
	}
	r.a.RunCommand("connect")
	waitFor(t, "third dial", func() bool { return r.d.count() == 3 && r.isConnected() })
}

func TestStaleCloseFromOldConnectionIgnored(t *testing.T) {
	r := newRig(t, nil)
	r.connect(t, "pub400.com")
	o1, l1 := r.d.last()
	r.a.RunCommand("connect pub400.com")
	waitFor(t, "second dial", func() bool { return r.d.count() == 2 && r.isConnected() })
	if !l1.isClosed() {
		t.Fatal("old link not closed")
	}
	o1.OnClosed(nil) // late callback of the first connection
	if !r.isConnected() {
		t.Fatal("stale OnClosed dropped the new connection")
	}
}

func TestDialFailureShowsMessage(t *testing.T) {
	r := newRig(t, nil)
	r.d.err = errors.New("dial tcp: connection refused")
	r.a.RunCommand("connect pub400.com")
	waitFor(t, "failure notice", func() bool { return strings.Contains(r.noticeText(), "Connect failed") })
	if r.isConnected() {
		t.Fatal("connected after a failed dial")
	}
}

func TestDisconnectResetsEngine(t *testing.T) {
	r := newRig(t, nil)
	r.connect(t, "pub400.com")
	_, l := r.d.last()
	e := r.engine()
	r.a.RunCommand("disconnect")
	if !l.isClosed() || !hasCall(e.Calls(), "reset") || r.isConnected() {
		t.Fatalf("closed=%v calls=%v", l.isClosed(), e.Calls())
	}
	if _, ok := r.a.screenSource(); ok {
		t.Error("screen still shows a session after disconnect")
	}
}

func TestStartupChoices(t *testing.T) {
	// no sites, no target: login form
	r := newRig(t, nil)
	r.a.startup()
	if r.a.topOverlay() != "login" {
		t.Fatalf("overlay %q", r.a.topOverlay())
	}
	// sites, no target: site manager
	r2 := newRig(t, func(o *AppOptions) { o.Sites = siteWithLogin(false) })
	r2.a.startup()
	if r2.a.topOverlay() != "sites" {
		t.Fatalf("overlay %q", r2.a.topOverlay())
	}
	// target = site name connects with the site's settings
	r3 := newRig(t, func(o *AppOptions) {
		o.Sites = siteWithLogin(true)
		o.Target = "pub"
	})
	r3.a.startup()
	waitFor(t, "connect", r3.isConnected)
	if o, _ := r3.d.last(); o.Host != "PUB400.COM" {
		t.Fatalf("host %q", o.Host)
	}
	if r3.a.opts.Sites.LastUsed != "pub" {
		t.Errorf("LastUsed %q", r3.a.opts.Sites.LastUsed)
	}
	// bad target shows a message
	r4 := newRig(t, func(o *AppOptions) { o.Target = "host:99999" })
	r4.a.startup()
	if !strings.Contains(r4.noticeText(), "bad port") {
		t.Fatalf("notice %q", r4.noticeText())
	}
}

func TestRedrawCoalesces(t *testing.T) {
	r := newRig(t, nil)
	n := 0
	r.a.post = func(f func()) {
		r.a.mu.Lock()
		n++
		r.a.mu.Unlock()
	}
	for i := 0; i < 50; i++ {
		r.a.requestRedraw()
	}
	time.Sleep(80 * time.Millisecond)
	r.a.mu.Lock()
	got := n
	r.a.mu.Unlock()
	if got != 1 {
		t.Fatalf("50 requests inside one interval gave %d redraws", got)
	}
	r.a.requestRedraw()
	time.Sleep(80 * time.Millisecond)
	r.a.mu.Lock()
	got = n
	r.a.mu.Unlock()
	if got != 2 {
		t.Fatalf("next request gave %d total", got)
	}
}
