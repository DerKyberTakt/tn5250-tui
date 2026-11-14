package tn5250

import (
	"strings"
	"sync"
	"testing"
)

func TestNewValidatesOptions(t *testing.T) {
	if _, err := New(Options{ScreenSize: "80x24"}, Events{}); err == nil {
		t.Error("bad screen size accepted")
	}
	if _, err := New(Options{CodePage: 12345}, Events{}); err == nil {
		t.Error("unsupported code page accepted")
	}
	s, err := New(Options{}, Events{})
	if err != nil {
		t.Fatal(err)
	}
	if s.terminalType != "IBM-3179-2" || s.cp.CCSID != 37 {
		t.Errorf("defaults: %s %d", s.terminalType, s.cp.CCSID)
	}
	w, err := New(Options{ScreenSize: "27x132"}, Events{})
	if err != nil || w.terminalType != "IBM-3477-FC" {
		t.Errorf("27x132: %v %v", err, w)
	}
}

func replayCapture(t *testing.T, h *harness) capture {
	c := loadCapture(t, "pub400-signon-3477fc")
	for _, ev := range c.Events {
		if ev.Dir == "in" {
			h.session.Feed(h2b(t, ev.Hex))
		}
	}
	return c
}

func TestSessionRendersPUB400SignOn(t *testing.T) {
	h := newHarness(t, Options{ScreenSize: "27x132", CodePage: 37})
	c := replayCapture(t, h)
	if got := strings.Join(h.wire, ""); got != c.outboundHex() {
		t.Fatalf("wire bytes differ:\n got %s\nwant %s", got, c.outboundHex())
	}
	// Event by event too: what we sent, in order, is what the capture recorded.
	var want []string
	for _, ev := range c.Events {
		if ev.Dir == "out" {
			want = append(want, ev.Hex)
		}
	}
	if strings.Join(h.wire, "|") != strings.Join(want, "|") {
		t.Errorf("chunking differs:\n got %v\nwant %v", h.wire, want)
	}
	if h.updates != 1 {
		t.Errorf("updates = %d, want 1 (one record)", h.updates)
	}
	snap := h.session.Snapshot()
	if snap.Rows != 24 || snap.Cols != 80 {
		t.Fatalf("size %dx%d", snap.Rows, snap.Cols)
	}
	if !strings.Contains(snap.RowText(0), "Welcome to PUB400.COM") {
		t.Errorf("row 0 = %q", snap.RowText(0))
	}
	if !strings.HasPrefix(snap.RowText(4), "Your user name:") || !strings.HasPrefix(snap.RowText(5), "Password (max. 128):") {
		t.Errorf("rows 4,5 = %q %q", snap.RowText(4), snap.RowText(5))
	}
	if len(snap.Fields) != 2 {
		t.Fatalf("fields = %+v", snap.Fields)
	}
	user, pw := snap.Fields[0], snap.Fields[1]
	if user.Start != 4*80+24 || user.Length != 10 || user.Bypass || user.Nondisplay {
		t.Errorf("user field %+v", user)
	}
	if pw.Start != 5*80+24 || pw.Length != 128 || pw.Bypass || !pw.Nondisplay {
		t.Errorf("password field %+v", pw)
	}
	if snap.Cursor.Row != 4 || snap.Cursor.Col != 24 {
		t.Errorf("cursor %+v", snap.Cursor)
	}
	// Attributes: the user field is green underlined (0x24), the password field non-display.
	if a := DecodeAttr(snap.Cells[user.Start].Attr); !a.Underline || a.Color != "green" {
		t.Errorf("user attr %+v", a)
	}
	if !DecodeAttr(snap.Cells[pw.Start].Attr).Nondisplay {
		t.Error("password attr not non-display")
	}
	colored := 0
	for _, c := range snap.Cells {
		if c.Attr != AttrNormal {
			colored++
		}
	}
	if colored <= 5 {
		t.Errorf("only %d cells carry a non-default attribute", colored)
	}
	oia := h.session.OIA(true)
	if !oia.Connected || oia.InputInhibited != "none" || oia.MessageWaiting || oia.Insert || oia.QueuedKeys != 0 || oia.Message != "" {
		t.Errorf("OIA = %+v", oia)
	}
	if h.session.state.pendingRead != readMDT {
		t.Error("pending read should be MDT")
	}
	if snap.Seq != 1 {
		t.Errorf("Seq = %d", snap.Seq)
	}
	if len(h.sent) != 0 {
		t.Errorf("records sent before any key: %+v", h.sent)
	}
}

func TestSessionSignOnTypeAndEnter(t *testing.T) {
	h := newHarness(t, Options{ScreenSize: "27x132", CodePage: 37})
	replayCapture(t, h)
	h.session.Type("alex")
	h.session.Press(ActionTab)
	h.session.Type("hunter2")
	// The password never shows in the snapshot, but the cursor moved through it.
	snap := h.session.Snapshot()
	if strings.Contains(snap.Text(), "hunter2") {
		t.Fatal("password in snapshot")
	}
	if !strings.Contains(snap.RowText(4), "ALEX") {
		t.Errorf("row 4 = %q", snap.RowText(4))
	}
	if snap.Cursor.Row != 5 || snap.Cursor.Col != 24+7 {
		t.Errorf("cursor %+v", snap.Cursor)
	}
	h.session.Press(ActionEnter)
	if len(h.sent) != 1 {
		t.Fatalf("sent %d", len(h.sent))
	}
	// Row 6 col 32 = 0x20, AID Enter, user then password (trailing blanks stripped).
	if want := "0620" + "f1" + "110519" + e("ALEX") + "110619" + e("hunter2"); h.sent[0].data != want {
		t.Errorf("data %s\nwant %s", h.sent[0].data, want)
	}
	// The wire frame has the password; the trace copy has it blanked, same length.
	wire, trace := h.wire[len(h.wire)-1], h.trace[len(h.trace)-1]
	if !strings.Contains(wire, e("hunter2")) || strings.Contains(trace, e("hunter2")) || len(wire) != len(trace) {
		t.Errorf("wire %s trace %s", wire, trace)
	}
	if h.session.OIA(true).InputInhibited != "system" {
		t.Error("keyboard should be locked")
	}
}

func TestSessionTraceCopyOnlyWhenNeeded(t *testing.T) {
	signon := "04110008" + sba(6, 10) + "1d4000200010" + sba(7, 10) + "1d4000270010" + "0452" + "0000"
	t.Run("read MDT blanks the password but keeps the length", func(t *testing.T) {
		h := newHarness(t, Options{})
		h.host(signon)
		h.session.Type("alex")
		h.session.Press(ActionTab)
		h.session.Type("s3cret")
		h.session.Press(ActionEnter)
		wire, trace := h.wire[len(h.wire)-1], h.trace[len(h.trace)-1]
		if !strings.Contains(wire, e("alex")) || !strings.Contains(wire, e("s3cret")) {
			t.Fatal("wire frame lacks data")
		}
		if strings.Contains(trace, e("s3cret")) || !strings.Contains(trace, e("alex")) || len(trace) != len(wire) {
			t.Errorf("trace %s", trace)
		}
		at := strings.Index(wire, e("s3cret"))
		if trace[at:at+len(e("s3cret"))] != strings.Repeat("40", 6) {
			t.Error("password not blanked in place")
		}
	})
	t.Run("a Read Input Fields (flat) response is blanked too", func(t *testing.T) {
		h := newHarness(t, Options{})
		h.host("04110008" + sba(6, 10) + "1d4000200010" + sba(7, 10) + "1d4000270010" + "0442" + "0000")
		h.session.Type("user")
		h.session.Press(ActionTab)
		h.session.Type("pass")
		h.session.Press(ActionEnter)
		wire, trace := h.wire[len(h.wire)-1], h.trace[len(h.trace)-1]
		if !strings.Contains(wire, e("pass")) || strings.Contains(trace, e("pass")) || !strings.Contains(trace, e("user")) {
			t.Errorf("trace %s", trace)
		}
	})
	t.Run("a frame without non-display data has no separate trace copy", func(t *testing.T) {
		var gotNil bool
		s, err := New(Options{}, Events{Send: func(b, f []byte) { gotNil = f == nil }})
		if err != nil {
			t.Fatal(err)
		}
		s.Feed(frame(t, "04110008"+sba(6, 10)+"1d4000200004"+"0452"+"0000"))
		s.Type("user")
		s.Press(ActionEnter)
		if !gotNil {
			t.Error("forTrace should be nil for a frame without secrets")
		}
	})
	t.Run("the engine keeps the password for the host but not for the snapshot", func(t *testing.T) {
		h := newHarness(t, Options{})
		h.host(signon)
		h.session.Press(ActionTab)
		h.session.Type("s3cret")
		if strings.Contains(h.session.Snapshot().Text(), "s3cret") {
			t.Error("snapshot leaked the password")
		}
		if !strings.Contains(h.rowText(6), "s3cret") {
			t.Error("engine lost the password")
		}
	})
}

func TestSessionTypeAheadQueueAndDrain(t *testing.T) {
	h := newHarness(t, Options{ScreenSize: "27x132", CodePage: 37, TypeAhead: true})
	replayCapture(t, h)
	first := h.screen().fields[0]
	h.screen().cursor = first.start
	h.session.Press(ActionEnter)
	if h.state().inputInhibited != inhibitSystem {
		t.Fatal("should be locked after Enter")
	}
	afterEnter := len(h.sent)
	h.session.Press(ActionHome)
	h.session.Type("AB")
	h.session.Press(ActionEnter)
	h.session.Type("C")
	if h.session.OIA(true).QueuedKeys != 4 {
		t.Fatalf("queued = %d", h.session.OIA(true).QueuedKeys)
	}
	if len(h.sent) != afterEnter {
		t.Fatal("queued keys went out")
	}
	// Write to Display, CC2 0x08 = unlock keyboard: replays up to the next Enter.
	h.session.Feed(h2b(t, "000e12a0"+"0000040000"+"03"+"0411"+"0008"+"ffef"))
	if len(h.sent) != afterEnter+1 {
		t.Fatalf("sent %d, want %d", len(h.sent), afterEnter+1)
	}
	if h.state().inputInhibited != inhibitSystem || h.session.OIA(true).QueuedKeys != 1 {
		t.Errorf("after replay: %s queued %d", h.state().inputInhibited, h.session.OIA(true).QueuedKeys)
	}
	if got := h.session.Snapshot().RowText(4)[24:26]; got != "AB" {
		t.Errorf("typed text %q", got)
	}
	// Reset throws the rest away.
	h.session.Press(ActionReset)
	if h.session.OIA(true).QueuedKeys != 0 {
		t.Error("Reset did not clear the queue")
	}
}

func TestSessionTypeAheadLimitAndImmediateKeys(t *testing.T) {
	h := newHarness(t, Options{TypeAhead: true})
	for i := 0; i < 300; i++ {
		h.session.Type("x")
	}
	if got := h.session.OIA(true).QueuedKeys; got != typeAheadLimit {
		t.Errorf("queued %d, want the limit %d", got, typeAheadLimit)
	}
	// Attn, SysReq and TestRequest bypass the queue even while X SYSTEM.
	h.session.Press(ActionAttn)
	h.session.Press(ActionSysReq)
	h.session.Press(ActionTestRequest)
	if len(h.sent) != 3 || h.sent[0].flags1 != FlagATN || h.sent[1].flags1 != FlagSRQ || h.sent[2].flags1 != FlagTRQ {
		t.Errorf("sent %+v", h.sent)
	}
	if got := h.session.OIA(true).QueuedKeys; got != typeAheadLimit {
		t.Errorf("immediate keys were queued: %d", got)
	}
}

func TestSessionTypeAheadDroppedOnError(t *testing.T) {
	h := newHarness(t, Options{TypeAhead: true})
	// A queue that replays cleanly is drained.
	h.session.Type("ab")
	h.session.Press(ActionTab)
	h.host("04110008" + sba(5, 5) + e("label") + "1d4000200003" + "0452" + "0000")
	if h.session.OIA(true).QueuedKeys != 0 || !strings.Contains(h.rowText(4), "ab") {
		t.Errorf("queue not drained: %d row %q", h.session.OIA(true).QueuedKeys, h.rowText(4))
	}
	// A queue that hits an operator error is dropped.
	h2 := newHarness(t, Options{TypeAhead: true})
	h2.session.Type("a")
	h2.session.Type("b")
	h2.host("04110008" + sba(5, 5) + e("label") + "130101" + "04520000") // no fields: cursor on a protected cell
	if h2.state().inputInhibited != inhibitError || h2.session.OIA(true).QueuedKeys != 0 {
		t.Errorf("state %s queued %d", h2.state().inputInhibited, h2.session.OIA(true).QueuedKeys)
	}
}

func TestSessionDropsKeysWhileLockedWithoutTypeAhead(t *testing.T) {
	h := newHarness(t, Options{})
	h.session.Type("X")
	if h.session.OIA(true).QueuedKeys != 0 {
		t.Error("queued without type-ahead")
	}
}

func TestSessionCancelInviteAndMessageLight(t *testing.T) {
	h := newHarness(t, Options{})
	rec := func(op byte) []byte {
		return []byte{0x00, 0x0a, 0x12, 0xa0, 0, 0, 4, 0, 0, op, 0xff, 0xef}
	}
	h.session.Feed(rec(OpMessageLightOn))
	if !h.state().messageWaiting || !h.session.OIA(true).MessageWaiting {
		t.Fatal("message light should be on")
	}
	h.session.Feed(rec(OpMessageLightOff))
	if h.state().messageWaiting {
		t.Fatal("message light should be off")
	}
	h.session.Feed(rec(OpCancelInvite))
	if len(h.wire) != 1 || h.wire[0] != "000a12a000000400000affef" {
		t.Errorf("wire %v", h.wire)
	}
}

func TestSessionReportsBadRecords(t *testing.T) {
	h := newHarness(t, Options{})
	h.session.Feed(h2b(t, "0102ffef")) // too short for a GDS header
	if len(h.errs) != 1 || !strings.Contains(h.errs[0].Error(), "too short") {
		t.Errorf("errs = %v", h.errs)
	}
	h.session.Feed(h2b(t, "000a12a1000004000003ffef"))
	if len(h.errs) != 2 {
		t.Errorf("errs = %v", h.errs)
	}
}

func TestSessionAlarmAndOIAMessage(t *testing.T) {
	h := newHarness(t, Options{})
	h.host("0411" + "00" + "0c") // CC2: unlock + alarm
	if h.alarms != 1 || h.state().inputInhibited != inhibitNone {
		t.Errorf("alarms %d inhibited %s", h.alarms, h.state().inputInhibited)
	}
	h.host("04" + "21" + e("Not allowed"))
	oia := h.session.OIA(true)
	if oia.InputInhibited != "error" || oia.Message != "Not allowed" {
		t.Errorf("OIA = %+v", oia)
	}
	h.session.Press(ActionReset)
	if oia := h.session.OIA(true); oia.InputInhibited != "none" || oia.Message != "" {
		t.Errorf("after reset OIA = %+v", oia)
	}
}

func TestSessionDeviceNameFromHost(t *testing.T) {
	h := newHarness(t, Options{DeviceName: "mydev"})
	if got := h.session.OIA(true).DeviceName; got != "mydev" {
		t.Errorf("requested name %q", got)
	}
	// Host answers NEW-ENVIRON IS with the name it assigned.
	h.session.Feed(append([]byte{0xff, 0xfa, 0x27, 0x00, 0x03}, append([]byte("DEVNAME\x01QPADEV0007"), 0xff, 0xf0)...))
	if got := h.session.OIA(true).DeviceName; got != "QPADEV0007" {
		t.Errorf("assigned name %q", got)
	}
	// And the request carries the device name.
	h.session.Feed(h2b(t, "fffa2701fff0"))
	if len(h.wire) != 1 || !strings.Contains(string(h2b(t, h.wire[0])), "DEVNAME\x01MYDEV") {
		t.Errorf("wire %v", h.wire)
	}
}

func TestSessionSeqAndUpdateEvents(t *testing.T) {
	h := newHarness(t, Options{})
	if h.session.Snapshot().Seq != 0 {
		t.Fatal("fresh session should start at 0")
	}
	h.host(twoFields)
	s1 := h.session.Snapshot().Seq
	if s1 == 0 || h.updates != 1 {
		t.Fatalf("seq %d updates %d", s1, h.updates)
	}
	h.session.Type("a")
	s2 := h.session.Snapshot().Seq
	h.session.Press(ActionTab)
	s3 := h.session.Snapshot().Seq
	h.session.SetCursor(0, 0)
	s4 := h.session.Snapshot().Seq
	if !(s1 < s2 && s2 < s3 && s3 < s4) {
		t.Errorf("seq not increasing: %d %d %d %d", s1, s2, s3, s4)
	}
	if h.updates != 4 {
		t.Errorf("updates = %d, want one per change (4)", h.updates)
	}
	// Reading does not change it.
	if h.session.Snapshot().Seq != s4 {
		t.Error("Snapshot changed Seq")
	}
	// Telnet negotiation alone is not a screen change.
	h.session.Feed(h2b(t, "fffd00"))
	if h.session.Snapshot().Seq != s4 {
		t.Error("negotiation bumped Seq")
	}
	// Out-of-range cursor positions are ignored.
	h.session.SetCursor(99, 0)
	h.session.SetCursor(0, -1)
	if h.session.Snapshot().Seq != s4 {
		t.Error("ignored SetCursor bumped Seq")
	}
}

func TestSessionSnapshotIsACopy(t *testing.T) {
	h := newHarness(t, Options{})
	h.host("04110008" + sba(1, 1) + e("Hello"))
	snap := h.session.Snapshot()
	h.host("04110008" + sba(1, 1) + e("Jello"))
	if !strings.HasPrefix(snap.RowText(0), "Hello") {
		t.Errorf("snapshot changed under us: %q", snap.RowText(0))
	}
	if !strings.HasPrefix(h.session.Snapshot().RowText(0), "Jello") {
		t.Error("new snapshot stale")
	}
}

func TestSessionReset(t *testing.T) {
	h := newHarness(t, Options{TypeAhead: true, DeviceName: "mydev"})
	h.session.Feed(h2b(t, "fffd19"))
	h.host("042000" + "04110008" + sba(1, 1) + e("wide screen") + "1d4000200005" + "0452" + "0000")
	h.session.Press(ActionEnter)
	h.session.Type("q")
	h.session.Feed(append([]byte{0xff, 0xfa, 0x27, 0x00, 0x03}, append([]byte("DEVNAME\x01QPADEV0007"), 0xff, 0xf0)...))
	before := h.session.Snapshot().Seq
	h.session.Reset()
	snap := h.session.Snapshot()
	if snap.Rows != 24 || snap.Cols != 80 || len(snap.Fields) != 0 || strings.TrimSpace(snap.Text()) != "" {
		t.Errorf("screen not cleared: %dx%d fields %d", snap.Rows, snap.Cols, len(snap.Fields))
	}
	if snap.Seq <= before {
		t.Error("Reset must bump Seq")
	}
	oia := h.session.OIA(false)
	if oia.Connected || oia.InputInhibited != "system" || oia.QueuedKeys != 0 || oia.DeviceName != "mydev" || oia.Message != "" {
		t.Errorf("OIA = %+v", oia)
	}
	if len(h.session.telnet.agreed) != 0 {
		t.Error("telnet options kept")
	}
	// The session works again for a new connection.
	h.host(twoFields)
	h.session.Type("ok")
	if !strings.Contains(h.rowText(1), "ok") {
		t.Errorf("row 1 = %q", h.rowText(1))
	}
}

func TestSessionEventsMayCallBackIntoTheSession(t *testing.T) {
	var s *Session
	var seen []string
	s, err := New(Options{}, Events{
		Update: func() { seen = append(seen, s.Snapshot().RowText(0)[:5]) },
		Send:   func(b, _ []byte) { _ = s.OIA(true) },
	})
	if err != nil {
		t.Fatal(err)
	}
	s.Feed(frame(t, "04110008"+sba(1, 1)+e("Hello")))
	s.Feed(h2b(t, "fffd00"))
	if len(seen) != 1 || seen[0] != "Hello" {
		t.Errorf("seen %v", seen)
	}
}

func TestSessionConcurrentUse(t *testing.T) {
	h := newHarness(t, Options{TypeAhead: true})
	h.host(twoFields)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_ = h.session.Snapshot()
				_ = h.session.OIA(true)
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for j := 0; j < 50; j++ {
			h.session.Type("a")
			h.session.Press(ActionTab)
			h.session.Press(ActionReset)
		}
	}()
	wg.Wait()
}

func TestSessionTraceEvents(t *testing.T) {
	var lines []string
	s, err := New(Options{}, Events{Trace: func(l string) { lines = append(lines, l) }})
	if err != nil {
		t.Fatal(err)
	}
	s.Feed(frame(t, "04110008"+sba(1, 1)+e("x")))
	if len(lines) == 0 || !strings.HasPrefix(lines[0], "cmd 11") {
		t.Errorf("trace = %q", lines)
	}
}

func TestSessionCodePages(t *testing.T) {
	// German code page 273: the host writes 0x4A as the umlaut A; typing that character
	// sends the same byte back.
	h := newHarness(t, Options{CodePage: 273})
	h.host("04110008" + sba(1, 1) + "4a" + "1d4000200003" + "0452" + "0000")
	if !strings.HasPrefix(h.session.Snapshot().RowText(0), "Ä") {
		t.Errorf("row 0 = %q", h.session.Snapshot().RowText(0))
	}
	h.session.Type("Ä")
	h.session.Press(ActionEnter)
	if len(h.sent) != 1 || !strings.Contains(h.sent[0].data, "4a") {
		t.Errorf("sent %+v", h.sent)
	}
	// A character outside the page goes out as the substitute.
	h2 := newHarness(t, Options{})
	h2.host("04110008" + sba(1, 1) + "1d4000200003" + "0452" + "0000")
	h2.session.Type("中")
	h2.session.Press(ActionEnter)
	if !strings.Contains(h2.sent[0].data, "6f") {
		t.Errorf("sent %+v", h2.sent)
	}
	// Control bytes written as data show as the box.
	h3 := newHarness(t, Options{})
	h3.host("04110008" + sba(1, 1) + "1f" + e("A"))
	if got := []rune(h3.session.Snapshot().RowText(0)); got[0] != '□' || got[1] != 'A' {
		t.Errorf("row 0 = %q", string(got[:3]))
	}
}
