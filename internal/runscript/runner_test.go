package runscript

import (
	"strings"
	"testing"

	"github.com/DerKyberTakt/tn5250-tui/internal/hostserver/db/dbtest"
	"github.com/DerKyberTakt/tn5250-tui/internal/hostserver/rmtcmd/rmtcmdtest"
)

func dbHandler(sql string) any {
	s := strings.ToUpper(strings.TrimSpace(sql))
	switch {
	case strings.HasPrefix(s, "SELECT N FROM"):
		t := dbtest.Table{Cols: []dbtest.Col{{Name: "N", Type: 496, Length: 4, CCSID: 65535}}}
		for i := int64(1); i <= 7; i++ {
			t.Rows = append(t.Rows, []any{i})
		}
		return t
	case strings.HasPrefix(s, "SELECT LINE FROM TABLE(QSYS2.IFS_READ"):
		a, b := "hello from qsh", "second line"
		return dbtest.Table{Cols: []dbtest.Col{{Name: "LINE", Type: 449, Length: 42, CCSID: 37}}, Rows: [][]any{{a}, {b}}}
	case strings.HasPrefix(s, "SELECT SPOOLED_FILE_NAME, FILE_NUMBER FROM TABLE(QSYS2.SPOOLED_FILE_INFO"):
		n, f := "QPDSPLIB", "1"
		return dbtest.Table{Cols: []dbtest.Col{{Name: "SPOOLED_FILE_NAME", Type: 449, Length: 12, CCSID: 37}, {Name: "FILE_NUMBER", Type: 449, Length: 8, CCSID: 37}}, Rows: [][]any{{n, f}}}
	case strings.HasPrefix(s, "SELECT SPOOLED_DATA FROM TABLE(SYSTOOLS.SPOOLED_FILE_DATA"):
		a, b := " Library  Type", " QGPL     PROD"
		return dbtest.Table{Cols: []dbtest.Col{{Name: "SPOOLED_DATA", Type: 449, Length: 42, CCSID: 37}}, Rows: [][]any{{a}, {b}}}
	case strings.HasPrefix(s, "INSERT"):
		return dbtest.Exec{Count: 3}
	case strings.HasPrefix(s, "CREATE"):
		return dbtest.Exec{}
	case strings.HasPrefix(s, "SELECT * FROM NOWHERE"):
		return dbtest.Fail{Class: 7, Code: -204, ID: "SQL0204", Text: "NOWHERE in QGPL type *FILE not found.", SQLCode: -204, SQLState: "42704"}
	}
	return dbtest.Fail{Class: 7, Code: -104, ID: "SQL0104", Text: "Token not valid.", SQLCode: -104, SQLState: "42601"}
}

func cmdHandler(command string) (uint16, []rmtcmdtest.Message) {
	switch {
	case strings.HasPrefix(command, "DSPLIBL"):
		return 0, nil
	case strings.HasPrefix(command, "QSH CMD("):
		return 0, []rmtcmdtest.Message{{ID: "QSH0005", Type: 1, Text: "Command ended normally with exit status 0."}}
	case strings.HasPrefix(command, "DLTSPLF"), strings.HasPrefix(command, "RMVLNK"):
		return 0, nil
	case strings.HasPrefix(command, "SNDMSG"):
		return 0, []rmtcmdtest.Message{{ID: "CPC2301", Type: 1, Text: "Message sent to user QSYSOPR."}}
	}
	return 0x0400, []rmtcmdtest.Message{{ID: "CPF0006", Type: 15, Severity: 30, Text: "Errors occurred in command."}}
}

func newSession(t *testing.T) (*Session, *rmtcmdtest.Server) {
	t.Helper()
	d, err := dbtest.New("127.0.0.1:0", "TESTUSER", "SECRET1", 2, dbHandler)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	c, err := rmtcmdtest.New("127.0.0.1:0", "TESTUSER", "SECRET1", 2, cmdHandler)
	if err != nil {
		t.Fatal(err)
	}
	c.JobName = "QZRCSRVS  TESTUSER  123456"
	t.Cleanup(func() { _ = c.Close() })
	s := New(Options{Host: "127.0.0.1", DatabasePort: d.Port(), CommandPort: c.Port(), User: "testuser", Password: "SECRET1"})
	t.Cleanup(s.Close)
	return s, c
}

func cell(r Result, row, col int) string {
	if row >= len(r.Rows) || col >= len(r.Rows[row]) || r.Rows[row][col] == nil {
		return "<nil>"
	}
	return *r.Rows[row][col]
}

func TestRunSQLQueryAndFetch(t *testing.T) {
	s, _ := newSession(t)
	r, err := s.Run(Classify("select n from t"), 3)
	if err != nil {
		t.Fatal(err)
	}
	if r.Kind != "rows" || len(r.Rows) != 3 || !r.More || r.Columns[0] != "N" || r.Types[0] != "INTEGER" || cell(r, 2, 0) != "3" {
		t.Fatalf("first block %+v", r)
	}
	r2, err := s.Fetch(3)
	if err != nil || r2.Kind != "rows" || len(r2.Rows) != 3 || cell(r2, 0, 0) != "4" {
		t.Fatalf("second block %+v %v", r2, err)
	}
	r3, err := s.Fetch(3)
	if err != nil || len(r3.Rows) != 1 || r3.More {
		t.Fatalf("last block %+v %v", r3, err)
	}
	if _, err := s.Fetch(3); err == nil {
		t.Fatal("fetch after the end")
	}
	if r.Statement.Kind != SQL || r.Elapsed <= 0 {
		t.Fatalf("statement %+v elapsed %v", r.Statement, r.Elapsed)
	}
}

func TestRunSQLExecAndErrors(t *testing.T) {
	s, _ := newSession(t)
	r, err := s.Run(Classify("insert into t values (1)"), 100)
	if err != nil || r.Kind != "count" || r.UpdateCount != 3 {
		t.Fatalf("insert %+v %v", r, err)
	}
	r, err = s.Run(Classify("create table qtemp.x (a int)"), 100)
	if err != nil || r.Kind != "none" {
		t.Fatalf("create %+v %v", r, err)
	}
	r, err = s.Run(Classify("select * from nowhere"), 100)
	if err != nil || r.Kind != "error" || len(r.Messages) != 1 || !strings.Contains(r.Messages[0], "SQL0204") {
		t.Fatalf("refused %+v %v", r, err)
	}
	if _, err := s.Fetch(10); err == nil {
		t.Fatal("no query open after an error")
	}
}

func TestRunCLWithSpooledOutput(t *testing.T) {
	s, c := newSession(t)
	r, err := s.Run(Classify("DSPLIBL OUTPUT(*PRINT)"), 100)
	if err != nil {
		t.Fatal(err)
	}
	if r.Kind != "rows" || len(r.Rows) != 2 || cell(r, 1, 0) != " QGPL     PROD" || r.Columns[0] != "OUTPUT" {
		t.Fatalf("spool %+v", r)
	}
	if len(r.Messages) != 1 || !strings.Contains(r.Messages[0], "spooled file QPDSPLIB, 2 lines, shown here and deleted") {
		t.Fatalf("notes %v", r.Messages)
	}
	ran := c.Ran()
	if len(ran) != 2 || !strings.HasPrefix(ran[1], "DLTSPLF FILE(QPDSPLIB) JOB(123456/TESTUSER/QZRCSRVS) SPLNBR(1)") {
		t.Fatalf("commands run: %q", ran)
	}
	r, err = s.Run(Classify("SNDMSG MSG('x') TOUSR(QSYSOPR)"), 100)
	if err != nil || r.Kind != "rows" || !strings.Contains(r.Messages[0], "CPC2301") {
		t.Fatalf("message %+v %v", r, err)
	}
	r, err = s.Run(Classify("BADCMD"), 100)
	if err != nil || r.Kind != "error" || !strings.Contains(r.Messages[0], "CPF0006") {
		t.Fatalf("failed command %+v %v", r, err)
	}
	if s.Job() == "" {
		t.Fatal("job name")
	}
}

func TestRunQSH(t *testing.T) {
	s, c := newSession(t)
	r, err := s.Run(Classify("ls -l /tmp"), 100)
	if err != nil {
		t.Fatal(err)
	}
	if r.Kind != "rows" || len(r.Rows) != 2 || cell(r, 0, 0) != "hello from qsh" || r.Messages[0] != "Command ended normally with exit status 0." {
		t.Fatalf("qsh %+v", r)
	}
	ran := c.Ran()
	if len(ran) != 2 || !strings.HasPrefix(ran[0], "QSH CMD('ls -l /tmp > /tmp/tn5250-tui-") || !strings.HasSuffix(ran[0], ".txt 2>&1')") || !strings.HasPrefix(ran[1], "RMVLNK OBJLNK('/tmp/tn5250-tui-") {
		t.Fatalf("commands run: %q", ran)
	}
}

func TestConnectionFailureIsAnError(t *testing.T) {
	s := New(Options{Host: "127.0.0.1", DatabasePort: 1, CommandPort: 1, User: "u", Password: "p"})
	defer s.Close()
	if _, err := s.Run(Classify("select 1"), 10); err == nil || !strings.Contains(err.Error(), "database server") {
		t.Fatalf("db: %v", err)
	}
	if _, err := s.Run(Classify("DSPLIBL"), 10); err == nil || !strings.Contains(err.Error(), "remote command server") {
		t.Fatalf("cmd: %v", err)
	}
	if err := s.Cancel(); err == nil {
		t.Fatal("cancel without a connection")
	}
}
