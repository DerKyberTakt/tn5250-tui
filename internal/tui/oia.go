package tui

import (
	"fmt"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"github.com/rivo/uniseg"
)

// FormatOIA builds the status line text: left part (connection, X SYSTEM /
// X II, INS, MW, device, message) and right part (row,col, 1-based). A
// non-empty notice (app message such as "unknown command") replaces the
// engine's own message.
func FormatOIA(o OIA, cursorRow, cursorCol int, notice string) (left, right string) {
	var parts []string
	if o.Connected {
		parts = append(parts, "Connected")
	} else {
		parts = append(parts, "Disconnected")
	}
	switch o.InputInhibited {
	case "system":
		parts = append(parts, "X SYSTEM")
	case "error":
		parts = append(parts, "X II")
	}
	if o.Insert {
		parts = append(parts, "INS")
	}
	if o.MessageWaiting {
		parts = append(parts, "MW")
	}
	if o.DeviceName != "" {
		parts = append(parts, o.DeviceName)
	}
	msg := o.Message
	if notice != "" {
		msg = notice
	}
	if msg != "" {
		parts = append(parts, msg)
	}
	return strings.Join(parts, "  "), fmt.Sprintf("%d,%d", cursorRow+1, cursorCol+1)
}

// OIABar is the one-line status primitive at the bottom of the window.
type OIABar struct {
	*tview.Box
	source func() (left, right string, alert bool)
}

// NewOIABar returns a status line reading its text from source; alert makes
// the left text stand out (input inhibited, error message, disconnected).
func NewOIABar(source func() (left, right string, alert bool)) *OIABar {
	b := &OIABar{Box: tview.NewBox(), source: source}
	return b
}

var (
	oiaBg    = tcell.NewRGBColor(0x1c, 0x22, 0x30)
	oiaFg    = tcell.NewRGBColor(0xc8, 0xd0, 0xe0)
	oiaAlert = tcell.NewRGBColor(0xf0, 0xe0, 0x40)
)

// putString draws s at x,y (at most maxWidth columns) and returns the columns used.
func putString(scr tcell.Screen, x, y, maxWidth int, s string, st tcell.Style) int {
	used := 0
	for _, r := range s {
		rw := uniseg.StringWidth(string(r))
		if rw == 0 {
			continue
		}
		if used+rw > maxWidth {
			break
		}
		scr.SetContent(x+used, y, r, nil, st)
		used += rw
	}
	return used
}

// Draw implements tview.Primitive.
func (b *OIABar) Draw(scr tcell.Screen) {
	x, y, w, h := b.GetRect()
	if w <= 0 || h <= 0 {
		return
	}
	left, right, alert := b.source()
	st := tcell.StyleDefault.Background(oiaBg).Foreground(oiaFg)
	for cx := x; cx < x+w; cx++ {
		scr.SetContent(cx, y, ' ', nil, st)
	}
	lst := st
	if alert {
		lst = st.Foreground(oiaAlert)
	}
	rw := uniseg.StringWidth(right)
	putString(scr, x+1, y, w-rw-3, left, lst)
	if w > rw+2 {
		putString(scr, x+w-rw-1, y, rw, right, st)
	}
}
