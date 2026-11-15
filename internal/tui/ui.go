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
	// A tool tab (the SQL runner) takes every key but the global ones.
	a.mu.Lock()
	tool := a.tool
	a.mu.Unlock()
	if tool != nil && !globalKey(ev) {
		return ev
	}
	// Alt+1..9 switches tabs (a rune chord, so it is not in the key table).
	if ev.Key() == tcell.KeyRune && ev.Modifiers()&(tcell.ModAlt|tcell.ModCtrl) == tcell.ModAlt && ev.Rune() >= '1' && ev.Rune() <= '9' {
		if msg := a.switchTab(string(ev.Rune())); msg != "" {
			a.setNotice(msg)
		}
		return nil
	}
	action, text, ok := KeyAction(ev)
	if !ok {
		return nil
	}
	switch {
	case action == ActCommandBar:
		a.openCommandBar()
	case action == ActQuit:
		a.showQuit()
	case action == ActNewTab:
		a.setNotice(a.openNewTab("", false))
	case action == ActCloseTab:
		a.closeActiveTab()
	case action == ActNextTab:
		a.setNotice(a.switchTab("next"))
	case action == ActPrevTab:
		a.setNotice(a.switchTab("prev"))
	case action == ActSplit:
		a.setNotice(a.split(""))
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
	a.clearFlashLocked()
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
	a.clearFlashLocked()
	a.pressLocked(action)
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
		a.app.SetFocus(a.focusedPrimitive())
	}
}

// globalKey reports whether a key belongs to the App rather than the tab in front:
// the command bar, quit, and the tab and split keys.
func globalKey(ev *tcell.EventKey) bool {
	if ev.Key() == tcell.KeyRune && ev.Modifiers()&(tcell.ModAlt|tcell.ModCtrl) == tcell.ModAlt && ev.Rune() >= '1' && ev.Rune() <= '9' {
		return true
	}
	action, _, ok := KeyAction(ev)
	if !ok {
		return false
	}
	switch action {
	case ActCommandBar, ActQuit, ActNewTab, ActCloseTab, ActNextTab, ActPrevTab, ActSplit:
		return true
	}
	return false
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
	text := "Quit tn5250-tui?"
	if n := a.connectedTabs(); n > 1 {
		text = fmt.Sprintf("Quit tn5250-tui? %d sessions are connected.", n)
	}
	m := tview.NewModal().SetText(text).AddButtons([]string{"Quit", "Cancel"})
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
	tv := tview.NewTextView().SetText(HelpText()).SetScrollable(true).SetWrap(false)
	tv.SetBorder(true).SetTitle(" Help ")
	scrollbar(tv.Box, true, textRows(tv, false))
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
