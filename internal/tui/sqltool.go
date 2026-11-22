package tui

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/DerKyberTakt/tn5250-tui/internal/runscript"
	"github.com/DerKyberTakt/tn5250-tui/internal/sites"
)

// The SQL tool is a tab of its own: a script editor over a result grid, run against the
// site's host servers (database and remote command) with the site's user and password.
// Every statement is run as what it looks like (SQL, a CL command, a Qshell line; see
// package runscript), and the status line says which.

// sqlBlock is how many rows a query brings at a time; PgDn at the bottom fetches more.
const sqlBlock = 100

// runscriptOptions builds the host-server options for a site. Tests point it at fakes.
var runscriptOptions = func(s sites.Site, naming string) runscript.Options {
	return runscript.Options{Host: s.Host, TLS: s.TLS, TrustAnyCertificate: s.TrustAnyCertificate,
		User: s.User, Password: s.Password, Naming: naming}
}

type sqlTool struct {
	a      *App
	tab    *conn
	name   string // the site name or host, for the tab title
	root   *tview.Flex
	editor *tview.TextArea
	table  *tview.Table
	out    *tview.TextView
	sess   *runscript.Session

	vmu      sync.Mutex // held while a result is drawn into table and out; readers on other goroutines (tests) take it too
	mu       sync.Mutex
	running  bool
	fetching bool
	more     bool
	onTable  bool // the keyboard is on the result grid
	closed   bool
	lastKind string
}

var (
	sqlHeaderStyle = tcell.StyleDefault.Foreground(colWhite).Background(colBlack).Bold(true)
	sqlErrColor    = colRed
	sqlNoteColor   = colTurquoise
)

func newSQLTool(a *App, tab *conn, site sites.Site, name, naming string) *sqlTool {
	t := &sqlTool{a: a, tab: tab, name: name}
	t.sess = runscript.New(runscriptOptions(site, naming))
	t.editor = tview.NewTextArea().SetWrap(false)
	t.editor.SetPlaceholder("SQL, CL commands and Qshell lines, one statement per line or ended with ;   Ctrl+R (F5) runs the statement at the cursor or the selection, Ctrl+A then Ctrl+R (or F9) the whole script")
	t.editor.SetBorder(true).SetTitle(fmt.Sprintf(" %s  Ctrl+R run  Ctrl+A select all  F9 run all  Tab results  F3 close ", name)).SetTitleAlign(tview.AlignLeft)
	t.editor.SetBackgroundColor(colBlack)
	t.editor.SetTextStyle(tcell.StyleDefault.Background(colBlack).Foreground(colGreen))
	t.table = tview.NewTable().SetFixed(1, 0).SetSelectable(true, false).SetBorders(false)
	t.table.SetBorder(true).SetTitle(" Results ").SetTitleAlign(tview.AlignLeft)
	t.table.SetBackgroundColor(colBlack)
	t.table.SetSelectedStyle(tcell.StyleDefault.Background(oiaFg).Foreground(oiaBg))
	t.out = tview.NewTextView().SetDynamicColors(false).SetScrollable(true).SetWrap(true)
	t.out.SetBackgroundColor(oiaBg)
	t.out.SetTextColor(oiaFg)
	scrollbar(t.out.Box, false, textRows(t.out, true))
	t.root = tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(t.editor, 0, 2, true).
		AddItem(t.table, 0, 3, false).
		AddItem(t.out, 3, 0, false)
	t.editor.SetInputCapture(t.handleKey)
	t.table.SetInputCapture(t.handleKey)
	t.status("Ready. Nothing connects until the first statement runs.")
	return t
}

// focus is the widget the keyboard belongs to inside the tool.
func (t *sqlTool) focus() tview.Primitive {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.onTable {
		return t.table
	}
	return t.editor
}

func (t *sqlTool) status(text string) {
	t.out.SetText(text)
	t.out.ScrollToBeginning()
}

func (t *sqlTool) isRunning() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.running
}

// handleKey is the tool's own key table (the App handles the global keys before this).
func (t *sqlTool) handleKey(ev *tcell.EventKey) *tcell.EventKey {
	switch ev.Key() {
	case tcell.KeyCtrlR:
		// Ctrl+R runs the statement at the cursor, or the selection when there is one: Ctrl+A
		// then Ctrl+R runs the whole script. Enter and Ctrl+Enter only break the line.
		t.runSelected()
		return nil
	case tcell.KeyCtrlA:
		t.mu.Lock()
		onTable := t.onTable
		t.mu.Unlock()
		if onTable {
			return ev
		}
		t.editor.Select(0, len(t.editor.GetText()))
		return nil
	case tcell.KeyF5: // run the statement at the cursor, on every terminal
		t.runAtCursor()
		return nil
	case tcell.KeyF9:
		t.runAll()
		return nil
	case tcell.KeyF3:
		t.a.closeTab(t.a.tabIndexOf(t.tab))
		return nil
	case tcell.KeyTab, tcell.KeyBacktab:
		t.toggleFocus()
		return nil
	case tcell.KeyEsc:
		if t.isRunning() {
			// The cancel opens a side connection to the host: never on the UI goroutine.
			t.status("Cancelling...")
			go func() {
				err := t.sess.Cancel()
				t.a.post(func() {
					t.vmu.Lock()
					defer t.vmu.Unlock()
					if err != nil {
						t.status("cancel: " + err.Error())
					} else {
						t.status("Cancel sent; waiting for the host...")
					}
				})
			}()
			return nil
		}
		t.mu.Lock()
		onTable := t.onTable
		t.mu.Unlock()
		if onTable {
			t.toggleFocus()
			return nil
		}
		return ev
	case tcell.KeyCtrlL:
		t.table.Clear()
		t.status("Results cleared.")
		return nil
	case tcell.KeyPgDn:
		t.mu.Lock()
		onTable, more, fetching := t.onTable, t.more, t.fetching
		t.mu.Unlock()
		if onTable && more && !fetching {
			row, _ := t.table.GetSelection()
			if row >= t.table.GetRowCount()-1 {
				t.fetchMore()
				return nil
			}
		}
	}
	return ev
}

func (t *sqlTool) toggleFocus() {
	t.mu.Lock()
	t.onTable = !t.onTable && t.table.GetRowCount() > 1
	t.mu.Unlock()
	t.a.app.SetFocus(t.focus())
}

// statements parses the editor's text.
func (t *sqlTool) statements() []runscript.Statement {
	return runscript.Split(t.editor.GetText())
}

// runAtCursor is Ctrl+R without a selection (F5): the statement the cursor is in (or the last one above it).
func (t *sqlTool) runAtCursor() {
	row, _, _, _ := t.editor.GetCursor()
	st, ok := runscript.At(t.statements(), row)
	if !ok {
		t.status("Nothing to run: put the cursor on a statement.")
		return
	}
	t.run([]runscript.Statement{st})
}

// runSelected is Ctrl+R: the statements in the selection, else the one at the cursor.
func (t *sqlTool) runSelected() {
	if !t.editor.HasSelection() {
		t.runAtCursor()
		return
	}
	text, _, _ := t.editor.GetSelection()
	list := runscript.Split(text)
	if len(list) == 0 {
		t.status("Nothing to run in the selection.")
		return
	}
	t.run(list)
}

// runAll is F9 (or Ctrl+A, Ctrl+R): every statement in order, stopping at the first failure.
func (t *sqlTool) runAll() {
	all := t.statements()
	if len(all) == 0 {
		t.status("Nothing to run.")
		return
	}
	t.run(all)
}

// run runs the statements in the background and shows each result as it comes.
func (t *sqlTool) run(list []runscript.Statement) {
	t.mu.Lock()
	if t.running || t.closed {
		t.mu.Unlock()
		t.status("A statement is still running (Esc cancels it).")
		return
	}
	t.running = true
	t.mu.Unlock()
	t.status(fmt.Sprintf("Running as %s (%s): %s", list[0].Kind, list[0].Why, firstLine(list[0].Text)))
	go func() {
		var notes []string
		for i, st := range list {
			res, err := t.sess.Run(st.Classified, sqlBlock)
			done := i == len(list)-1
			failed := err != nil || res.Kind == "error"
			t.a.post(func() { t.show(st, res, err, notes, done || failed, i+1, len(list)) })
			if failed {
				break
			}
			if res.Kind != "rows" {
				notes = append(notes, summarize(st, res))
			}
		}
		t.mu.Lock()
		t.running = false
		t.mu.Unlock()
	}()
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i] + " ..."
	}
	if len(s) > 60 {
		s = s[:57] + "..."
	}
	return s
}

func summarize(st runscript.Statement, r runscript.Result) string {
	var s string
	switch r.Kind {
	case "count":
		s = fmt.Sprintf("%s: %d rows affected", firstLine(st.Text), r.UpdateCount)
	case "rows":
		s = fmt.Sprintf("%s: %d rows", firstLine(st.Text), len(r.Rows))
	default:
		s = firstLine(st.Text) + ": ran"
	}
	if len(r.Messages) > 0 {
		s += " · " + strings.Join(r.Messages, "; ")
	}
	return s
}

// show puts a result on the grid and the status line (UI goroutine).
func (t *sqlTool) show(st runscript.Statement, r runscript.Result, err error, earlier []string, last bool, n, of int) {
	t.vmu.Lock()
	defer t.vmu.Unlock()
	var b strings.Builder
	for _, e := range earlier {
		b.WriteString(e + "\n")
	}
	if err != nil {
		t.out.SetTextColor(sqlErrColor)
		b.WriteString(fmt.Sprintf("%s (%s): connection failed: %s", st.Kind, firstLine(st.Text), err.Error()))
		t.status(b.String())
		return
	}
	t.mu.Lock()
	t.more = r.More
	t.lastKind = r.Kind
	t.mu.Unlock()
	ms := r.Elapsed.Round(time.Millisecond)
	switch r.Kind {
	case "rows":
		t.fill(r.Columns, r.Rows, true)
		more := ""
		if r.More {
			more = ", PgDn on the last row fetches more"
		}
		b.WriteString(fmt.Sprintf("%s (%s): %d rows%s · %v", r.Statement.Kind, r.Statement.Why, len(r.Rows), more, ms))
		t.out.SetTextColor(oiaFg)
	case "count":
		t.table.Clear()
		b.WriteString(fmt.Sprintf("%s (%s): %d rows affected · %v", r.Statement.Kind, r.Statement.Why, r.UpdateCount, ms))
		t.out.SetTextColor(oiaFg)
	case "error":
		t.table.Clear()
		b.WriteString(fmt.Sprintf("%s (%s) failed · %v", r.Statement.Kind, r.Statement.Why, ms))
		t.out.SetTextColor(sqlErrColor)
	default:
		t.table.Clear()
		b.WriteString(fmt.Sprintf("%s (%s): ran · %v", r.Statement.Kind, r.Statement.Why, ms))
		t.out.SetTextColor(oiaFg)
	}
	if of > 1 {
		b.WriteString(fmt.Sprintf(" · statement %d of %d", n, of))
	}
	for _, m := range r.Messages {
		b.WriteString("\n" + m)
	}
	t.status(b.String())
	if r.Kind == "rows" {
		t.table.ScrollToBeginning()
		t.table.Select(1, 0)
	}
}

// fill draws columns and rows; with reset the grid starts over, else rows are appended.
func (t *sqlTool) fill(cols []string, rows [][]*string, reset bool) {
	start := t.table.GetRowCount()
	if reset {
		t.table.Clear()
		for c, name := range cols {
			t.table.SetCell(0, c, tview.NewTableCell(" "+name+" ").SetStyle(sqlHeaderStyle).SetSelectable(false).SetMaxWidth(40))
		}
		start = 1
	}
	for i, row := range rows {
		for c := range cols {
			text := "-"
			if c < len(row) && row[c] != nil {
				text = *row[c]
			}
			cell := tview.NewTableCell(" " + strings.ReplaceAll(text, "\n", " ") + " ").SetMaxWidth(40).SetTextColor(colGreen)
			t.table.SetCell(start+i, c, cell)
		}
	}
}

// fetchMore brings the next block of the open query.
func (t *sqlTool) fetchMore() {
	t.mu.Lock()
	if t.fetching || t.running {
		t.mu.Unlock()
		return
	}
	t.fetching = true
	t.mu.Unlock()
	t.status("Fetching...")
	go func() {
		res, err := t.sess.Fetch(sqlBlock)
		t.a.post(func() {
			t.vmu.Lock()
			defer t.vmu.Unlock()
			t.mu.Lock()
			t.fetching = false
			t.more = err == nil && res.More
			t.mu.Unlock()
			switch {
			case err != nil:
				t.out.SetTextColor(sqlErrColor)
				t.status("fetch: " + err.Error())
			case res.Kind == "error":
				t.out.SetTextColor(sqlErrColor)
				t.status("fetch failed: " + strings.Join(res.Messages, "; "))
			default:
				t.fill(res.Columns, res.Rows, false)
				more := ""
				if res.More {
					more = ", PgDn on the last row fetches more"
				}
				t.out.SetTextColor(oiaFg)
				t.status(fmt.Sprintf("%d rows so far%s", t.table.GetRowCount()-1, more))
			}
		})
	}()
}

// close ends the host-server connections (the tab is closing).
func (t *sqlTool) close() {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return
	}
	t.closed = true
	t.mu.Unlock()
	go t.sess.Close()
}

// ---- App side -----------------------------------------------------------------------

// tabIndexOf is the position of a tab, or 0.
func (a *App) tabIndexOf(t *conn) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	for i, c := range a.tabs {
		if c == t {
			return i
		}
	}
	return 0
}

// closeTool ends a tab's tool, if it has one.
func (c *conn) closeTool() {
	if c.tool != nil {
		c.tool.close()
	}
}

// closeTools ends every tool (quit).
func (a *App) closeTools() {
	a.mu.Lock()
	tabs := append([]*conn(nil), a.tabs...)
	a.mu.Unlock()
	for _, t := range tabs {
		t.closeTool()
	}
}

// openSQL is `:sql [site|host] [--sys] [--tls|--plain]`: a new tab with the SQL tool for
// the site (the active tab's when none is named).
func (a *App) openSQL(args []string) string {
	naming, target := "sql", ""
	tlsFlag, tlsSet, plain := false, false, false
	for _, arg := range args {
		switch strings.ToLower(arg) {
		case "--sys":
			naming = "sys"
		case "--sql":
			naming = "sql"
		case "--tls":
			tlsFlag, tlsSet = true, true
		case "--plain", "--no-tls":
			plain = true
		default:
			if strings.HasPrefix(arg, "-") || target != "" {
				return "usage: sql [site|host] [--sys] [--tls|--plain]"
			}
			target = arg
		}
	}
	var site sites.Site
	var name string
	if target != "" {
		s, saved, err := a.resolveTarget(target, tlsFlag)
		if err != nil {
			return err.Error()
		}
		site, name = s, saved
	} else {
		a.mu.Lock()
		site, name = a.cur, a.curSaved
		a.mu.Unlock()
		if site.Host == "" {
			return "no connection in this tab; name a site: sql <site>"
		}
	}
	if name == "" {
		name = site.Host
	}
	if tlsSet {
		site.TLS = true
	}
	if plain {
		site.TLS = false
	}
	if site.User == "" || site.Password == "" {
		return "the site needs a user and a password for the host servers (edit it in :sites)"
	}
	t := a.newTab()
	tool := newSQLTool(a, t, site, name, naming)
	a.mu.Lock()
	t.tool = tool
	t.cur, t.curSaved = site, name
	a.mu.Unlock()
	a.syncScreens()
	return ""
}
