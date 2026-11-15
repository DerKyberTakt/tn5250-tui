package runscript

import "testing"

func TestSplitMixedScript(t *testing.T) {
	script := "-- customers\nselect *\n  from qiws.qcustcdt\n where cusnum > 1;\n\nWRKACTJOB SBS(QINTER)\nCHGCURLIB +\n   CURLIB(QGPL)\nls -l /tmp \\\n  /home\ninsert into t values (';', 1); -- done\nvalues 'a--b' /* ; */\n\nselect 2;\nDSPLIBL\n"
	got := Split(script)
	want := []struct {
		kind       Kind
		text       string
		start, end int
	}{
		{SQL, "-- customers\nselect *\n  from qiws.qcustcdt\n where cusnum > 1", 0, 3},
		{CL, "WRKACTJOB SBS(QINTER)", 5, 5},
		{CL, "CHGCURLIB CURLIB(QGPL)", 6, 7},
		{QSH, "ls -l /tmp /home", 8, 9},
		{SQL, "insert into t values (';', 1); -- done", 10, 10},
		{SQL, "values 'a--b' /* ; */", 11, 11},
		{SQL, "select 2", 13, 13},
		{CL, "DSPLIBL", 14, 14},
	}
	if len(got) != len(want) {
		for _, s := range got {
			t.Logf("%s %d-%d %q", s.Kind, s.Line, s.EndLine, s.Text)
		}
		t.Fatalf("%d statements, want %d", len(got), len(want))
	}
	for i, w := range want {
		g := got[i]
		if g.Kind != w.kind || g.Text != w.text || g.Line != w.start || g.EndLine != w.end {
			t.Errorf("statement %d: %s %d-%d %q, want %s %d-%d %q", i, g.Kind, g.Line, g.EndLine, g.Text, w.kind, w.start, w.end, w.text)
		}
	}
	// The insert keeps its trailing comment but loses the terminator; the one with the
	// quoted semicolon was not cut there.
	if got[4].Text[len(got[4].Text)-1] == ';' {
		t.Fatal("terminator kept")
	}
	if s, ok := At(got, 2); !ok || s.Line != 0 {
		t.Fatalf("At(2) = %+v %v", s, ok)
	}
	if s, ok := At(got, 4); !ok || s.Line != 0 { // the blank line after a statement belongs to the one above
		t.Fatalf("At(4) = %+v %v", s, ok)
	}
	if s, ok := At(got, 7); !ok || s.Kind != CL || s.Line != 6 {
		t.Fatalf("At(7) = %+v %v", s, ok)
	}
	if _, ok := At(nil, 0); ok {
		t.Fatal("At on nothing")
	}
}

func TestSplitEdges(t *testing.T) {
	if got := Split(""); len(got) != 0 {
		t.Fatalf("empty: %+v", got)
	}
	if got := Split("\n\n-- just a comment\n\n"); len(got) != 0 {
		t.Fatalf("comment only: %+v", got)
	}
	got := Split("select 1\r\nfrom x\r\n")
	if len(got) != 1 || got[0].Text != "select 1\nfrom x" || got[0].EndLine != 1 {
		t.Fatalf("crlf: %+v", got)
	}
	got = Split("select 1; select 2;")
	if len(got) != 1 { // one line = one statement; the host gets both (and says so)
		t.Fatalf("two on a line: %+v", got)
	}
	got = Split("CL: select-looking\nQSH: WRKACTJOB")
	if len(got) != 2 || got[0].Kind != CL || got[0].Text != "select-looking" || got[1].Kind != QSH || got[1].Text != "WRKACTJOB" {
		t.Fatalf("prefixes: %+v", got)
	}
	for in, want := range map[string]bool{"select 1;": true, "select ';'": false, "select 1 -- ;": false, "select 1; -- end": true, "a /* ; */": false, "x;\n": true, "it''s;": true} {
		if got := endsStatement(in); got != want {
			t.Errorf("endsStatement(%q) = %v", in, got)
		}
	}
}
