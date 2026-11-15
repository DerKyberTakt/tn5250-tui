package tui

import (
	"fmt"
	"strconv"

	"github.com/rivo/tview"
)

// Split view: two tabs side by side. The focused tab (a.conn, the one the keyboard and the
// OIA line belong to) sits in pane a.focusPane; a.other is the tab in the opposite pane and
// nil when the view is not split. The left widget is a.screen, the right one a.screen2; a
// one-column divider sits between them. Tab switching changes the focused pane's tab, and
// switching to the tab that is already in the other pane moves the focus there instead.

// isSplit reports whether a second pane is shown.
func (a *App) isSplit() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.other != nil
}

// paneTabLocked is the tab drawn in pane k (0 = left): nil when the pane is empty.
func (a *App) paneTabLocked(k int) *conn {
	if a.other == nil {
		if k == 0 {
			return a.conn
		}
		return nil
	}
	if k == a.focusPane {
		return a.conn
	}
	return a.other
}

// paneSource is the Screen source of pane k.
func (a *App) paneSource(k int) func() (Snapshot, bool) {
	return func() (Snapshot, bool) {
		a.mu.Lock()
		defer a.mu.Unlock()
		t := a.paneTabLocked(k)
		if t == nil || t.engine == nil {
			return Snapshot{}, false
		}
		return t.engine.Snapshot(), true
	}
}

// focusedScreen is the widget that owns the keyboard cursor.
func (a *App) focusedScreen() *Screen {
	a.mu.Lock()
	k := a.focusPane
	split := a.other != nil
	a.mu.Unlock()
	if split && k == 1 {
		return a.screen2
	}
	return a.screen
}

// focusedPrimitive is the widget the keyboard belongs to: the focused pane's screen, or
// the tool inside a tool tab.
func (a *App) focusedPrimitive() tview.Primitive {
	a.mu.Lock()
	tool := a.tool
	a.mu.Unlock()
	if tool != nil {
		return tool.focus()
	}
	return a.focusedScreen()
}

// eachScreen applies f to both pane widgets (settings that are not per tab).
func (a *App) eachScreen(f func(*Screen)) {
	f(a.screen)
	f(a.screen2)
}

// syncScreens refreshes what each pane widget knows about its tab (demo label, highlight
// cache, scroll), shows or hides the second pane and moves the tview focus (UI goroutine).
func (a *App) syncScreens() {
	a.mu.Lock()
	split := a.other != nil
	var labels [2]string
	widgets := [2]tview.Primitive{a.screen, a.screen2}
	for k := 0; k < 2; k++ {
		if t := a.paneTabLocked(k); t != nil {
			labels[k] = t.demoLabel
			if t.tool != nil {
				widgets[k] = t.tool.root
			}
		}
	}
	a.mu.Unlock()
	for k, s := range []*Screen{a.screen, a.screen2} {
		s.DemoLabel = labels[k]
		s.hlSeq = 0 // another engine's snapshot may carry the same sequence number
		s.scrollX, s.scrollY = 0, 0
	}
	a.panes.Clear()
	a.panes.AddItem(widgets[0], 0, 1, true)
	if split {
		a.panes.AddItem(a.divider, 1, 0, false)
		a.panes.AddItem(widgets[1], 0, 1, false)
	}
	a.splitShown = split
	if len(a.overlays) == 0 && !a.barOpen {
		a.app.SetFocus(a.focusedPrimitive())
	}
}

// split is `:split [n]` and Ctrl+\: with a tab number, that tab goes into the other pane;
// without one, an unsplit view opens the next tab beside this one (a new tab when this is
// the only one) and a split view moves the focus to the other pane.
func (a *App) split(arg string) string {
	a.mu.Lock()
	n, cur, split := len(a.tabs), a.tabIndexLocked(), a.other != nil
	a.mu.Unlock()
	if arg == "" {
		if split {
			return a.swapPanes()
		}
		if n == 1 {
			prev := a.conn
			a.newTab() // focused, as the active tab
			a.mu.Lock()
			a.other, a.focusPane = prev, 1
			a.mu.Unlock()
			a.syncScreens()
			if len(a.opts.Sites.Sites) == 0 {
				a.showLogin()
			} else {
				a.showSites()
			}
			return ""
		}
		arg = strconv.Itoa((cur+1)%n + 1)
	}
	k, err := strconv.Atoi(arg)
	if err != nil {
		return "usage: split [n]"
	}
	if k < 1 || k > n {
		return fmt.Sprintf("no tab %d (%d open)", k, n)
	}
	a.mu.Lock()
	t := a.tabs[k-1]
	if t == a.conn {
		a.mu.Unlock()
		return fmt.Sprintf("tab %d is the one in front; name the tab for the other pane", k)
	}
	a.other = t
	a.mu.Unlock()
	a.syncScreens()
	return ""
}

// unsplit closes the second pane; its tab stays open.
func (a *App) unsplit() string {
	a.mu.Lock()
	if a.other == nil {
		a.mu.Unlock()
		return "not split"
	}
	a.other, a.focusPane = nil, 0
	a.mu.Unlock()
	a.syncScreens()
	return ""
}

// swapPanes moves the keyboard to the other pane.
func (a *App) swapPanes() string {
	a.mu.Lock()
	if a.other == nil {
		a.mu.Unlock()
		return "not split (:split opens a second pane)"
	}
	a.conn, a.other = a.other, a.conn
	a.focusPane ^= 1
	a.mu.Unlock()
	a.syncScreens()
	return ""
}
