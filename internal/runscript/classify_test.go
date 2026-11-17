package runscript

import "testing"

func TestClassify(t *testing.T) {
	cases := []struct {
		in   string
		kind Kind
		text string
	}{
		{"select * from qiws.qcustcdt", SQL, "select * from qiws.qcustcdt"},
		{"  SELECT 1 FROM sysibm.sysdummy1;", SQL, "SELECT 1 FROM sysibm.sysdummy1;"},
		{"-- a comment\nselect 1", SQL, "-- a comment\nselect 1"},
		{"/* block */ WITH t AS (SELECT 1) SELECT * FROM t", SQL, "/* block */ WITH t AS (SELECT 1) SELECT * FROM t"},
		{"(select 1) union (select 2)", SQL, "(select 1) union (select 2)"},
		{"VALUES CURRENT TIMESTAMP", SQL, "VALUES CURRENT TIMESTAMP"},
		{"insert into x values (1)", SQL, "insert into x values (1)"},
		{"CREATE TABLE QTEMP.T (A INT)", SQL, "CREATE TABLE QTEMP.T (A INT)"},
		{"SET CURRENT SCHEMA = QGPL", SQL, "SET CURRENT SCHEMA = QGPL"},
		{"CALL QSYS2.QCMDEXC('DSPLIBL')", SQL, "CALL QSYS2.QCMDEXC('DSPLIBL')"},
		{"CALL MYLIB.MYPROC(1, 'x')", SQL, "CALL MYLIB.MYPROC(1, 'x')"},
		{"CALL PGM(QGPL/HELLO)", CL, "CALL PGM(QGPL/HELLO)"},
		{"CALL HELLO", CL, "CALL HELLO"},
		{"CALL QGPL/HELLO PARM('a' 'b')", CL, "CALL QGPL/HELLO PARM('a' 'b')"},
		{"WRKACTJOB", CL, "WRKACTJOB"},
		{"wrkactjob sbs(qinter)", CL, "wrkactjob sbs(qinter)"},
		{"DSPLIBL OUTPUT(*PRINT)", CL, "DSPLIBL OUTPUT(*PRINT)"},
		{"CHGCURLIB CURLIB(QGPL)", CL, "CHGCURLIB CURLIB(QGPL)"},
		{"SNDMSG MSG('hi there') TOUSR(*SYSOPR)", CL, "SNDMSG MSG('hi there') TOUSR(*SYSOPR)"},
		{"ADDLIBLE QGPL", CL, "ADDLIBLE QGPL"},
		{"MYLIB/MYCMD PARM1(X)", CL, "MYLIB/MYCMD PARM1(X)"},
		{"DLYJOB 5", CL, "DLYJOB 5"},
		{"QSH CMD('ls')", CL, "QSH CMD('ls')"},
		{"ls -l /home", QSH, "ls -l /home"},
		{"/QOpenSys/pkgs/bin/python3 --version", PASE, "/QOpenSys/pkgs/bin/python3 --version"},
		{"./run.sh", QSH, "./run.sh"},
		{"PATH=/QOpenSys/pkgs/bin:$PATH", QSH, "PATH=/QOpenSys/pkgs/bin:$PATH"},
		{"db2 \"select 1 from sysibm.sysdummy1\"", QSH, "db2 \"select 1 from sysibm.sysdummy1\""},
		{"cat /etc/hosts | grep local", QSH, "cat /etc/hosts | grep local"},
		{"system \"WRKACTJOB\"", QSH, "system \"WRKACTJOB\""},
		{"CL: SELECTX", CL, "SELECTX"},
		{"cl:dspjob", CL, "dspjob"},
		{"QSH: WRKACTJOB", QSH, "WRKACTJOB"},
		{"sh: echo hi", QSH, "echo hi"},
		{"SQL: ls", SQL, "ls"},
		{"", SQL, ""},
		{"-- only a comment", SQL, "-- only a comment"},
		{"FOO BAR", CL, "FOO BAR"},
		{"somethingelse x = 1", SQL, "somethingelse x = 1"},
		{"EXPLAIN SELECT 1", SQL, "EXPLAIN SELECT 1"},
		{"RUNSQL SQL('select 1') COMMIT(*NONE)", CL, "RUNSQL SQL('select 1') COMMIT(*NONE)"},
		{"STRSQL", CL, "STRSQL"},
		{"select a||b from t", SQL, "select a||b from t"},
	}
	for _, c := range cases {
		got := Classify(c.in)
		if got.Kind != c.kind || got.Text != c.text {
			t.Errorf("Classify(%q) = %s %q (%s), want %s %q", c.in, got.Kind, got.Text, got.Why, c.kind, c.text)
		}
	}
}

func TestVerb(t *testing.T) {
	for in, want := range map[string]string{"select 1": "SELECT", "  WRKACTJOB SBS(X)": "WRKACTJOB", "-- c\ninsert into": "INSERT", "CALL PGM(X)": "CALL", "": ""} {
		if got := Verb(in); got != want {
			t.Errorf("Verb(%q) = %q", in, got)
		}
	}
	if (Kind(9)).String() != "SQL" || CL.String() != "CL" || QSH.String() != "QSH" {
		t.Fatal("Kind.String")
	}
}

func TestClassifyPASE(t *testing.T) {
	cases := map[string]Kind{
		"PASE: bash -c \"echo hi\"":            PASE,
		"/QOpenSys/pkgs/bin/python3 --version": PASE,
		"git -C /home/jsmith/app status":       PASE,
		"yum list installed":                   PASE,
		"ls -l /home":                          QSH,
		"openssl version":                      QSH,
		"QSH: git status":                      QSH,
		"/usr/bin/ls":                          QSH,
	}
	for in, want := range cases {
		if got := Classify(in); got.Kind != want {
			t.Errorf("%q: got %s (%s), want %s", in, got.Kind, got.Why, want)
		}
	}
	if got := Classify("PASE: bash -c x"); got.Text != "bash -c x" || got.Why != "prefix PASE:" {
		t.Errorf("prefix: %+v", got)
	}
	cmd := paseCommand("git status", "/tmp/t.txt")
	want := "CALL PGM(QP2SHELL) PARM('/QOpenSys/usr/bin/sh' '-c' 'PATH=/QOpenSys/pkgs/bin:/QOpenSys/usr/bin:/usr/bin:$PATH; export PATH; { git status ; } > /tmp/t.txt 2>&1; echo \"tn5250-exit=$?\" >> /tmp/t.txt')"
	if cmd != want {
		t.Errorf("got  %s\nwant %s", cmd, want)
	}
}
