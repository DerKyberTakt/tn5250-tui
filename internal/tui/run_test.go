package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

func simRow(sim tcell.SimulationScreen, y int) string {
	cells, w, _ := sim.GetContents()
	var b strings.Builder
	for x := 0; x < w; x++ {
		c := cells[y*w+x]
		if len(c.Runes) > 0 {
			b.WriteRune(c.Runes[0])
		} else {
			b.WriteByte(' ')
		}
	}
	return b.String()
}

func simHas(sim tcell.SimulationScreen, want string) bool {
	_, _, h := sim.GetContents()
	for y := 0; y < h; y++ {
		if strings.Contains(simRow(sim, y), want) {
			return true
		}
	}
	return false
}

// TestRunEndToEnd drives the real tview event loop on a simulation screen.
func TestRunEndToEnd(t *testing.T) {
	r := newRig(t, func(o *AppOptions) { o.Target = "PUB400.COM" })
	sim := simScreen(t, 100, 30)
	r.a.post = func(f func()) { r.a.app.QueueUpdateDraw(f) }
	r.a.SetScreen(sim)
	done := make(chan error, 1)
	go func() { done <- r.a.Run() }()

	waitFor(t, "connected", r.isConnected)
	sim.SetSize(100, 30) // Run initialises the screen (80x25); resize afterwards
	waitFor(t, "resized", func() bool { c, _, _ := sim.GetContents(); return len(c) == 3000 })
	waitFor(t, "sign-on screen drawn", func() bool { return simHas(sim, "Sign On") })
	_, _, vis := sim.GetCursor()
	if !vis {
		t.Error("cursor not shown")
	}
	if !simHas(sim, "Connected") {
		t.Error("OIA line missing")
	}

	e := r.engine()
	e.clear()
	sim.InjectKey(tcell.KeyF1, 0, tcell.ModNone)
	sim.InjectKey(tcell.KeyF2, 0, tcell.ModShift)
	waitFor(t, "keys delivered", func() bool { return joinCalls(e.Calls()) == "press:pf1 press:pf14" })

	// command bar: Ctrl+K opens, typing goes to the bar, Enter runs, the bar closes
	sim.InjectKey(tcell.KeyCtrlK, 0, tcell.ModNone)
	waitFor(t, "bar open", func() bool { return strings.HasPrefix(strings.TrimSpace(simRow(sim, 28)), ":") })
	for _, c := range "key pf5" {
		sim.InjectKey(tcell.KeyRune, c, tcell.ModNone)
	}
	sim.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
	waitFor(t, "command ran", func() bool { return hasCall(e.Calls(), "press:pf5") })
	waitFor(t, "bar closed", func() bool { return !strings.HasPrefix(strings.TrimSpace(simRow(sim, 28)), ":") })

	// help modal opens and Esc closes it
	r.a.app.QueueUpdateDraw(func() { r.a.RunCommand("help") })
	waitFor(t, "help shown", func() bool { return simHas(sim, "Commands (Ctrl+K") })
	sim.InjectKey(tcell.KeyEscape, 0, tcell.ModNone)
	waitFor(t, "help closed", func() bool { return !simHas(sim, "Commands (Ctrl+K") })
	if hasCall(e.Calls(), "press:attn") {
		t.Error("Esc inside the help modal must not reach the engine")
	}

	// the host updates the screen: the redraw is driven by the engine's update callback
	e.mu.Lock()
	e.snap.put(10, 10, "NEW TEXT", 0x20)
	e.mu.Unlock()
	e.upd()
	waitFor(t, "update drawn", func() bool { return simHas(sim, "NEW TEXT") })

	// quit prompt: Enter on "Quit" ends Run
	sim.InjectKey(tcell.KeyCtrlQ, 0, tcell.ModNone)
	waitFor(t, "quit prompt", func() bool { return simHas(sim, "Quit tn5250-tui?") })
	sim.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return after Quit")
	}
	if _, l := r.d.last(); !l.isClosed() {
		t.Error("link not closed on quit")
	}
}
