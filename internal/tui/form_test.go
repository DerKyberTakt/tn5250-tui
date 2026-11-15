package tui

import (
	"strconv"
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/DerKyberTakt/tn5250-tui/internal/sites"
)

// formApp mounts f on a simulation screen with a real tview application (not run), so
// focus moves the way it does live and Draw paints the frame.
func formApp(t *testing.T, f *tview.Form, w, h int) (*tview.Application, tcell.SimulationScreen) {
	t.Helper()
	sim := simScreen(t, w, h)
	app := tview.NewApplication()
	app.SetScreen(sim)
	app.SetRoot(f, true)
	app.SetAfterDrawFunc(func(s tcell.Screen) { litFocusedField(s, f) })
	app.SetFocus(f)
	app.ForceDraw()
	return app, sim
}

func sendForm(app *tview.Application, f *tview.Form, k tcell.Key) {
	f.InputHandler()(tcell.NewEventKey(k, 0, tcell.ModNone), func(p tview.Primitive) { app.SetFocus(p) })
	app.ForceDraw()
}

// litRow returns the single screen row holding lit (white background) cells, or -1.
func litRow(t *testing.T, sim tcell.SimulationScreen) int {
	t.Helper()
	cells, w, h := sim.GetContents()
	row := -1
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if _, bg, _ := cells[y*w+x].Style.Decompose(); bg == litFieldColor {
				if row >= 0 && row != y {
					t.Fatalf("lit cells on rows %d and %d", row, y)
				}
				row = y
			}
		}
	}
	return row
}

func TestFormArrowsMoveBetweenFieldsAndLightTheFocusedOne(t *testing.T) {
	f := newSettingsForm(sites.Defaults{ScreenSize: "27x132", CodePage: 37}, func(sites.Defaults) {}, func() {}, func(string) {}, nil)
	f.SetBorder(true)
	app, sim := formApp(t, f, 64, 23)
	dd := f.GetFormItemByLabel("Screen size").(*tview.DropDown)
	if !dd.HasFocus() {
		t.Fatal("the first field should have the focus")
	}
	first := litRow(t, sim)
	if first < 0 {
		t.Fatal("focused drop-down not lit")
	}

	// Down leaves the drop-down closed and moves to the next field, which is now lit.
	sendForm(app, f, tcell.KeyDown)
	if dd.IsOpen() {
		t.Fatal("Down opened the drop-down")
	}
	if !f.GetFormItemByLabel("Code page").HasFocus() {
		t.Fatal("Down did not move to Code page")
	}
	if second := litRow(t, sim); second <= first {
		t.Fatalf("lit row %d after Down, was %d", second, first)
	}
	// Up goes back.
	sendForm(app, f, tcell.KeyUp)
	if !dd.HasFocus() || litRow(t, sim) != first {
		t.Fatal("Up did not move back to the drop-down")
	}
	// Enter opens the list; the arrows then move inside it and it stays open.
	sendForm(app, f, tcell.KeyEnter)
	if !dd.IsOpen() {
		t.Fatal("Enter did not open the drop-down")
	}
	sendForm(app, f, tcell.KeyDown)
	if !dd.IsOpen() || !dd.HasFocus() {
		t.Fatal("Down inside the open list must stay in the list")
	}
	sendForm(app, f, tcell.KeyEnter)
	if dd.IsOpen() {
		t.Fatal("Enter did not pick")
	}
	if _, opt := dd.GetCurrentOption(); opt != "24x80" {
		t.Fatalf("picked %q", opt)
	}
}

func TestFormCtrlSSaves(t *testing.T) {
	var saved *sites.Defaults
	f := newSettingsForm(sites.Defaults{ScreenSize: "27x132", CodePage: 37}, func(d sites.Defaults) { saved = &d }, func() {}, func(string) {}, nil)
	app, _ := formApp(t, f, 64, 23)
	setText(f, "Code page", "500")
	sendForm(app, f, tcell.KeyCtrlS)
	if saved == nil || saved.CodePage != 500 {
		t.Fatalf("Ctrl+S did not save: %+v", saved)
	}

	var site *sites.Site
	sf := newSiteForm(sites.Site{Name: "pub", Host: "PUB400.COM"}, func(s sites.Site) { site = &s }, func() {}, func(string) {})
	app, _ = formApp(t, sf, 64, 24)
	sendForm(app, sf, tcell.KeyCtrlS)
	if site == nil || site.Name != "pub" {
		t.Fatalf("Ctrl+S did not save the site: %+v", site)
	}
}

func TestScrollbarOnlyWhenTheListOverflows(t *testing.T) {
	l := tview.NewList().ShowSecondaryText(false)
	l.SetBorder(true)
	scrollbar(l.Box, true, func(h int) (int, int) { return listRows(l, false, h) })
	sim := simScreen(t, 20, 6)
	for i := 0; i < 3; i++ {
		l.AddItem("item "+strconv.Itoa(i), "", 0, nil)
	}
	l.SetRect(0, 0, 20, 6)
	l.Draw(sim)
	sim.Show()
	if _, _, w, _ := l.GetInnerRect(); w != 18 {
		t.Fatalf("inner width %d with 3 items, want the full 18", w)
	}
	for i := 3; i < 20; i++ {
		l.AddItem("item "+strconv.Itoa(i), "", 0, nil)
	}
	l.SetCurrentItem(19)
	l.Draw(sim)
	sim.Show()
	if _, _, w, _ := l.GetInnerRect(); w != 17 {
		t.Fatalf("inner width %d with 20 items, want 17", w)
	}
	thumb := 0
	for y := 1; y <= 4; y++ {
		if r, _, _ := cellAt(sim, 18, y); r == '█' {
			thumb++
		} else if r != '│' {
			t.Fatalf("row %d: %q in the bar column", y, r)
		}
	}
	if thumb < 1 || thumb > 3 {
		t.Fatalf("thumb rows %d", thumb)
	}
	if r, _, _ := cellAt(sim, 18, 4); r != '█' {
		t.Fatal("at the end of the list the thumb must sit at the bottom")
	}
}
