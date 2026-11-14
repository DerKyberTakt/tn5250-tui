package highlight

import (
	"strings"
)

var (
	reFreeStart = jsRe(`(?i)^(\*\*free|dcl-|end-|ctl-opt\b|if\b|elseif\b|else\b|endif\b|dow\b|dou\b|enddo\b|for\b|endfor\b|select\b|when\b|other\b|endsl\b|monitor\b|on-error\b|endmon\b|begsr\b|endsr\b|exsr\b|return\b|leave\b|iter\b|exec\s+sql\b|//)`)
	reStarFree  = jsRe(`(?i)^\*\*free\b`)
	reExecSQL   = jsRe(`(?i)exec\s+sql\b`)
	reExecSQL2  = jsRe(`(?i)exec\s+sql`)
	reFixedExec = jsRe(`(?i)^/\s*exec\s+sql`)
	reFixedEnd  = jsRe(`(?i)^/\s*end-exec`)
	reDirective = jsRe(`^/[A-Za-z-]+`)
	reOpWord    = jsRe(`^[A-Za-z-]+`)
)

func isFullyFree(lines [][]rune, firstColumn int) bool {
	if firstColumn != 1 {
		return false
	}
	free, fixed := 0, 0
	for _, t := range lines {
		if reStarFree.MatchString(string(t)) {
			return true
		}
		lead := -1
		for i, r := range t {
			if !isJSSpace(r) {
				lead = i
				break
			}
		}
		if lead < 0 {
			continue
		}
		c5, c6 := charAt(t, 5), charAt(t, 6)
		if strings.ContainsRune("HFDICOPhfdicop", c5) && c5 != 0 && (c6 == ' ' || c6 == '*' || c6 == '/' || c6 == '+') {
			fixed++
		} else if lead < 5 && reFreeStart.MatchString(string(t[lead:])) {
			free++
		}
	}
	return free > 0 && free >= fixed
}

type rpgState struct {
	sql        bool
	sqlComment sqlState
}

// freeLine colours free-form code from index from; embedded SQL carries over
// lines until its semicolon.
func freeLine(text []rune, from int, out []Tok, st *rpgState) {
	start := from
	if st.sql {
		semi := indexRunes(text, ";", start)
		stop := len(text)
		if semi >= 0 {
			stop = semi + 1
		}
		highlightSQLRange(jsSlice(text, 0, stop), start, out, &st.sqlComment)
		if semi < 0 {
			return
		}
		st.sql = false
		start = stop
	}
	if m := findIdx(reExecSQL, jsSliceFrom(text, start)); m != nil {
		s := start + m[0]
		mlen := m[1] - m[0]
		scanExpr(text, start, s, out, exprOptions{keywords: rpgKeywords, slashComments: true})
		if tokAt(out, s) == TokComment {
			return
		}
		paint(out, s, s+mlen, TokKeyword)
		st.sql = true
		freeLine(text, s+mlen, out, st)
		return
	}
	scanExpr(text, start, len(text), out, exprOptions{keywords: rpgKeywords, slashComments: true})
}

func highlightRPG(lines [][]rune, firstColumn int) [][]Tok {
	st := &rpgState{}
	res := make([][]Tok, len(lines))
	if isFullyFree(lines, firstColumn) {
		for i, text := range lines {
			out := make([]Tok, len(text))
			if reStarFree.MatchString(string(text)) {
				paint(out, 0, 6, TokKeyword)
			} else {
				freeLine(text, 0, out, st)
			}
			res[i] = out
		}
		return res
	}
	for li, text := range lines {
		out := make([]Tok, len(text))
		res[li] = out
		at := func(c int) int { return c - firstColumn }
		seg := func(c1, c2 int) []rune { return jsSlice(text, max(0, at(c1)), max(0, at(c2)+1)) }
		put := func(c1, c2 int, kind Tok) {
			if trimJS(seg(c1, c2)) != "" {
				paint(out, max(0, at(c1)), at(c2)+1, kind)
			}
		}
		expr := func(c1, c2 int) {
			scanExpr(text, max(0, at(c1)), at(c2)+1, out, exprOptions{keywords: rpgKeywords})
		}
		tail := func() {
			if len(text) > at(80)+1 {
				paint(out, at(81), len(text), TokComment)
			}
		}

		put(1, 5, TokMarker)
		spec := strings.ToUpper(string(seg(6, 6)))
		c7 := string(seg(7, 7))

		// Embedded SQL in fixed form: C/EXEC SQL, C+ ..., C/END-EXEC.
		if spec == "C" && c7 == "/" && reFixedExec.MatchString(string(seg(7, 80))) {
			put(6, 6, TokSpec)
			mlen := 0
			mindex := 0
			if m := findIdx(reExecSQL2, jsSliceFrom(text, at(7))); m != nil {
				mindex = m[0]
				mlen = m[1] - m[0]
			}
			s := at(7) + mindex
			paint(out, at(7), at(7)+1, TokKeyword)
			paint(out, s, s+mlen, TokKeyword)
			highlightSQLRange(jsSlice(text, 0, at(80)+1), s+mlen, out, &st.sqlComment)
			st.sql = true
			tail()
			continue
		}
		if spec == "C" && c7 == "/" && reFixedEnd.MatchString(string(seg(7, 80))) {
			put(6, 6, TokSpec)
			put(7, 16, TokKeyword)
			st.sql = false
			continue
		}
		if spec == "C" && c7 == "+" && st.sql {
			put(6, 7, TokSpec)
			highlightSQLRange(jsSlice(text, 0, at(80)+1), max(0, at(8)), out, &st.sqlComment)
			tail()
			continue
		}

		if c7 == "*" && (spec == "" || spec == " " || (len(spec) == 1 && strings.Contains("HFDICOP", spec))) {
			put(6, 6, TokSpec)
			paint(out, max(0, at(7)), len(text), TokComment)
			continue
		}
		if c7 == "/" {
			put(6, 6, TokSpec)
			dirLen := 1
			if m := findIdx(reDirective, seg(7, 80)); m != nil {
				dirLen = m[1] - m[0]
				paint(out, at(7), at(7)+dirLen, TokKeyword)
			}
			expr(7+dirLen, 80)
			continue
		}

		switch spec {
		case "H":
			put(6, 6, TokSpec)
			expr(7, 80)
		case "F":
			put(6, 6, TokSpec)
			put(7, 16, TokName)
			for _, c := range []int{17, 18, 19, 20, 22, 28, 34, 35} {
				put(c, c, TokSpecial)
			}
			put(23, 27, TokNumber)
			put(29, 33, TokNumber)
			put(36, 42, TokKeyword)
			expr(44, 80)
		case "D", "P":
			put(6, 6, TokSpec)
			put(7, 21, TokName)
			put(22, 23, TokSpecial)
			put(24, 25, TokKeyword)
			put(26, 39, TokNumber)
			put(40, 40, TokSpecial)
			put(41, 42, TokNumber)
			expr(44, 80)
		case "C":
			put(6, 6, TokSpec)
			put(7, 8, TokIndicator)
			put(9, 11, TokIndicator)
			opcode := []rune(trimJS(seg(26, 35)))
			opWord := ""
			if m := findIdx(reOpWord, opcode); m != nil {
				opWord = strings.ToUpper(string(opcode[m[0]:m[1]]))
			}
			expr(12, 25)
			if len(opcode) > 0 {
				put(26, 35, TokKeyword)
			}
			if len(opcode) == 0 || extFactor2[opWord] {
				expr(36, 80)
			} else {
				expr(36, 49)
				expr(50, 63)
				put(64, 70, TokNumber)
				put(71, 76, TokIndicator)
			}
		case "I", "O":
			put(6, 6, TokSpec)
			expr(7, 80)
		default:
			// Columns 6-7 blank: free-form code in columns 8-80.
			if trimJS(seg(6, 7)) == "" {
				freeLine(jsSlice(text, 0, at(80)+1), max(0, at(8)), out, st)
			}
		}
		tail()
	}
	return res
}
