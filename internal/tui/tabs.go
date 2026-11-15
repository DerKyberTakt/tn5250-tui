package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"github.com/rivo/uniseg"

	"github.com/DerKyberTakt/tn5250-tui/internal/sites"
)

// TabInfo describes one tab for the tab bar.
type TabInfo struct {
	Title      string
	Active     bool
	Connected  bool
	Connecting bool
}

// TabBar is the one-row strip above the screen that names the open sessions. It is
// shown once there is more than one tab (the single-session layout stays as it was).
type TabBar struct {
	*tview.Box
	source func() []TabInfo
}

// NewTabBar returns a tab bar reading its tabs from source.
func NewTabBar(source func() []TabInfo) *TabBar {
	return &TabBar{Box: tview.NewBox(), source: source}
}

var (
	tabIdleFg = tcell.NewRGBColor(0x78, 0x82, 0x94)
	tabHint   = "Ctrl+PgUp/PgDn switch  Ctrl+T new  Ctrl+W close"
)

// Draw implements tview.Primitive: " 1 PUB400  2 agent " with the active tab in reverse
// video, a dropped one dimmed and a connecting one marked with "...". When the tabs do not
// fit, the leading ones scroll out so the active tab is always on view.
func (b *TabBar) Draw(scr tcell.Screen) {
	x, y, w, h := b.GetRect()
	if w <= 0 || h <= 0 {
		return
	}
	bg := tcell.StyleDefault.Background(oiaBg).Foreground(oiaFg)
	for cx := x; cx < x+w; cx++ {
		scr.SetContent(cx, y, ' ', nil, bg)
	}
	tabs := b.source()
	labels := make([]string, len(tabs))
	widths := make([]int, len(tabs))
	start, active := 0, 0
	for i, t := range tabs {
		labels[i] = tabLabel(i, t)
		widths[i] = uniseg.StringWidth(labels[i]) + 1 // one blank between tabs
		if t.Active {
			active = i
		}
	}
	// Scroll: drop leading tabs until the active one ends inside the width.
	for {
		end := 0
		for i := start; i <= active && i < len(tabs); i++ {
			end += widths[i]
		}
		if end <= w || start >= active {
			break
		}
		start++
	}
	cx := x
	for i := start; i < len(tabs) && cx < x+w; i++ {
		st := bg
		switch {
		case tabs[i].Active:
			st = tcell.StyleDefault.Background(oiaFg).Foreground(oiaBg).Bold(true)
		case !tabs[i].Connected && !tabs[i].Connecting:
			st = bg.Foreground(tabIdleFg)
		}
		cx += putString(scr, cx, y, x+w-cx, labels[i], st)
		if cx < x+w {
			scr.SetContent(cx, y, ' ', nil, bg)
			cx++
		}
	}
	// The key hint on the right, when there is room for it.
	hw := uniseg.StringWidth(tabHint)
	if x+w-cx >= hw+2 {
		putString(scr, x+w-hw-1, y, hw, tabHint, bg.Foreground(tabIdleFg))
	}
}

// tabLabel is the bar text of tab number i (0-based): " 1 PUB400 ", "..." while connecting.
func tabLabel(i int, t TabInfo) string {
	title := t.Title
	if t.Connecting {
		title += "..."
	}
	return " " + strconv.Itoa(i+1) + " " + title + " "
}

// ---- the App's tabs ----------------------------------------------------------

// tabTitle names a tab: the saved site, else the host, else "new".
func tabTitle(t *conn) string {
	switch {
	case t.curSaved != "":
		return t.curSaved
	case t.cur.Host != "":
		return t.cur.Host
	}
	return "new"
}

// tabInfos is the tab bar's source.
func (a *App) tabInfos() []TabInfo {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]TabInfo, len(a.tabs))
	for i, t := range a.tabs {
		out[i] = TabInfo{Title: tabTitle(t), Active: t == a.conn, Connected: t.connected, Connecting: t.connecting}
	}
	return out
}

// tabIndex is the active tab's position (0-based). The caller holds a.mu.
func (a *App) tabIndexLocked() int {
	for i, t := range a.tabs {
		if t == a.conn {
			return i
		}
	}
	return 0
}

// TabCount reports how many tabs are open.
func (a *App) TabCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.tabs)
}

// ActiveTab reports the active tab's position (0-based).
func (a *App) ActiveTab() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.tabIndexLocked()
}

// activate makes tab i the active one: the screen and the OIA line show it from now on.
// It runs on the UI goroutine.
func (a *App) activate(i int) {
	a.mu.Lock()
	if i < 0 || i >= len(a.tabs) {
		a.mu.Unlock()
		return
	}
	a.conn = a.tabs[i]
	label := a.conn.demoLabel
	a.mu.Unlock()
	a.screen.DemoLabel = label
	a.screen.hlSeq = 0 // another engine's snapshot may carry the same sequence number
	a.screen.scrollX, a.screen.scrollY = 0, 0
}

// newTab opens an empty tab, makes it active and returns it (UI goroutine).
func (a *App) newTab() *conn {
	t := &conn{}
	a.mu.Lock()
	a.tabs = append(a.tabs, t)
	n := len(a.tabs)
	a.mu.Unlock()
	a.activate(n - 1)
	a.layoutTabs()
	return t
}

// closeTab disconnects tab i and removes it; the last tab is emptied instead of removed.
// It returns the message for the OIA line (UI goroutine).
func (a *App) closeTab(i int) string {
	a.mu.Lock()
	if i < 0 || i >= len(a.tabs) {
		a.mu.Unlock()
		return "no such tab"
	}
	t := a.tabs[i]
	last := len(a.tabs) == 1
	a.mu.Unlock()
	a.disconnectTab(t)
	if last {
		a.mu.Lock()
		*t = conn{}
		a.mu.Unlock()
		a.screen.DemoLabel = ""
		return "Disconnected"
	}
	a.mu.Lock()
	wasActive := t == a.conn
	a.tabs = append(a.tabs[:i], a.tabs[i+1:]...)
	j := i
	if j >= len(a.tabs) {
		j = len(a.tabs) - 1
	}
	a.mu.Unlock()
	if wasActive {
		a.activate(j)
	}
	a.layoutTabs()
	return ""
}

// switchTab moves to another tab: "next", "prev" or a 1-based number.
func (a *App) switchTab(arg string) string {
	a.mu.Lock()
	n, cur := len(a.tabs), a.tabIndexLocked()
	a.mu.Unlock()
	i := cur
	switch strings.ToLower(arg) {
	case "next", "n":
		i = (cur + 1) % n
	case "prev", "p", "previous":
		i = (cur + n - 1) % n
	default:
		k, err := strconv.Atoi(arg)
		if err != nil {
			return "usage: tab <n>|next|prev"
		}
		if k < 1 || k > n {
			return fmt.Sprintf("no tab %d (%d open)", k, n)
		}
		i = k - 1
	}
	a.activate(i)
	return ""
}

// layoutTabs shows the tab bar when there is more than one tab (UI goroutine).
func (a *App) layoutTabs() {
	h := 0
	if a.TabCount() > 1 {
		h = 1
	}
	a.flex.ResizeItem(a.tabbar, h, 0)
}

// openNewTab is Ctrl+T / `:new`: a fresh tab with the site list (the login form when there
// are no sites), or connected to target when one is given.
func (a *App) openNewTab(target string, tlsFlag bool) string {
	if target == "" {
		a.newTab()
		if len(a.opts.Sites.Sites) == 0 {
			a.showLogin()
		} else {
			a.showSites()
		}
		return ""
	}
	site, saved, err := a.resolveTarget(target, tlsFlag)
	if err != nil {
		return err.Error()
	}
	a.dial(a.newTab(), site, saved, 0, "")
	return ""
}

// closeActiveTab is Ctrl+W: a connected tab asks first, an idle one closes at once.
func (a *App) closeActiveTab() {
	a.mu.Lock()
	i, live := a.tabIndexLocked(), a.connected || a.connecting
	title := tabTitle(a.conn)
	a.mu.Unlock()
	if !live {
		a.setNotice(a.closeTab(i))
		return
	}
	if a.topOverlay() == "closetab" {
		return
	}
	m := tview.NewModal().SetText(fmt.Sprintf("Close tab %d (%s)? It is connected.", i+1, title)).
		AddButtons([]string{"Close", "Cancel"})
	m.SetDoneFunc(func(_ int, label string) {
		a.closeNamed("closetab")
		if label == "Close" {
			a.setNotice(a.closeTab(i))
		}
	})
	a.showOverlay("closetab", m, 0, 0)
}

// connectedTabs counts the tabs with a live connection.
func (a *App) connectedTabs() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	n := 0
	for _, t := range a.tabs {
		if t.connected {
			n++
		}
	}
	return n
}

// siteOf is the active tab's target (for tests and the status line).
func (a *App) siteOf(i int) sites.Site {
	a.mu.Lock()
	defer a.mu.Unlock()
	if i < 0 || i >= len(a.tabs) {
		return sites.Site{}
	}
	return a.tabs[i].cur
}
