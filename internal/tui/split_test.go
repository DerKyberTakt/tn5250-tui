package tui

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
)

func TestSplitStateTransitions(t *testing.T) {
	r, _, _ := twoTabs(t) // tab 2 (index 1) is active
	if r.a.isSplit() {
		t.Fatal("split before :split")
	}
	if msg := r.a.execute("unsplit"); msg != "not split" {
		t.Fatalf("unsplit: %q", msg)
	}
	if msg := r.a.execute("split 2"); !strings.Contains(msg, "in front") {
		t.Fatalf("split with the active tab: %q", msg)
	}
	if msg := r.a.execute("split"); msg != "" {
		t.Fatalf("split: %q", msg)
	}
	if !r.a.isSplit() || r.a.ActiveTab() != 1 || r.a.focusPane != 0 {
		t.Fatalf("split=%v active=%d pane=%d", r.a.isSplit(), r.a.ActiveTab(), r.a.focusPane)
	}
	infos := r.a.tabInfos()
	if !infos[1].Active || !infos[0].Split || infos[0].Active {
		t.Fatalf("tab infos %+v", infos)
	}
	// Ctrl+\ moves the keyboard to the other pane: tab 1 is active, drawn on the right.
	r.a.handleKey(key(tcell.KeyCtrlBackslash, 0, tcell.ModNone))
	if r.a.ActiveTab() != 0 || r.a.focusPane != 1 || r.a.focusedScreen() != r.a.screen2 {
		t.Fatalf("after swap: active=%d pane=%d", r.a.ActiveTab(), r.a.focusPane)
	}
	// Switching to the tab in the other pane swaps again instead of showing it twice.
	r.a.RunCommand("tab 2")
	if r.a.ActiveTab() != 1 || r.a.focusPane != 0 || !r.a.isSplit() {
		t.Fatalf("tab 2: active=%d pane=%d split=%v", r.a.ActiveTab(), r.a.focusPane, r.a.isSplit())
	}
	// A third tab replaces the focused pane's tab; the other pane keeps tab 1.
	r.a.RunCommand("connect pub400.com:3")
	waitFor(t, "third tab", func() bool { return r.a.TabCount() == 3 && r.isConnected() })
	if r.a.ActiveTab() != 2 || r.a.paneTabLocked(1) != r.a.tabs[0] {
		t.Fatalf("third tab: active=%d right=%p want %p", r.a.ActiveTab(), r.a.paneTabLocked(1), r.a.tabs[0])
	}
	// Closing the other pane's tab unsplits; the focused tab stays in front.
	r.a.closeTab(0)
	if r.a.isSplit() || r.a.TabCount() != 2 || r.a.ActiveTab() != 1 {
		t.Fatalf("after closing the other tab: split=%v tabs=%d active=%d", r.a.isSplit(), r.a.TabCount(), r.a.ActiveTab())
	}
	// Closing the focused tab when the neighbour is the other pane's tab: that tab stays, in one pane.
	r.a.RunCommand("split 1")
	other := r.a.tabs[0]
	r.a.RunCommand("close")
	if r.a.isSplit() || r.a.TabCount() != 1 || r.a.conn != other || r.a.focusPane != 0 {
		t.Fatalf("after closing the focused tab: split=%v tabs=%d", r.a.isSplit(), r.a.TabCount())
	}
	if msg := r.a.execute("split 5"); !strings.Contains(msg, "no tab 5") {
		t.Fatalf("%q", msg)
	}
	if msg := r.a.execute("split x"); msg != "usage: split [n]" {
		t.Fatalf("%q", msg)
	}
}

func TestSplitWithOneTabOpensANewOne(t *testing.T) {
	r := newRig(t, nil)
	r.connect(t, "pub400.com")
	if msg := r.a.execute("split"); msg != "" {
		t.Fatalf("%q", msg)
	}
	if r.a.TabCount() != 2 || r.a.ActiveTab() != 1 || r.a.focusPane != 1 || !r.a.isSplit() {
		t.Fatalf("tabs=%d active=%d pane=%d split=%v", r.a.TabCount(), r.a.ActiveTab(), r.a.focusPane, r.a.isSplit())
	}
	if r.a.topOverlay() != "login" { // no saved sites: the login form
		t.Fatalf("overlay %q", r.a.topOverlay())
	}
	r.a.closeOverlay()
	if r.a.focusedScreen() != r.a.screen2 {
		t.Fatal("focus should be on the right pane")
	}
	r.a.RunCommand("unsplit")
	if r.a.isSplit() || r.a.TabCount() != 2 || r.a.focusedScreen() != r.a.screen {
		t.Fatal("unsplit keeps both tabs in one pane")
	}
}

func TestSplitDrawsBothTabs(t *testing.T) {
	r := newRig(t, func(o *AppOptions) { o.Target = "PUB400.COM" })
	sim := simScreen(t, 170, 30)
	r.a.post = func(f func()) { r.a.app.QueueUpdateDraw(f) }
	r.a.SetScreen(sim)
	done := make(chan error, 1)
	go func() { done <- r.a.Run() }()
	waitFor(t, "connected", r.isConnected)
	sim.SetSize(170, 30)
	waitFor(t, "drawn", func() bool { return simHas(sim, "Sign On") })
	r.a.app.QueueUpdateDraw(func() { r.a.RunCommand("connect pub400.com:2323") })
	waitFor(t, "two tabs", func() bool { return r.a.TabCount() == 2 && r.isConnected() })
	r.mu.Lock()
	e2 := r.engines[1]
	r.mu.Unlock()
	second := newSnap(24, 80)
	second.put(1, 30, "SECOND TAB SCREEN", 0x22)
	e2.mu.Lock()
	e2.snap = second
	e2.mu.Unlock()
	r.a.app.QueueUpdateDraw(func() { r.a.RunCommand("split") })
	waitFor(t, "both drawn", func() bool { return simHas(sim, "SECOND TAB SCREEN") && simHas(sim, "Sign On") })
	// the active (second) tab is on the left, the first tab on the right of the divider
	if !strings.Contains(simRow(sim, 0), "2 pub400.com") {
		t.Fatalf("tab bar %q", simRow(sim, 0))
	}
	// Both titles sit on the same screen row (row 1 of each 24-row screen, centred in 29
	// rows); the active (second) tab is on the left of the divider, the first tab right.
	var left, right int = -1, -1
	for y := 0; y < 30; y++ {
		row := simRow(sim, y)
		if i := strings.Index(row, "SECOND TAB SCREEN"); i >= 0 {
			left = i
		}
		if i := strings.Index(row, "Sign On"); i >= 0 {
			right = i
		}
	}
	if left < 0 || right < 0 || left > right || right < 85 {
		t.Fatalf("panes: second at %d, sign on at %d", left, right)
	}
	r.a.app.QueueUpdateDraw(func() { r.a.RunCommand("unsplit") })
	waitFor(t, "one pane", func() bool { return !simHas(sim, "Sign On") })
	r.a.app.QueueUpdateDraw(func() { r.a.Quit() })
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
