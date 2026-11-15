package tui

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
)

// twoTabs connects a first and a second session (two tabs) and returns their engines.
func twoTabs(t *testing.T) (*rig, *fakeEngine, *fakeEngine) {
	t.Helper()
	r := newRig(t, nil)
	r.connect(t, "pub400.com")
	e1 := r.engine()
	r.a.RunCommand("connect pub400.com:2323")
	waitFor(t, "second tab connected", func() bool { return r.d.count() == 2 && r.isConnected() && r.a.TabCount() == 2 })
	e2 := r.engine()
	if e1 == e2 {
		t.Fatal("the second connect reused the first engine")
	}
	return r, e1, e2
}

func TestConnectWhileConnectedOpensATab(t *testing.T) {
	r, e1, e2 := twoTabs(t)
	if r.a.ActiveTab() != 1 {
		t.Fatalf("active tab %d, want the new one", r.a.ActiveTab())
	}
	// Both links stay open; data for the first tab reaches its engine in the background.
	if r.d.links[0].isClosed() {
		t.Fatal("the first session was closed by the second connect")
	}
	r.d.dials[0].OnData([]byte("abc"))
	if !hasCall(e1.Calls(), "feed:3") {
		t.Fatalf("background tab not fed: %v", e1.Calls())
	}
	if hasCall(e2.Calls(), "feed:3") {
		t.Fatal("data of tab 1 reached tab 2")
	}
	// Keys go to the active tab only.
	e1.clear()
	e2.clear()
	r.a.handleKey(key(tcell.KeyF3, 0, 0))
	if !hasCall(e2.Calls(), "press:pf3") || len(e1.Calls()) != 0 {
		t.Fatalf("tab2 %v tab1 %v", e2.Calls(), e1.Calls())
	}
	if got := r.a.siteOf(0).Port; got != 23 {
		t.Fatalf("tab 1 port %d", got)
	}
	if got := r.a.siteOf(1).Port; got != 2323 {
		t.Fatalf("tab 2 port %d", got)
	}
}

func TestSwitchTabsByCommandAndKeys(t *testing.T) {
	r, e1, e2 := twoTabs(t)
	// The screen shows the active tab's engine.
	snap1 := newSnap(24, 80)
	snap1.put(0, 0, "FIRST", 0x20)
	e1.mu.Lock()
	e1.snap = snap1
	e1.mu.Unlock()
	snap2 := newSnap(24, 80)
	snap2.put(0, 0, "SECOND", 0x20)
	e2.mu.Lock()
	e2.snap = snap2
	e2.mu.Unlock()
	shown := func() string {
		s, _ := r.a.screenSource()
		return strings.TrimSpace(strings.SplitN(s.Text(), "\n", 2)[0])
	}
	if shown() != "SECOND" {
		t.Fatalf("shows %q", shown())
	}
	r.a.RunCommand("tab 1")
	if r.a.ActiveTab() != 0 || shown() != "FIRST" {
		t.Fatalf("tab 1: active %d shows %q", r.a.ActiveTab(), shown())
	}
	r.a.RunCommand("tab next")
	if r.a.ActiveTab() != 1 {
		t.Fatalf("next: %d", r.a.ActiveTab())
	}
	r.a.RunCommand("tab next") // wraps
	if r.a.ActiveTab() != 0 {
		t.Fatalf("wrap: %d", r.a.ActiveTab())
	}
	r.a.RunCommand("tab prev")
	if r.a.ActiveTab() != 1 {
		t.Fatalf("prev: %d", r.a.ActiveTab())
	}
	r.a.handleKey(key(tcell.KeyPgUp, 0, tcell.ModCtrl))
	if r.a.ActiveTab() != 0 {
		t.Fatalf("Ctrl+PgUp: %d", r.a.ActiveTab())
	}
	r.a.handleKey(key(tcell.KeyPgDn, 0, tcell.ModCtrl))
	if r.a.ActiveTab() != 1 {
		t.Fatalf("Ctrl+PgDn: %d", r.a.ActiveTab())
	}
	r.a.handleKey(key(tcell.KeyRune, '1', tcell.ModAlt))
	if r.a.ActiveTab() != 0 {
		t.Fatalf("Alt+1: %d", r.a.ActiveTab())
	}
	r.a.handleKey(key(tcell.KeyRune, '2', tcell.ModAlt))
	if r.a.ActiveTab() != 1 {
		t.Fatalf("Alt+2: %d", r.a.ActiveTab())
	}
	r.a.RunCommand("tab 7")
	if !strings.Contains(r.noticeText(), "no tab 7") {
		t.Fatalf("notice %q", r.noticeText())
	}
	r.a.RunCommand("tab")
	if !strings.Contains(r.noticeText(), "usage") {
		t.Fatalf("notice %q", r.noticeText())
	}
	// A plain PgUp still rolls (the chord is Ctrl+PgUp).
	e2.clear()
	r.a.handleKey(key(tcell.KeyPgUp, 0, 0))
	if !hasCall(e2.Calls(), "press:pageUp") {
		t.Fatalf("PgUp: %v", e2.Calls())
	}
}

func TestCloseTab(t *testing.T) {
	r, _, _ := twoTabs(t)
	r.a.RunCommand("close") // the active (second) tab
	if r.a.TabCount() != 1 || r.a.ActiveTab() != 0 {
		t.Fatalf("tabs %d active %d", r.a.TabCount(), r.a.ActiveTab())
	}
	if !r.d.links[1].isClosed() || r.d.links[0].isClosed() {
		t.Fatal("close must drop the closed tab's link and keep the other")
	}
	if !r.isConnected() {
		t.Fatal("the remaining tab is still connected")
	}
	// The last tab is emptied, not removed.
	r.a.RunCommand("close")
	if r.a.TabCount() != 1 || r.isConnected() || !r.d.links[0].isClosed() {
		t.Fatalf("last tab: tabs %d connected %v", r.a.TabCount(), r.isConnected())
	}
	if r.a.siteOf(0).Host != "" {
		t.Fatal("the emptied tab still names a host")
	}
}

func TestCtrlWAsksWhenConnected(t *testing.T) {
	r, _, _ := twoTabs(t)
	r.a.handleKey(key(tcell.KeyCtrlW, 0, tcell.ModCtrl))
	if r.a.topOverlay() != "closetab" {
		t.Fatalf("overlay %q", r.a.topOverlay())
	}
	r.a.closeNamed("closetab")
	if r.a.TabCount() != 2 {
		t.Fatal("cancel must keep the tab")
	}
	// An idle tab closes without asking.
	r.a.RunCommand("disconnect")
	r.a.handleKey(key(tcell.KeyCtrlW, 0, tcell.ModCtrl))
	if r.a.TabCount() != 1 || r.a.topOverlay() != "" {
		t.Fatalf("tabs %d overlay %q", r.a.TabCount(), r.a.topOverlay())
	}
}

func TestNewTabAndDroppedTabReconnectsInPlace(t *testing.T) {
	r := newRig(t, nil)
	r.connect(t, "pub400.com")
	r.a.handleKey(key(tcell.KeyCtrlT, 0, tcell.ModCtrl))
	if r.a.TabCount() != 2 || r.a.ActiveTab() != 1 || r.a.topOverlay() != "login" {
		t.Fatalf("Ctrl+T: tabs %d active %d overlay %q", r.a.TabCount(), r.a.ActiveTab(), r.a.topOverlay())
	}
	r.a.closeNamed("login")
	// The new, idle tab takes the next connect; no third tab.
	r.a.RunCommand("connect pub400.com:2323")
	waitFor(t, "connected in the new tab", func() bool { return r.d.count() == 2 && r.isConnected() })
	if r.a.TabCount() != 2 {
		t.Fatalf("tabs %d", r.a.TabCount())
	}
	// Dropped by the host: Enter reconnects in the same tab.
	o, _ := r.d.last()
	o.OnClosed(nil)
	r.a.handleKey(key(tcell.KeyEnter, 0, 0))
	waitFor(t, "reconnected", func() bool { return r.d.count() == 3 && r.isConnected() })
	if r.a.TabCount() != 2 || r.a.ActiveTab() != 1 {
		t.Fatalf("tabs %d active %d", r.a.TabCount(), r.a.ActiveTab())
	}
	// `:new host` always opens a tab.
	r.a.RunCommand("new pub400.com")
	waitFor(t, "third tab", func() bool { return r.d.count() == 4 && r.isConnected() })
	if r.a.TabCount() != 3 || r.a.ActiveTab() != 2 {
		t.Fatalf("tabs %d active %d", r.a.TabCount(), r.a.ActiveTab())
	}
}

func TestTabBarDraw(t *testing.T) {
	sim := simScreen(t, 60, 1)
	infos := []TabInfo{
		{Title: "PUB400", Connected: true},
		{Title: "agent", Active: true, Connected: true},
		{Title: "dev", Connecting: true},
		{Title: "old"},
	}
	b := NewTabBar(func() []TabInfo { return infos })
	b.SetRect(0, 0, 60, 1)
	b.Draw(sim)
	sim.Show()
	row := simRow(sim, 0)
	want := " 1 PUB400   2 agent   3 dev...   4 old "
	if !strings.HasPrefix(row, want) {
		t.Fatalf("row %q, want prefix %q", row, want)
	}
	// The active tab is reverse video, a dropped one dimmed.
	_, fg, _ := cellAt(sim, 13, 0) // inside " 2 agent "
	if fg != oiaBg {
		t.Fatalf("active tab fg %v", fg)
	}
	_, fg, _ = cellAt(sim, 35, 0) // inside " 4 old "
	if fg != tabIdleFg {
		t.Fatalf("idle tab fg %v", fg)
	}
	// Too narrow: the active tab scrolls into view.
	narrow := simScreen(t, 20, 1)
	infos = []TabInfo{{Title: "PUB400"}, {Title: "second"}, {Title: "third", Active: true}}
	b.SetRect(0, 0, 20, 1)
	b.Draw(narrow)
	narrow.Show()
	if row := simRow(narrow, 0); !strings.Contains(row, "3 third") {
		t.Fatalf("narrow row %q", row)
	}
}

func TestTabBarShownForTwoTabs(t *testing.T) {
	r := newRig(t, func(o *AppOptions) { o.Target = "PUB400.COM" })
	sim := simScreen(t, 100, 30)
	r.a.post = func(f func()) { r.a.app.QueueUpdateDraw(f) }
	r.a.SetScreen(sim)
	done := make(chan error, 1)
	go func() { done <- r.a.Run() }()
	waitFor(t, "connected", r.isConnected)
	sim.SetSize(100, 30)
	waitFor(t, "drawn", func() bool { return simHas(sim, "Sign On") })
	if strings.Contains(simRow(sim, 0), "1 PUB400.COM") {
		t.Fatal("tab bar shown with a single tab")
	}
	r.a.app.QueueUpdateDraw(func() { r.a.RunCommand("connect pub400.com:2323") })
	waitFor(t, "two tabs drawn", func() bool { return strings.Contains(simRow(sim, 0), "2 pub400.com") })
	if !strings.Contains(simRow(sim, 0), "1 PUB400.COM") {
		t.Fatalf("row 0 %q", simRow(sim, 0))
	}
	r.a.app.QueueUpdateDraw(func() { r.a.RunCommand("close") })
	waitFor(t, "bar hidden", func() bool {
		return !strings.Contains(simRow(sim, 0), "PUB400.COM") || strings.Contains(simRow(sim, 0), "Sign On")
	})
	r.a.app.QueueUpdateDraw(func() { r.a.Quit() })
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
