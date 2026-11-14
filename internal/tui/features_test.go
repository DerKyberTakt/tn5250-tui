package tui

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// probeEngine is a fakeEngine that also answers the optional engine interfaces.
type probeEngine struct {
	*fakeEngine
	probe SignonProbe
	pc    PcCommand
	pcOK  bool
}

func (p *probeEngine) SignonProbe() SignonProbe     { return p.probe }
func (p *probeEngine) PcCommand() (PcCommand, bool) { return p.pc, p.pcOK }

// probeRig is a rig whose engines are probeEngines; eng returns the latest one.
func probeRig(t *testing.T, mod func(*AppOptions)) (*rig, func() *probeEngine) {
	t.Helper()
	r := newRig(t, mod)
	var last *probeEngine
	r.a.opts.EngineFactory = func(size string, cp int, dev string, send func(b, forTrace []byte), update func()) (Engine, error) {
		fe := &fakeEngine{snap: r.snap, send: send, upd: update}
		fe.oia.InputInhibited = "none"
		p := &probeEngine{fakeEngine: fe}
		r.mu.Lock()
		last = p
		r.engines = append(r.engines, fe)
		r.args = append(r.args, fmt.Sprintf("%s/%d/%s", size, cp, dev))
		r.mu.Unlock()
		return p, nil
	}
	return r, func() *probeEngine {
		r.mu.Lock()
		defer r.mu.Unlock()
		return last
	}
}

// devices lists the device name each engine was created with.
func (r *rig) devices() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, a := range r.args {
		out = append(out, a[strings.LastIndex(a, "/")+1:])
	}
	return out
}

// closeByHost simulates the host closing the latest connection.
func (r *rig) closeByHost() {
	o, _ := r.d.last()
	o.OnClosed(nil)
}

// retries closes the connection n times in a row, waiting for each reconnect.
func (r *rig) retries(t *testing.T, n int) {
	t.Helper()
	for i := 1; i <= n; i++ {
		want := i + 1
		r.closeByHost()
		waitFor(t, "next dial", func() bool { return r.d.count() == want })
		waitFor(t, "connected", r.isConnected)
	}
}

// ---- device name in use ----------------------------------------------------

func TestDeviceNameInUseTriesTheNextLetter(t *testing.T) {
	r := newRig(t, func(o *AppOptions) { o.DeviceName = "TESTPC*" })
	r.connect(t, "pub400.com")
	if got := r.devices(); len(got) != 1 || got[0] != "TESTPCA" {
		t.Fatalf("first name %v", got)
	}
	// The host closes at once, with no screen: the name is in use.
	r.retries(t, 2)
	if got := strings.Join(r.devices(), ","); got != "TESTPCA,TESTPCB,TESTPCC" {
		t.Fatalf("names %s", got)
	}
}

func TestDeviceNameRetryNoticeNamesBothNames(t *testing.T) {
	r := newRig(t, func(o *AppOptions) { o.DeviceName = "TESTPC*" })
	r.connect(t, "pub400.com")
	block := make(chan struct{})
	t.Cleanup(func() { close(block) })
	r.a.dialer = dialerFunc(func(o clientOptions) (Link, error) {
		<-block // the second connect never completes: the notice stays "trying"
		return nil, errors.New("test over")
	})
	r.closeByHost()
	waitFor(t, "second engine", func() bool { return len(r.devices()) == 2 })
	if n := r.noticeText(); !strings.Contains(n, "TESTPCA refused by the host, trying TESTPCB") {
		t.Fatalf("notice %q", n)
	}
}

func TestDeviceNameGivesUpAfterTenRetries(t *testing.T) {
	r := newRig(t, func(o *AppOptions) { o.DeviceName = "PC=" })
	r.connect(t, "pub400.com")
	r.retries(t, maxDeviceRetries-1) // PC1 .. PC9
	// "=" only has the digits 0..9: PC0 .. PC9 are used up, so the next refusal is final.
	r.closeByHost()
	time.Sleep(50 * time.Millisecond)
	if r.d.count() != 10 {
		t.Fatalf("dials = %d, want 10", r.d.count())
	}
	if n := r.noticeText(); !strings.Contains(n, "workstation PC9 in use?") {
		t.Fatalf("notice %q", n)
	}
	left, _, alert := r.a.oiaSource()
	if !alert || !strings.Contains(left, "workstation PC9 in use?") {
		t.Fatalf("OIA %q (alert %v)", left, alert)
	}
}

func TestDeviceNameTenRetriesAtMost(t *testing.T) {
	r := newRig(t, func(o *AppOptions) { o.DeviceName = "TESTPC*" })
	r.connect(t, "pub400.com")
	r.retries(t, maxDeviceRetries)
	r.closeByHost() // the eleventh refusal is final even though '*' has letters left
	time.Sleep(50 * time.Millisecond)
	if r.d.count() != maxDeviceRetries+1 {
		t.Fatalf("dials = %d, want %d", r.d.count(), maxDeviceRetries+1)
	}
	if n := r.noticeText(); !strings.Contains(n, "workstation TESTPCK in use?") {
		t.Fatalf("notice %q", n)
	}
}

func TestDeviceNamePlainNameGetsNumericSuffixes(t *testing.T) {
	r := newRig(t, func(o *AppOptions) { o.DeviceName = "testpc" })
	r.connect(t, "pub400.com")
	r.retries(t, 9)
	want := "TESTPC,TESTPC1,TESTPC2,TESTPC3,TESTPC4,TESTPC5,TESTPC6,TESTPC7,TESTPC8,TESTPC9"
	if got := strings.Join(r.devices(), ","); got != want {
		t.Fatalf("names %s", got)
	}
	r.closeByHost() // no TESTPC10: the user is told instead
	time.Sleep(50 * time.Millisecond)
	if r.d.count() != 10 || !strings.Contains(r.noticeText(), "workstation TESTPC9 in use?") {
		t.Fatalf("dials %d notice %q", r.d.count(), r.noticeText())
	}
}

func TestDeviceNameNoRetryAfterAScreenOrLate(t *testing.T) {
	// A screen arrived: a normal close, no retry.
	r := newRig(t, func(o *AppOptions) { o.DeviceName = "TESTPC*" })
	r.connect(t, "pub400.com")
	r.engine().upd() // the host drew a screen
	r.closeByHost()
	time.Sleep(50 * time.Millisecond)
	if r.d.count() != 1 || strings.Contains(r.noticeText(), "in use") {
		t.Fatalf("dials %d notice %q", r.d.count(), r.noticeText())
	}
	// No screen, but the close came after the 8 s window.
	r2 := newRig(t, func(o *AppOptions) { o.DeviceName = "TESTPC*" })
	r2.connect(t, "pub400.com")
	waitFor(t, "connect time", func() bool {
		r2.a.mu.Lock()
		defer r2.a.mu.Unlock()
		return !r2.a.sess.connectedAt.IsZero()
	})
	r2.a.mu.Lock()
	at := r2.a.sess.connectedAt
	r2.a.now = func() time.Time { return at.Add(earlyDropWindow + time.Second) }
	r2.a.mu.Unlock()
	r2.closeByHost()
	time.Sleep(50 * time.Millisecond)
	if r2.d.count() != 1 || strings.Contains(r2.noticeText(), "in use") {
		t.Fatalf("late close: dials %d notice %q", r2.d.count(), r2.noticeText())
	}
	// No device name requested: nothing to retry.
	r3 := newRig(t, nil)
	r3.connect(t, "pub400.com")
	r3.closeByHost()
	time.Sleep(50 * time.Millisecond)
	if r3.d.count() != 1 || strings.Contains(r3.noticeText(), "in use") {
		t.Fatalf("no device name: dials %d notice %q", r3.d.count(), r3.noticeText())
	}
}

func TestDeviceNameUserReconnectStartsOverAtTheFirstName(t *testing.T) {
	r := newRig(t, func(o *AppOptions) { o.DeviceName = "TESTPC*" })
	r.connect(t, "pub400.com")
	r.retries(t, 1)
	r.a.reconnect() // the user asked: the template starts again
	waitFor(t, "third dial", func() bool { return r.d.count() == 3 })
	if got := r.devices(); got[2] != "TESTPCA" {
		t.Fatalf("names %v", got)
	}
}

// ---- code page hint --------------------------------------------------------

func TestSignonHintNamesVariantCharsAndCodePageNotThePassword(t *testing.T) {
	r, eng := probeRig(t, nil)
	r.connect(t, "pub400.com")
	e := eng()
	e.probe = SignonProbe{Screen: true, VariantChars: []string{"!", "@"}, CodePage: 37}
	typeRunes(r.a, "pa!s@w!rd")
	r.a.pressKey("enter")
	r.a.afterUpdate() // the host unlocked the keyboard and shows the sign-on screen again
	n := r.noticeText()
	for _, want := range []string{"Sign-on failed", "(! @)", "37", ":cp"} {
		if !strings.Contains(n, want) {
			t.Errorf("notice %q lacks %q", n, want)
		}
	}
	for _, leak := range []string{"pa!s", "s@w", "w!rd", "pa!s@w!rd"} {
		if strings.Contains(n, leak) {
			t.Errorf("notice shows part of the password (%q): %q", leak, n)
		}
	}
	// A key press clears the one-off notice; a second refusal says nothing new.
	r.a.pressKey("pf4")
	if r.noticeText() != "" {
		t.Fatalf("notice survived a key press: %q", r.noticeText())
	}
	r.a.pressKey("enter")
	r.a.afterUpdate()
	if r.noticeText() != "" {
		t.Errorf("hint shown twice: %q", r.noticeText())
	}
}

func TestSignonHintStaysQuietWhenItDoesNotApply(t *testing.T) {
	// The password has no variant characters.
	r, eng := probeRig(t, nil)
	r.connect(t, "pub400.com")
	e := eng()
	e.probe = SignonProbe{Screen: true, CodePage: 37}
	r.a.pressKey("enter")
	r.a.afterUpdate()
	if r.noticeText() != "" {
		t.Errorf("no variant characters: notice %q", r.noticeText())
	}
	// Variant characters, but the sign-on worked (the host moved on to another screen).
	e.probe = SignonProbe{Screen: true, VariantChars: []string{"!"}, CodePage: 37}
	r.a.pressKey("enter")
	e.probe = SignonProbe{} // a menu now
	r.a.afterUpdate()
	if r.noticeText() != "" {
		t.Errorf("sign-on succeeded: notice %q", r.noticeText())
	}
	// Not a sign-on screen when Enter was pressed.
	e.probe = SignonProbe{VariantChars: []string{"!"}}
	r.a.pressKey("enter")
	e.probe = SignonProbe{Screen: true, VariantChars: []string{"!"}, CodePage: 37}
	r.a.afterUpdate()
	if r.noticeText() != "" {
		t.Errorf("Enter outside a sign-on screen: notice %q", r.noticeText())
	}
	// A key other than Enter does not arm it.
	r.a.pressKey("pf3")
	r.a.afterUpdate()
	if r.noticeText() != "" {
		t.Errorf("pf3: notice %q", r.noticeText())
	}
}

func TestSignonHintWaitsForTheHostToUnlockAndGivesUp(t *testing.T) {
	r, eng := probeRig(t, nil)
	r.connect(t, "pub400.com")
	e := eng()
	setInhibit := func(e *probeEngine, v string) {
		e.mu.Lock()
		e.oia.InputInhibited = v
		e.mu.Unlock()
	}
	e.probe = SignonProbe{Screen: true, VariantChars: []string{"!"}, CodePage: 273}
	r.a.pressKey("enter")
	setInhibit(e, "system")
	r.a.afterUpdate()
	r.a.afterUpdate()
	if r.noticeText() != "" {
		t.Fatalf("judged while the keyboard was locked: %q", r.noticeText())
	}
	setInhibit(e, "none")
	r.a.afterUpdate()
	if n := r.noticeText(); !strings.Contains(n, "(!)") || !strings.Contains(n, "273") {
		t.Fatalf("notice %q", n)
	}
	// A host that never answers: after a few locked updates the attempt is forgotten.
	r2, eng2 := probeRig(t, nil)
	r2.connect(t, "pub400.com")
	e2 := eng2()
	e2.probe = SignonProbe{Screen: true, VariantChars: []string{"!"}, CodePage: 37}
	r2.a.pressKey("enter")
	setInhibit(e2, "system")
	for i := 0; i < signonAnswerScreens+2; i++ {
		r2.a.afterUpdate()
	}
	setInhibit(e2, "none")
	r2.a.afterUpdate()
	if r2.noticeText() != "" {
		t.Errorf("a stale attempt produced a hint: %q", r2.noticeText())
	}
}

func TestSignonHintAlsoWatchesTheAutoSignon(t *testing.T) {
	r, eng := probeRig(t, func(o *AppOptions) { o.Sites = siteWithLogin(true) })
	r.connect(t, "pub")
	e := eng()
	e.probe = SignonProbe{Screen: true, VariantChars: []string{"$"}, CodePage: 37}
	r.a.afterUpdate() // types the sign-on and presses Enter
	if !hasCall(e.Calls(), "press:enter") {
		t.Fatalf("calls %v", e.Calls())
	}
	r.a.afterUpdate() // the host shows the sign-on screen again
	n := r.noticeText()
	if !strings.Contains(n, "($)") {
		t.Fatalf("notice %q", n)
	}
	if strings.Contains(n, "SECRET") {
		t.Error("notice shows the password")
	}
}

// ---- STRPCCMD --------------------------------------------------------------

func TestPcCommandIsAnsweredWithEnterAndReported(t *testing.T) {
	r, eng := probeRig(t, nil)
	r.connect(t, "pub400.com")
	e := eng()
	e.clear()
	r.a.afterUpdate()
	if hasCall(e.Calls(), "press:enter") {
		t.Fatal("pressed Enter with no PC command on the screen")
	}
	e.pc, e.pcOK = PcCommand{Command: "START https://example.invalid/", Wait: true}, true
	r.a.afterUpdate()
	if got := joinCalls(e.Calls()); got != "press:enter" {
		t.Fatalf("calls %q", got)
	}
	if n := r.noticeText(); n != "PC command not run here: START https://example.invalid/" {
		t.Fatalf("notice %q", n)
	}
	// The same screen on the next update is not answered again.
	r.a.afterUpdate()
	if got := joinCalls(e.Calls()); got != "press:enter" {
		t.Fatalf("answered twice: %q", got)
	}
	// A key press clears the notice; a later request is answered again.
	r.a.pressKey("pf3")
	if r.noticeText() != "" {
		t.Errorf("notice survived the key: %q", r.noticeText())
	}
	e.pcOK = false
	r.a.afterUpdate()
	e.clear()
	e.pc, e.pcOK = PcCommand{Command: "NOTEPAD"}, true
	r.a.afterUpdate()
	if got := joinCalls(e.Calls()); got != "press:enter" {
		t.Fatalf("second request: %q", got)
	}
	if !strings.Contains(r.noticeText(), "NOTEPAD") {
		t.Errorf("notice %q", r.noticeText())
	}
}

func TestPcCommandNotAnsweredWhenDisconnectedOrUnsupported(t *testing.T) {
	// An engine without PcCommandSource (the plain fakeEngine) never gets an Enter.
	r := newRig(t, nil)
	r.connect(t, "pub400.com")
	e := r.engine()
	e.clear()
	r.a.afterUpdate()
	if len(e.Calls()) != 0 {
		t.Errorf("calls %v", e.Calls())
	}
	// A dropped connection is left alone.
	r2, eng := probeRig(t, nil)
	r2.connect(t, "pub400.com")
	pe := eng()
	pe.pc, pe.pcOK = PcCommand{Command: "DIR"}, true
	r2.closeByHost()
	pe.clear()
	r2.a.afterUpdate()
	if hasCall(pe.Calls(), "press:enter") {
		t.Error("pressed Enter on a dead connection")
	}
}
