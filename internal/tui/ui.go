package tui

import (
	"fmt"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// handleKey is the application-wide input capture: it turns key events into
// engine actions (see KeyTable) unless an overlay or the command bar is open,
// in which case tview's own widgets get the keys.
func (a *App) handleKey(ev *tcell.EventKey) *tcell.EventKey {
	if len(a.overlays) > 0 || a.barOpen {
		if ev.Key() == tcell.KeyCtrlC && !a.barOpen {
			if a.topOverlay() == "quit" {
				a.Quit()
			} else {
				a.showQuit()
			}
			return nil
		}
		return ev
	}
	a.setNotice("")
	action, text, ok := KeyAction(ev)
	if !ok {
		return nil
	}
	switch {
	case action == ActCommandBar:
		a.openCommandBar()
	case action == ActQuit:
		a.showQuit()
	case text != "":
		a.typeText(text)
	default:
		a.pressKey(action)
	}
	return nil
}

// typeText sends printable text to the engine; ':' outside a field opens the
// command bar instead.
func (a *App) typeText(text string) {
	a.mu.Lock()
	if a.engine == nil || !a.connected {
		a.mu.Unlock()
		if text == ":" {
			a.openCommandBar()
		}
		return
	}
	if text == ":" {
		snap := a.engine.Snapshot()
		if !snap.CursorInField() {
			a.mu.Unlock()
			a.openCommandBar()
			return
		}
	}
	a.engine.Type(text)
	a.mu.Unlock()
}

// pressKey sends an engine action; Enter on a dropped connection reconnects.
func (a *App) pressKey(action string) {
	a.mu.Lock()
	if a.engine == nil || !a.connected {
		idle := !a.connecting
		a.mu.Unlock()
		if action == "enter" && idle {
			a.reconnect()
		}
		return
	}
	a.engine.Press(action)
	a.mu.Unlock()
}

// ---- overlays --------------------------------------------------------------

func (a *App) topOverlay() string {
	if len(a.overlays) == 0 {
		return ""
	}
	return a.overlays[len(a.overlays)-1].name
}

// showOverlay puts p on top of the screen. When w and h are > 0 it is centred
// at that size; otherwise p is added as is (tview.Modal centres itself).
func (a *App) showOverlay(name string, p tview.Primitive, w, h int) {
	a.closeCommandBar()
	var item tview.Primitive = p
	if w > 0 && h > 0 {
		item = tview.NewFlex().SetDirection(tview.FlexRow).
			AddItem(nil, 0, 1, false).
			AddItem(tview.NewFlex().
				AddItem(nil, 0, 1, false).
				AddItem(p, w, 0, true).
				AddItem(nil, 0, 1, false), h, 0, true).
			AddItem(nil, 0, 1, false)
	}
	a.pages.AddPage(name, item, true, true)
	a.overlays = append(a.overlays, overlay{name: name, focus: p})
	a.app.SetFocus(p)
}

// closeOverlay removes the top overlay and returns focus below it.
func (a *App) closeOverlay() {
	if len(a.overlays) == 0 {
		return
	}
	top := a.overlays[len(a.overlays)-1]
	a.overlays = a.overlays[:len(a.overlays)-1]
	a.pages.RemovePage(top.name)
	if n := len(a.overlays); n > 0 {
		a.app.SetFocus(a.overlays[n-1].focus)
	} else {
		a.app.SetFocus(a.screen)
	}
}

// replaceOverlay closes the overlay called name if it is on top.
func (a *App) closeNamed(name string) {
	if a.topOverlay() == name {
		a.closeOverlay()
	}
}

func (a *App) showQuit() {
	if a.topOverlay() == "quit" {
		return
	}
	m := tview.NewModal().SetText("Quit tn5250-tui?").AddButtons([]string{"Quit", "Cancel"})
	m.SetDoneFunc(func(i int, label string) {
		if label == "Quit" {
			a.Quit()
			return
		}
		a.closeNamed("quit")
	})
	a.showOverlay("quit", m, 0, 0)
}

// HelpText is the content of the help modal.
func HelpText() string {
	var b strings.Builder
	b.WriteString("Keys\n")
	for _, k := range HelpKeys() {
		fmt.Fprintf(&b, "  %-34s %s\n", k[0], k[1])
	}
	b.WriteString("\nCommands (Ctrl+K, or : outside a field)\n")
	for _, c := range CommandHelp() {
		fmt.Fprintf(&b, "  %-34s %s\n", c[0], c[1])
	}
	b.WriteString("\nEsc / Enter closes this window.")
	return b.String()
}

func (a *App) showHelp() {
	if a.topOverlay() == "help" {
		return
	}
	tv := tview.NewTextView().SetText(HelpText()).SetScrollable(true)
	tv.SetBorder(true).SetTitle(" Help ")
	tv.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		switch ev.Key() {
		case tcell.KeyEscape, tcell.KeyEnter:
			a.closeNamed("help")
			return nil
		case tcell.KeyRune:
			if ev.Rune() == 'q' {
				a.closeNamed("help")
				return nil
			}
		}
		return ev
	})
	a.showOverlay("help", tv, 98, 34)
}
