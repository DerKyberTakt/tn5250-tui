package highlight

type sqlState struct {
	inComment bool
}

func highlightSQLRange(text []rune, from int, out []Tok, st *sqlState) {
	i := from
	for i < len(text) {
		if st.inComment {
			closeAt := indexRunes(text, "*/", i)
			j := len(text)
			if closeAt >= 0 {
				j = closeAt + 2
			}
			paint(out, i, j, TokComment)
			if closeAt < 0 {
				return
			}
			st.inComment = false
			i = j
			continue
		}
		open := indexRunes(text, "/*", i)
		stop := len(text)
		if open >= 0 {
			stop = open
		}
		scanExpr(text, i, stop, out, exprOptions{keywords: sqlKeywords, sql: true, callsAreBuiltin: true})
		// A "--" comment found by scanExpr hides any later "/*".
		if tokAt(out, stop-1) == TokComment || open < 0 {
			return
		}
		st.inComment = true
		i = open
	}
}

func highlightSQL(lines [][]rune) [][]Tok {
	st := &sqlState{}
	res := make([][]Tok, len(lines))
	for i, text := range lines {
		out := make([]Tok, len(text))
		highlightSQLRange(text, 0, out, st)
		res[i] = out
	}
	return res
}
