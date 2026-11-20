package main

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DerKyberTakt/tn5250-tui/internal/fakehost"
	"github.com/DerKyberTakt/tn5250-tui/internal/sites"
	"github.com/DerKyberTakt/tn5250-tui/internal/tui"
	"github.com/gdamore/tcell/v2"
)

// lockedSim serialises the simulation screen's drawing with the tests' reads of it: tcell
// hands out its live cell buffer from GetContents, which the event loop redraws concurrently.
type lockedSim struct {
	tcell.SimulationScreen
	mu sync.Mutex
}

func (l *lockedSim) Show() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.SimulationScreen.Show()
}

func (l *lockedSim) SetSize(w, h int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.SimulationScreen.SetSize(w, h)
}

func (l *lockedSim) GetContents() ([]tcell.SimCell, int, int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	cells, w, h := l.SimulationScreen.GetContents()
	return append([]tcell.SimCell(nil), cells...), w, h
}

func simText(sim tcell.SimulationScreen) string {
	cells, w, h := sim.GetContents()
	var b strings.Builder
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := cells[y*w+x]
			if len(c.Runes) > 0 {
				b.WriteRune(c.Runes[0])
			} else {
				b.WriteByte(' ')
			}
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// TestEndToEnd runs the real engine, the real client and the real TUI against the fake host
// replaying the PUB400 capture (27x132, strict byte matching), on a simulation screen.
func TestEndToEnd(t *testing.T) {
	capture, err := fakehost.LoadCapture("../../testdata/pub400-signon-3477fc.json")
	if err != nil {
		t.Fatal(err)
	}
	host, err := fakehost.StartWith("127.0.0.1:0", capture, fakehost.Options{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close()

	app := tui.New(tui.AppOptions{
		EngineFactory: newEngine,
		Sites:         &sites.File{},
		Target:        host.Addr(),
		ScreenSize:    "27x132",
	})
	sim := &lockedSim{SimulationScreen: tcell.NewSimulationScreen("UTF-8")}
	if err := sim.Init(); err != nil {
		t.Fatal(err)
	}
	app.SetScreen(sim)
	done := make(chan error, 1)
	go func() { done <- app.Run() }()
	defer func() {
		app.Quit()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("the app did not stop")
		}
	}()

	waitFor(t, "a first draw", func() bool { c, _, _ := sim.GetContents(); return len(c) > 0 })
	sim.SetSize(140, 32)
	waitFor(t, "the sign-on screen", func() bool { return strings.Contains(simText(sim), "Welcome to PUB400.COM") })
	text := simText(sim)
	for _, want := range []string{"Your user name:", "Password (max. 128):", "COPYRIGHT IBM CORP."} {
		if !strings.Contains(text, want) {
			t.Errorf("screen lacks %q:\n%s", want, text)
		}
	}
	if len(host.Mismatches()) != 0 {
		t.Errorf("the terminal's bytes did not match the capture: %v", host.Mismatches())
	}

	// Type a user name: it lands in the field where the cursor is.
	for _, r := range "alex" {
		sim.InjectKey(tcell.KeyRune, r, tcell.ModNone)
	}
	waitFor(t, "the typed user name", func() bool { return strings.Contains(simText(sim), "ALEX") || strings.Contains(simText(sim), "alex") })
	// Enter sends the record; the fake host answers with its canned screen.
	sim.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
	waitFor(t, "the fake host's reply", func() bool { return strings.Contains(simText(sim), "FAKE HOST") })
}
