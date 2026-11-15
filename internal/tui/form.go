package tui

import (
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// Form conventions shared by the login, site and settings forms:
//
//   - Up and Down move between fields like Shift+Tab and Tab; a drop-down opens with
//     Enter or a typed letter, and only inside the open list do the arrows pick;
//   - the field that has the cursor is lit (black on white, yellow label) so it stands
//     out from the others, which keep tview's blue;
//   - Ctrl+S presses Save.

var (
	litLabelColor = tcell.ColorYellow
	litTextColor  = tcell.ColorBlack
	litFieldColor = tcell.ColorWhite
)

// prepForm installs the key conventions on f (which draws a border) and its scroll
// bar; onSave may be nil (no Save key).
func prepForm(f *tview.Form, onSave func()) {
	scrollbar(f.Box, true, func(int) (int, int) { return formRows(f) })
	f.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		switch ev.Key() {
		case tcell.KeyCtrlS:
			if onSave != nil {
				onSave()
				return nil
			}
		case tcell.KeyDown, tcell.KeyUp:
			if dd, ok := focusedFormItem(f).(*tview.DropDown); ok && dd.IsOpen() {
				return ev // the open list takes its own arrows
			}
			if ev.Key() == tcell.KeyDown {
				return tcell.NewEventKey(tcell.KeyTab, 0, ev.Modifiers())
			}
			return tcell.NewEventKey(tcell.KeyBacktab, 0, ev.Modifiers())
		}
		return ev
	})
}

// focusedFormItem returns the form item that has the focus, or nil (a button, or none).
func focusedFormItem(f *tview.Form) tview.FormItem {
	for i := 0; i < f.GetFormItemCount(); i++ {
		if it := f.GetFormItem(i); it.HasFocus() {
			return it
		}
	}
	return nil
}

// litFocusedField redraws the focused item of f in the lit colours. It runs after the
// whole screen is drawn (the form resets every item's colours on each draw), so the
// item is drawn twice: once by the form, once here on top.
func litFocusedField(screen tcell.Screen, f *tview.Form) {
	n := f.GetFormItemCount()
	labelWidth := 0
	for i := 0; i < n; i++ {
		if w := tview.TaggedStringWidth(f.GetFormItem(i).GetLabel()); w > labelWidth {
			labelWidth = w
		}
	}
	labelWidth++ // the form's one space
	for i := 0; i < n; i++ {
		it := f.GetFormItem(i)
		if !it.HasFocus() {
			continue
		}
		if dd, ok := it.(*tview.DropDown); ok && dd.IsOpen() {
			return // the open list is the highlight
		}
		it.SetFormAttributes(labelWidth, litLabelColor, f.GetBackgroundColor(), litTextColor, litFieldColor)
		it.Draw(screen)
		return
	}
}

// afterDraw runs after every frame: the form on top of the overlays gets its focused
// field lit and its scroll bar drawn from where the items ended up (the form moves
// them after its box, and so the bar, is drawn).
func (a *App) afterDraw(screen tcell.Screen) {
	if n := len(a.overlays); n > 0 {
		if f, ok := a.overlays[n-1].focus.(*tview.Form); ok {
			litFocusedField(screen, f)
			x, y, w, h := f.GetRect()
			off, total := formRows(f)
			drawBar(screen, f.Box, x+1, y+1, w-2, h-2, off, total)
		}
	}
}

// formRows is the pos function of a form's scroll bar: how far the items are scrolled
// and how many rows they take, read from where the last draw put them (zero before
// the first draw, so no bar).
func formRows(f *tview.Form) (offset, total int) {
	n, nb := f.GetFormItemCount(), f.GetButtonCount()
	if n == 0 {
		return 0, 0
	}
	_, y0, _, _ := f.GetFormItem(0).GetRect()
	_, fy, _, _ := f.GetRect()
	innerY := fy + 1 // inside the border (GetInnerRect is not settled while the box is being drawn)
	var last tview.Primitive = f.GetFormItem(n - 1)
	if nb > 0 {
		last = f.GetButton(nb - 1)
	}
	_, ly, _, lh := last.GetRect()
	if ly+lh <= y0 {
		return 0, 0
	}
	return innerY - y0, ly + lh - y0
}

// scrollbar draws a vertical scroll bar in the last column of b when its content is
// taller than the box, and gives the content the columns to its left. pos gets the
// visible height and returns the first visible row and the number of rows; border says
// whether b draws one.
func scrollbar(b *tview.Box, border bool, pos func(h int) (offset, total int)) {
	b.SetDrawFunc(func(screen tcell.Screen, x, y, w, h int) (int, int, int, int) {
		if border {
			x, y, w, h = x+1, y+1, w-2, h-2
		}
		offset, total := pos(h)
		return drawBar(screen, b, x, y, w, h, offset, total)
	})
}

// drawBar draws the bar in the last column of the inner rect x, y, w, h when total
// rows overflow h, and returns the rect left for the content.
func drawBar(screen tcell.Screen, b *tview.Box, x, y, w, h, offset, total int) (int, int, int, int) {
	if w < 2 || h < 1 || total <= h {
		return x, y, w, h
	}
	thumb := h * h / total
	if thumb < 1 {
		thumb = 1
	}
	top := 0
	if offset > 0 {
		top = offset * (h - thumb) / (total - h)
	}
	if top > h-thumb {
		top = h - thumb
	}
	cx := x + w - 1
	for i := 0; i < h; i++ {
		r, st := '│', tcell.StyleDefault.Foreground(tcell.ColorGray).Background(b.GetBackgroundColor())
		if i >= top && i < top+thumb {
			r, st = '█', st.Foreground(tcell.ColorSilver)
		}
		screen.SetContent(cx, y+i, r, nil, st)
	}
	return x, y, w - 1, h
}

// listRows is the pos function of a List's scrollbar: the rows the list is about to
// show. The bar is drawn before the list moves its offset to keep the current item in
// view, so the same move is applied here (tview's List.Draw rule).
func listRows(l *tview.List, secondary bool, h int) (offset, total int) {
	o, _ := l.GetOffset()
	cur, n := l.GetCurrentItem(), l.GetItemCount()
	per := 1
	if secondary {
		per = 2
	}
	switch {
	case cur < o:
		o = cur
	case secondary && 2*(cur-o) >= h-1:
		o = (2*cur + 3 - h) / 2
	case !secondary && cur-o >= h:
		o = cur + 1 - h
	}
	return o * per, n * per
}

// textRows is the pos function of a TextView's scrollbar.
func textRows(t *tview.TextView, wrapped bool) func(int) (int, int) {
	return func(int) (int, int) {
		r, _ := t.GetScrollOffset()
		if wrapped {
			return r, t.GetWrappedLineCount()
		}
		return r, t.GetOriginalLineCount()
	}
}
