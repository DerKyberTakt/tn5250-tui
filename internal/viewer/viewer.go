package viewer

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/DerKyberTakt/tn5250-tui/internal/tui"
)

// Viewer renders one session of a server live, read-only. Keys: q / Esc / Ctrl+C leave,
// Tab watches the next session, r reloads the session list.
type Viewer struct {
	cl     *Client
	app    *tview.Application
	screen *tui.Screen
	oia    *tui.OIABar
	label  string

	mu       sync.Mutex
	sessions []SessionInfo
	cur      int
	frame    Frame
	haveFrm  bool
	notice   string
	stop     func()
	gen      int  // counts watches: a stream that was replaced or stopped must not act
	gone     bool // the server behind the watched session went away
	stopped  atomic.Bool
	ended    atomic.Bool

	// ExitOnClose, when positive, ends Run that long after the watched session closed (the
	// viewer says so first), so a viewer never sits on a dead session. Zero keeps it open.
	ExitOnClose time.Duration
}

// Ended reports whether Run returned because the watched session closed (ExitOnClose), not
// because the user left.
func (v *Viewer) Ended() bool { return v.ended.Load() }

// New builds a viewer on an open client. label names the server (shown on the OIA line).
func New(cl *Client, label string) *Viewer {
	v := &Viewer{cl: cl, label: label, sessions: cl.sessions}
	v.app = tview.NewApplication()
	v.screen = tui.NewScreen(v.screenSource)
	v.screen.Highlight = true
	v.oia = tui.NewOIABar(v.oiaSource)
	flex := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(v.screen, 0, 1, true).
		AddItem(v.oia, 1, 0, false)
	v.app.SetRoot(flex, true).SetFocus(v.screen)
	v.app.SetInputCapture(v.key)
	return v
}

// SetScreen installs a tcell screen (tests).
func (v *Viewer) SetScreen(s tcell.Screen) { v.app.SetScreen(s) }

// Run watches session (or the first one when "") until the user leaves.
func (v *Viewer) Run(session string) error {
	v.mu.Lock()
	v.cur = 0
	for i, s := range v.sessions {
		if strings.EqualFold(s.Session, session) {
			v.cur = i
		}
	}
	v.mu.Unlock()
	v.watchCurrent()
	err := v.app.Run()
	v.stopped.Store(true)
	v.mu.Lock()
	if v.stop != nil {
		v.stop()
	}
	v.mu.Unlock()
	return err
}

// Stop ends Run (tests).
func (v *Viewer) Stop() { v.stopped.Store(true); v.app.Stop() }

func (v *Viewer) post(f func()) {
	if v.stopped.Load() {
		return
	}
	v.app.QueueUpdateDraw(f)
}

// watchCurrent (re)starts the frame stream of the current session.
func (v *Viewer) watchCurrent() {
	v.mu.Lock()
	if v.stop != nil {
		v.stop()
		v.stop = nil
	}
	v.haveFrm = false
	v.gen++
	gen := v.gen
	if len(v.sessions) == 0 {
		v.notice = "no sessions on " + v.label + " (r reloads, q quits)"
		v.mu.Unlock()
		return
	}
	s := v.sessions[v.cur]
	v.notice = ""
	v.mu.Unlock()
	frames, stop, errc := v.cl.Watch(s.Session)
	v.mu.Lock()
	v.stop = stop
	v.mu.Unlock()
	go func() {
		for f := range frames {
			f := f
			v.post(func() {
				v.mu.Lock()
				v.frame, v.haveFrm = f, true
				v.mu.Unlock()
			})
		}
		err := <-errc
		v.mu.Lock()
		current := v.gen == gen // false: the user switched sessions or reloaded
		v.mu.Unlock()
		if !current {
			return
		}
		secs := int(v.ExitOnClose.Round(time.Second) / time.Second)
		v.post(func() {
			v.mu.Lock()
			switch {
			case err != nil:
				// The server went away (the agent's MCP server exited): nothing more will come.
				v.gone = true
				v.notice = "agent server stopped (" + err.Error() + "); q or Esc closes"
				if v.ExitOnClose > 0 {
					v.notice = fmt.Sprintf("agent server stopped; closing in %d s", secs)
				}
			case v.haveFrm:
				v.notice = "session " + s.Session + " closed (r reloads, Tab next, q quits)"
				if v.ExitOnClose > 0 {
					v.notice = fmt.Sprintf("session %s ended; closing in %d s", s.Session, secs)
				}
			}
			v.mu.Unlock()
		})
		if v.ExitOnClose > 0 && !v.stopped.Load() {
			time.AfterFunc(v.ExitOnClose, func() {
				if v.stopped.Load() {
					return
				}
				v.ended.Store(true)
				v.Stop()
			})
		}
	}()
}

func (v *Viewer) reload() {
	v.mu.Lock()
	if v.stop != nil { // the stream holds the client; stop it before asking for the list
		v.stop()
		v.stop = nil
	}
	v.mu.Unlock()
	list, err := v.cl.Sessions()
	v.mu.Lock()
	if err != nil {
		v.notice = "list: " + err.Error()
		v.mu.Unlock()
		return
	}
	v.sessions = list
	if v.cur >= len(list) {
		v.cur = 0
	}
	v.mu.Unlock()
	v.watchCurrent()
}

func (v *Viewer) key(ev *tcell.EventKey) *tcell.EventKey {
	switch ev.Key() {
	case tcell.KeyEscape, tcell.KeyCtrlC, tcell.KeyCtrlQ:
		v.Stop()
		return nil
	case tcell.KeyTab:
		v.mu.Lock()
		if v.gone {
			v.mu.Unlock()
			return nil
		}
		if n := len(v.sessions); n > 0 {
			v.cur = (v.cur + 1) % n
		}
		v.mu.Unlock()
		v.watchCurrent()
		return nil
	case tcell.KeyRune:
		switch ev.Rune() {
		case 'q', 'Q':
			v.Stop()
			return nil
		case 'r', 'R':
			v.mu.Lock()
			gone := v.gone
			v.mu.Unlock()
			if gone {
				return nil
			}
			go v.reload()
			return nil
		}
	}
	return nil // read-only: nothing else reaches the screen
}

func (v *Viewer) screenSource() (tui.Snapshot, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if !v.haveFrm {
		return tui.Snapshot{}, false
	}
	return v.frame.Snapshot(), true
}

func (v *Viewer) oiaSource() (string, string, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	var o tui.OIA
	row, col := 0, 0
	name := "-"
	if len(v.sessions) > 0 {
		name = v.sessions[v.cur].Session
	}
	if v.haveFrm {
		o = v.frame.TUIOIA()
		row, col = v.frame.CursorRow, v.frame.CursorCol
	}
	if v.gone {
		o.Connected = false // the last screen stays readable, but nothing behind it is live
	}
	left, right := tui.FormatOIA(o, row, col, v.notice)
	left = fmt.Sprintf("WATCH %s %d/%d  ", name, v.cur+1, len(v.sessions)) + left
	alert := v.notice != "" || !o.Connected || o.Message != ""
	return left, right, alert
}

// Describe is a one-line summary of a server entry for lists.
func Describe(t TokenFile) string {
	age := time.Since(t.Started).Round(time.Minute)
	return fmt.Sprintf("pid %d  %s  %s  started %s ago", t.PID, t.Addr, t.Label, age)
}
