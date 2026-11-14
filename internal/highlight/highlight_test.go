package highlight

import (
	"fmt"
	"strings"
	"testing"
)

// kindOf is the token kind of the first character of needle in line text
// (looked up among lines, default just text itself).
func kindOf(t *testing.T, lang Lang, text, needle string, lines ...string) Tok {
	t.Helper()
	if len(lines) == 0 {
		lines = []string{text}
	}
	idx := -1
	for i, l := range lines {
		if l == text {
			idx = i
			break
		}
	}
	if idx < 0 {
		t.Fatalf("line %q not in lines", text)
	}
	kinds := Lines(lang, lines, 1)[idx]
	at := strings.Index(text, needle)
	if at < 0 {
		t.Fatalf("%q not in %q", needle, text)
	}
	return kinds[at]
}

type kcase struct {
	lang   Lang
	text   string
	needle string
	lines  []string
	want   Tok
}

func runKinds(t *testing.T, cases []kcase) {
	t.Helper()
	for _, c := range cases {
		t.Run(fmt.Sprintf("%s/%s", c.lang, c.needle), func(t *testing.T) {
			if got := kindOf(t, c.lang, c.text, c.needle, c.lines...); got != c.want {
				t.Errorf("kind of %q in %q = %d, want %d", c.needle, c.text, got, c.want)
			}
		})
	}
}

func col(n int, s string) string { return strings.Repeat(" ", n-1) + s }

func TestRPGFixedFormat(t *testing.T) {
	c := "     C     *ENTRY        PLIST"
	eval1 := "     C                   EVAL      TOTAL = %TRIM(NAME) + 'X'"
	ind := "     C   N50CUSTNO        CHAIN     CUSTMAST                           90"
	d := "     D CUSTREC         DS                  QUALIFIED"
	f := "     FCUSTMAST  IF   E           K DISK    USROPN"
	sql := []string{"     C/EXEC SQL", "     C+ SELECT NAME INTO :WNAME FROM CUSTMAST", "     C/END-EXEC"}
	runKinds(t, []kcase{
		// colours spec, factor 1 specials, opcodes and indicators
		{LangRPG, c, "C", nil, TokSpec},
		{LangRPG, c, "*ENTRY", nil, TokSpecial},
		{LangRPG, c, "PLIST", nil, TokKeyword},
		{LangRPG, ind, "N50", nil, TokIndicator},
		{LangRPG, ind, "CHAIN", nil, TokKeyword},
		{LangRPG, ind, "90", nil, TokIndicator},
		// treats extended factor 2 as an expression
		{LangRPG, eval1, "EVAL", nil, TokKeyword},
		{LangRPG, eval1, "%TRIM", nil, TokBuiltin},
		{LangRPG, eval1, "'X'", nil, TokString},
		{LangRPG, eval1, "TOTAL", nil, TokNone},
		// colours comments, markers and directives
		{LangRPG, "     C* a comment", "a comment", nil, TokComment},
		{LangRPG, "KW1  C                   EVAL      X = 1", "KW1", nil, TokMarker},
		{LangRPG, "      /COPY QRPGLESRC,MYCOPY", "/COPY", nil, TokKeyword},
		// colours D and F specs by column
		{LangRPG, d, "CUSTREC", nil, TokName},
		{LangRPG, d, "DS ", nil, TokKeyword},
		{LangRPG, d, "QUALIFIED", nil, TokKeyword},
		{LangRPG, f, "CUSTMAST", nil, TokName},
		{LangRPG, f, "DISK", nil, TokKeyword},
		{LangRPG, f, "USROPN", nil, TokKeyword},
		// colours embedded SQL between C/EXEC SQL and C/END-EXEC
		{LangRPG, sql[0], "EXEC", sql, TokKeyword},
		{LangRPG, sql[1], "SELECT", sql, TokKeyword},
		{LangRPG, sql[1], ":WNAME", sql, TokVariable},
		{LangRPG, sql[2], "END-EXEC", sql, TokKeyword},
	})
}

func TestRPGFreeFormat(t *testing.T) {
	col8 := "       dcl-s total packed(9:2) inz(0); // running total"
	fully := []string{"**FREE", "if %found(custmast) and name <> *blanks;", "  exec sql update custmast set name = 'A' where id = :id;", "endif;"}
	multi := []string{"       exec sql", "         select count(*) into :n", "           from custmast;", "       n += 1;"}
	runKinds(t, []kcase{
		// column-limited free form (columns 8-80)
		{LangRPG, col8, "dcl-s", nil, TokKeyword},
		{LangRPG, col8, "packed", nil, TokKeyword},
		{LangRPG, col8, "9", nil, TokNumber},
		{LangRPG, col8, "// running", nil, TokComment},
		// fully free (**FREE) source from column 1
		{LangRPG, fully[0], "**FREE", fully, TokKeyword},
		{LangRPG, fully[1], "if", fully, TokKeyword},
		{LangRPG, fully[1], "%found", fully, TokBuiltin},
		{LangRPG, fully[1], "*blanks", fully, TokSpecial},
		{LangRPG, fully[2], "update", fully, TokKeyword},
		{LangRPG, fully[2], ":id", fully, TokVariable},
		{LangRPG, fully[3], "endif", fully, TokKeyword},
		// embedded SQL across lines until the semicolon
		{LangRPG, multi[1], "select", multi, TokKeyword},
		{LangRPG, multi[1], "count", multi, TokBuiltin},
		{LangRPG, multi[2], "from", multi, TokKeyword},
		{LangRPG, multi[3], "1", multi, TokNumber},
	})
}

func TestCL(t *testing.T) {
	loop := "LOOP:       CHGVAR     VAR(&COUNT) VALUE(&COUNT + 1)"
	ifl := "            IF         COND(&X *EQ 'Y') THEN(GOTO CMDLBL(END))"
	mon := "            MONMSG     MSGID(CPF0000) EXEC(GOTO CMDLBL(ERROR))"
	cont := []string{"/* start of a", "   long comment */ PGM", "            SNDPGMMSG  MSG('one') +", "                         MSGTYPE(*INFO)"}
	str := "            CHGVAR     VAR(&P) VALUE('/*ALL')"
	runKinds(t, []kcase{
		// labels, commands, parameters, variables and specials
		{LangCL, loop, "LOOP", nil, TokName},
		{LangCL, loop, "CHGVAR", nil, TokKeyword},
		{LangCL, loop, "VAR(", nil, TokName},
		{LangCL, loop, "&COUNT", nil, TokVariable},
		{LangCL, loop, "1)", nil, TokNumber},
		{LangCL, ifl, "*EQ", nil, TokSpecial},
		{LangCL, ifl, "'Y'", nil, TokString},
		{LangCL, ifl, "THEN", nil, TokKeyword},
		{LangCL, ifl, "GOTO", nil, TokKeyword},
		{LangCL, mon, "MONMSG", nil, TokKeyword},
		{LangCL, mon, "GOTO", nil, TokKeyword},
		// comments across lines and continuations
		{LangCL, cont[1], "long", cont, TokComment},
		{LangCL, cont[1], "PGM", cont, TokKeyword},
		// the continued line does not start a new command
		{LangCL, cont[3], "MSGTYPE", cont, TokName},
		{LangCL, cont[3], "*INFO", cont, TokSpecial},
		// /* inside a string is not a comment
		{LangCL, str, "'/*ALL'", nil, TokString},
	})
}

func TestDDS(t *testing.T) {
	r := col(6, "A          R SCREEN1                   SFL")
	k := col(6, "A N83                                  DSPATR(HI) TEXT('Customer')")
	f := col(6, "A            CUSNO          7S 0B  5 10")
	runKinds(t, []kcase{
		{LangDDS, r, "R ", nil, TokKeyword},
		{LangDDS, r, "SCREEN1", nil, TokName},
		{LangDDS, r, "SFL", nil, TokKeyword},
		{LangDDS, k, "N83", nil, TokIndicator},
		{LangDDS, k, "DSPATR", nil, TokKeyword},
		{LangDDS, k, "'Customer'", nil, TokString},
		{LangDDS, col(6, "A* a comment"), "a comment", nil, TokComment},
		{LangDDS, f, "7S", nil, TokNumber},
		{LangDDS, f, "S 0", nil, TokSpecial},
	})
}

func TestSQL(t *testing.T) {
	lines := []string{"SELECT TRIM(NAME), 42 FROM MYLIB.CUSTMAST -- customers", "WHERE STATUS = 'A' /* active", "   only */ ORDER BY NAME;"}
	runKinds(t, []kcase{
		{LangSQL, lines[0], "SELECT", lines, TokKeyword},
		{LangSQL, lines[0], "TRIM", lines, TokBuiltin},
		{LangSQL, lines[0], "42", lines, TokNumber},
		{LangSQL, lines[0], "-- customers", lines, TokComment},
		{LangSQL, lines[1], "'A'", lines, TokString},
		{LangSQL, lines[2], "only", lines, TokComment},
		{LangSQL, lines[2], "ORDER", lines, TokKeyword},
	})
}

func TestLanguageFromFile(t *testing.T) {
	for _, c := range []struct {
		in   string
		want Lang
		ok   bool
	}{
		{"QRPGLESRC", LangRPG, true},
		{"QSQLRPGLE", LangRPG, true},
		{"QCLLESRC", LangCL, true},
		{"QCLSRC", LangCL, true},
		{"QDDSSRC", LangDDS, true},
		{"QSQLSRC", LangSQL, true},
		{"MYSRC", "", false},
		{"", "", false},
		{"qrpglesrc", LangRPG, true},
	} {
		got, ok := LanguageFromFile(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("LanguageFromFile(%q) = %q,%v want %q,%v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestLanguageFromContent(t *testing.T) {
	for _, c := range []struct {
		lines []string
		want  Lang
		ok    bool
	}{
		{[]string{"             PGM", "             DCL        VAR(&A) TYPE(*CHAR)"}, LangCL, true},
		{[]string{col(6, "A          R REC1"), col(6, "A            FLD1          10A")}, LangDDS, true},
		{[]string{"     C                   EVAL      X = 1"}, LangRPG, true},
		{[]string{"select * from t"}, LangSQL, true},
		{[]string{"nothing to see"}, "", false},
		{[]string{"dcl-s x int(10);"}, LangRPG, true},
	} {
		got, ok := LanguageFromContent(c.lines, 1)
		if got != c.want || ok != c.ok {
			t.Errorf("LanguageFromContent(%q) = %q,%v want %q,%v", c.lines, got, ok, c.want, c.ok)
		}
	}
}

// seuScreen builds a synthetic 24x80 SEU edit screen: sequence-number field
// plus source field per data line.
func seuScreen(file string, source []string) (rowText func(int) string, fields []Field) {
	const rows, cols = 24, 80
	lines := make([]string, rows)
	for i := range lines {
		lines[i] = strings.Repeat(" ", cols)
	}
	put := func(r int, text string) {
		lines[r] = (text + strings.Repeat(" ", cols))[:cols]
	}
	put(0, fmt.Sprintf(" Columns . . . :    1  71           Edit                       MYLIB/%s", file))
	put(1, " SEU==>                                                              MYMBR")
	fields = append(fields, Field{Start: 1*cols + 8, Length: 60})
	put(2, " FMT **  ...+... 1 ...+... 2 ...+... 3 ...+... 4 ...+... 5 ...+... 6 ...+... 7")
	put(3, "        *************** Beginning of data *************************************")
	for i, s := range source {
		r := 4 + i
		put(r, fmt.Sprintf("%04d.00 %s", i+1, s))
		fields = append(fields, Field{Start: r * cols, Length: 7}, Field{Start: r*cols + 8, Length: 71})
	}
	put(4+len(source), "        ****************** End of data ****************************************")
	put(21, " F3=Exit   F4=Prompt   F5=Refresh   F9=Retrieve   F10=Cursor   F11=Toggle")
	return func(r int) string { return lines[r] }, fields
}

func TestSEUDetection(t *testing.T) {
	rowText, fields := seuScreen("QCLLESRC", []string{"             PGM", "             SNDPGMMSG  MSG('Hi')", "             ENDPGM"})
	for name, detect := range map[string]func() (SeuScreen, bool){
		"fields":    func() (SeuScreen, bool) { return DetectSeuFields(24, 80, rowText, fields) },
		"no fields": func() (SeuScreen, bool) { return DetectSeu(24, 80, rowText) },
	} {
		t.Run(name, func(t *testing.T) {
			seu, ok := detect()
			if !ok {
				t.Fatal("not detected")
			}
			if seu.Lang != LangCL || seu.FirstColumn != 1 || seu.SourceFile != "QCLLESRC" {
				t.Errorf("got lang=%q first=%d file=%q", seu.Lang, seu.FirstColumn, seu.SourceFile)
			}
			var rows []int
			for _, l := range seu.Lines {
				rows = append(rows, l.Row)
			}
			if fmt.Sprint(rows) != "[4 5 6]" {
				t.Errorf("rows = %v", rows)
			}
			if seu.Lines[0].Col != 8 {
				t.Errorf("col = %d", seu.Lines[0].Col)
			}
		})
	}
}

func TestSEUColoursCells(t *testing.T) {
	rowText, fields := seuScreen("QCLLESRC", []string{"             PGM"})
	for name, screen := range map[string]func() ([]Tok, bool){
		"fields":    func() ([]Tok, bool) { return ScreenFields(24, 80, rowText, fields) },
		"no fields": func() ([]Tok, bool) { return Screen(24, 80, rowText) },
	} {
		t.Run(name, func(t *testing.T) {
			cells, ok := screen()
			if !ok {
				t.Fatal("not SEU")
			}
			if len(cells) != 24*80 {
				t.Fatalf("len = %d", len(cells))
			}
			if got := cells[4*80+8+13]; got != TokKeyword {
				t.Errorf("PGM kind = %d, want keyword", got)
			}
			// Header and function-key rows are left alone.
			for i, k := range cells[:4*80] {
				if k != 0 {
					t.Fatalf("header cell %d = %d", i, k)
				}
			}
			for i, k := range cells[21*80 : 22*80] {
				if k != 0 {
					t.Fatalf("fkey cell %d = %d", i, k)
				}
			}
		})
	}
}

func TestSEUIgnoresNonSEU(t *testing.T) {
	rowText, fields := seuScreen("QCLLESRC", []string{"             PGM"})
	plain := func(r int) string {
		if r == 0 {
			return strings.Repeat(" ", 80)
		}
		return rowText(r)
	}
	if _, ok := DetectSeuFields(24, 80, plain, fields); ok {
		t.Error("blank header detected as SEU")
	}
	if _, ok := Screen(24, 80, plain); ok {
		t.Error("blank header coloured as SEU")
	}
	if _, ok := DetectSeu(9, 80, rowText); ok {
		t.Error("short screen detected as SEU")
	}
}

// ---------------------------------------------------------------------------
// Extra cases: JavaScript semantics the Go port had to reproduce by hand.

func TestJSSliceSemantics(t *testing.T) {
	s := []rune("abcdef")
	for _, c := range []struct {
		a, b int
		want string
	}{
		{0, 3, "abc"}, {-2, 6, "ef"}, {2, -1, "cde"}, {4, 2, ""}, {-100, 2, "ab"}, {3, 100, "def"}, {10, 12, ""},
	} {
		if got := string(jsSlice(s, c.a, c.b)); got != c.want {
			t.Errorf("jsSlice(%d,%d) = %q want %q", c.a, c.b, got, c.want)
		}
	}
}

func TestJSWhitespaceInPatterns(t *testing.T) {
	// JS \s matches a no-break space; Go's \s does not. The CL command regex
	// and the SEU "Columns" header rely on it.
	got := Lines(LangCL, []string{"  CHGVAR VAR(&A)"}, 1)[0]
	if got[2] != TokKeyword {
		t.Errorf("command after NBSP: kind %d, want keyword", got[2])
	}
}

func TestCLContinuationRules(t *testing.T) {
	// An open comment on the line does not continue the command; a trailing
	// "-" does; a trailing "+" inside a comment is ignored.
	lines := []string{"  CHGVAR VAR(&A) VALUE(1) /* note + */", "  DLYJOB DLY(1) -", "  ADDLIBLE LIB(X)"}
	k := Lines(LangCL, lines, 1)
	if k[1][2] != TokKeyword {
		t.Errorf("line 2 command kind = %d", k[1][2])
	}
	if k[2][2] != TokNone {
		t.Errorf("continued line 3 starts a command: kind %d", k[2][2])
	}
	if k[0][len(lines[0])-1] != TokComment {
		t.Errorf("comment not painted")
	}
}

func TestCLElseCommand(t *testing.T) {
	line := "  ELSE CMD(GOTO CMDLBL(X))"
	k := Lines(LangCL, []string{line}, 1)[0]
	// ELSE keyword, then the CMD word consumed as the command after ELSE.
	if k[2] != TokKeyword || k[7] != TokKeyword {
		t.Errorf("ELSE/CMD kinds = %d,%d", k[2], k[7])
	}
}

func TestRPGFixedDefaultsAndTail(t *testing.T) {
	// Text past column 80 is a comment; columns 6-7 blank = free form in 8-80.
	long := "     C                   EVAL      X = 1" + strings.Repeat(" ", 40) + "TAILTEXT"
	k := Lines(LangRPG, []string{long}, 1)[0]
	if k[strings.Index(long, "TAILTEXT")] != TokComment {
		t.Error("text past column 80 is not a comment")
	}
	free := "       dcl-s x int(10);"
	if Lines(LangRPG, []string{free}, 1)[0][7] != TokKeyword {
		t.Error("column 8 free-form keyword")
	}
}

func TestFirstColumnShift(t *testing.T) {
	// With "Columns . . . : 6 76" the visible text starts at source column 6.
	line := "C                   EVAL      X = 1"
	k := Lines(LangRPG, []string{line}, 6)[0]
	if k[0] != TokSpec || k[strings.Index(line, "EVAL")] != TokKeyword {
		t.Errorf("shifted columns: spec=%d eval=%d", k[0], k[strings.Index(line, "EVAL")])
	}
}

func TestMultibyteRunesKeepColumns(t *testing.T) {
	// One token per rune: indexes stay screen columns with non-ASCII text.
	line := "       dcl-s é int(10); // ü"
	k := Lines(LangRPG, []string{line}, 1)[0]
	if len(k) != len([]rune(line)) {
		t.Fatalf("len %d, want %d", len(k), len([]rune(line)))
	}
	if k[len([]rune(line))-1] != TokComment {
		t.Error("trailing comment not painted at rune index")
	}
}
