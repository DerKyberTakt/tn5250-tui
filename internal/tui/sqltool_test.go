package tui

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/DerKyberTakt/tn5250-tui/internal/hostserver/db/dbtest"
	"github.com/DerKyberTakt/tn5250-tui/internal/hostserver/rmtcmd/rmtcmdtest"
	"github.com/DerKyberTakt/tn5250-tui/internal/runscript"
	"github.com/DerKyberTakt/tn5250-tui/internal/sites"
)

// fakeHostServers starts a database and a remote command server and points the SQL tool at them.
func fakeHostServers(t *testing.T) {
	t.Helper()
	d, err := dbtest.New("127.0.0.1:0", "TESTUSER", "SECRET1", 2, func(sql string) any {
		s := strings.ToUpper(strings.TrimSpace(sql))
		switch {
		case strings.HasPrefix(s, "SELECT N FROM"):
			tb := dbtest.Table{Cols: []dbtest.Col{{Name: "N", Type: 496, Length: 4, CCSID: 65535}, {Name: "NAME", Type: 449, Length: 12, CCSID: 37}}}
			for i := int64(1); i <= 150; i++ {
				var name any = "row"
				if i == 2 {
					name = nil
				}
				tb.Rows = append(tb.Rows, []any{i, name})
			}
			return tb
		case strings.HasPrefix(s, "SELECT LINE FROM TABLE(QSYS2.IFS_READ"):
			a := "qsh says hi"
			return dbtest.Table{Cols: []dbtest.Col{{Name: "LINE", Type: 449, Length: 42, CCSID: 37}}, Rows: [][]any{{a}}}
		case strings.HasPrefix(s, "SELECT SPOOLED_FILE_NAME"):
			return dbtest.Table{Cols: []dbtest.Col{{Name: "SPOOLED_FILE_NAME", Type: 449, Length: 12, CCSID: 37}, {Name: "FILE_NUMBER", Type: 449, Length: 8, CCSID: 37}}}
		case strings.HasPrefix(s, "INSERT"):
			return dbtest.Exec{Count: 2}
		}
		return dbtest.Fail{Class: 7, Code: -104, ID: "SQL0104", Text: "Token not valid.", SQLCode: -104, SQLState: "42601"}
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	c, err := rmtcmdtest.New("127.0.0.1:0", "TESTUSER", "SECRET1", 2, func(command string) (uint16, []rmtcmdtest.Message) {
		switch {
		case strings.HasPrefix(command, "QSH CMD("), strings.HasPrefix(command, "RMVLNK"), strings.HasPrefix(command, "DSPLIBL"):
			return 0, nil
		case strings.HasPrefix(command, "SNDMSG"):
			return 0, []rmtcmdtest.Message{{ID: "CPC2301", Type: 1, Text: "Message sent."}}
		}
		return 0x0400, []rmtcmdtest.Message{{ID: "CPF0006", Type: 15, Severity: 30, Text: "Errors occurred in command."}}
	})
	if err != nil {
		t.Fatal(err)
	}
	c.JobName = "QZRCSRVS  TESTUSER  123456"
	t.Cleanup(func() { _ = c.Close() })
	old := runscriptOptions
	runscriptOptions = func(s sites.Site, naming string) runscript.Options {
		return runscript.Options{Host: "127.0.0.1", DatabasePort: d.Port(), CommandPort: c.Port(), User: s.User, Password: s.Password, Naming: naming}
	}
	t.Cleanup(func() { runscriptOptions = old })
}

func (t *sqlTool) outText() string { return t.out.GetText(true) }

func TestSQLToolTab(t *testing.T) {
	fakeHostServers(t)
	r := newRig(t, nil)
	r.a.opts.Sites.Upsert(sites.Site{Name: "box", Host: "127.0.0.1", User: "testuser", Password: "SECRET1"})
	r.a.opts.Sites.Upsert(sites.Site{Name: "nopw", Host: "127.0.0.1", User: "testuser"})
	if msg := r.a.execute("sql"); !strings.Contains(msg, "no connection") {
		t.Fatalf("sql without a target: %q", msg)
	}
	if msg := r.a.execute("sql nopw"); !strings.Contains(msg, "needs a user and a password") {
		t.Fatalf("sql without a password: %q", msg)
	}
	if msg := r.a.execute("sql box --sys --bogus"); !strings.HasPrefix(msg, "usage:") {
		t.Fatalf("%q", msg)
	}
	r.connect(t, "pub400.com")
	if msg := r.a.execute("sql box"); msg != "" {
		t.Fatalf("sql box: %q", msg)
	}
	if r.a.TabCount() != 2 || r.a.ActiveTab() != 1 || r.a.tool == nil {
		t.Fatalf("tabs=%d active=%d tool=%v", r.a.TabCount(), r.a.ActiveTab(), r.a.tool)
	}
	infos := r.a.tabInfos()
	if infos[1].Title != "SQL box" || !infos[1].Connected {
		t.Fatalf("tab info %+v", infos[1])
	}
	tool := r.a.tool
	if r.a.focusedPrimitive() != tool.editor {
		t.Fatal("the editor should have the focus")
	}
	// 5250 keys are not the App's business in a tool tab; the global ones still are.
	if ev := r.a.handleKey(key(tcell.KeyF1, 0, tcell.ModNone)); ev == nil {
		t.Fatal("F1 was swallowed by the App in a tool tab")
	}
	if ev := r.a.handleKey(key(tcell.KeyPgUp, 0, tcell.ModCtrl)); ev != nil || r.a.ActiveTab() != 0 {
		t.Fatalf("Ctrl+PgUp in a tool tab: ev=%v active=%d", ev, r.a.ActiveTab())
	}
	r.a.RunCommand("tab 2")
	if r.a.tool != tool {
		t.Fatal("tab 2 is the tool")
	}
	// a connect while the tool tab is in front opens a new tab rather than dialling into it
	r.a.RunCommand("connect pub400.com:2323")
	waitFor(t, "third tab", func() bool { return r.a.TabCount() == 3 && r.isConnected() })
	if r.a.tabs[1].tool != tool || r.a.tabs[1].engine != nil {
		t.Fatal("the connect reused the tool tab")
	}
	r.a.RunCommand("tab 2")

	// F5 on a query: rows on the grid, a NULL as "-", more to fetch.
	tool.editor.SetText("select n from t\n", false)
	tool.handleKey(key(tcell.KeyF5, 0, tcell.ModNone))
	waitFor(t, "rows", func() bool { return tool.table.GetRowCount() == 1+sqlBlock && !tool.isRunning() })
	if got := tool.table.GetCell(0, 0).Text; got != " N " {
		t.Fatalf("header %q", got)
	}
	if got := tool.table.GetCell(2, 1).Text; got != " - " {
		t.Fatalf("null cell %q", got)
	}
	if out := tool.outText(); !strings.Contains(out, "SQL (SQL keyword SELECT): 100 rows, PgDn") {
		t.Fatalf("status %q", out)
	}
	// Tab moves to the grid; PgDn on the last row fetches the rest.
	tool.handleKey(key(tcell.KeyTab, 0, tcell.ModNone))
	if r.a.focusedPrimitive() != tool.table {
		t.Fatal("Tab should move to the grid")
	}
	tool.table.Select(tool.table.GetRowCount()-1, 0)
	tool.handleKey(key(tcell.KeyPgDn, 0, tcell.ModNone))
	waitFor(t, "more rows", func() bool { return tool.table.GetRowCount() == 1+150 })
	if out := tool.outText(); !strings.Contains(out, "150 rows so far") || strings.Contains(out, "PgDn") {
		t.Fatalf("status after fetch %q", out)
	}
	tool.handleKey(key(tcell.KeyEsc, 0, tcell.ModNone))
	if r.a.focusedPrimitive() != tool.editor {
		t.Fatal("Esc should go back to the editor")
	}

	// F9 runs a mixed script: an insert, a CL command, a Qshell line (last result on the grid).
	tool.editor.SetText("insert into t values (1);\nSNDMSG MSG('x') TOUSR(QSYSOPR)\nls /tmp\n", false)
	tool.handleKey(key(tcell.KeyF9, 0, tcell.ModNone))
	waitFor(t, "script done", func() bool { return !tool.isRunning() && strings.Contains(tool.outText(), "statement 3 of 3") })
	out := tool.outText()
	for _, want := range []string{"2 rows affected", "CPC2301", "QSH (shell command ls): 1 rows", "statement 3 of 3"} {
		if !strings.Contains(out, want) {
			t.Errorf("status lacks %q:\n%s", want, out)
		}
	}
	if got := tool.table.GetCell(1, 0).Text; got != " qsh says hi " {
		t.Fatalf("qsh output %q", got)
	}
	// A failure stops the script and says why.
	tool.editor.SetText("BADCMD\nselect n from t\n", false)
	tool.handleKey(key(tcell.KeyF9, 0, tcell.ModNone))
	waitFor(t, "failure", func() bool { return !tool.isRunning() && strings.Contains(tool.outText(), "CPF0006") })
	if out := tool.outText(); !strings.Contains(out, "CL (CL verb BADCMD) failed") || strings.Contains(out, "statement 2 of 2") {
		t.Fatalf("failure status %q", out)
	}
	// F3 closes the tool tab and its connections.
	tool.handleKey(key(tcell.KeyF3, 0, tcell.ModNone))
	if r.a.TabCount() != 2 || r.a.tool != nil {
		t.Fatalf("after F3: tabs=%d tool=%v", r.a.TabCount(), r.a.tool)
	}
	tool.mu.Lock()
	closed := tool.closed
	tool.mu.Unlock()
	if !closed {
		t.Fatal("the tool was not closed")
	}
}
