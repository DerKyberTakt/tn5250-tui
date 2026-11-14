package highlight

import (
	"regexp"
	"strings"
)

var (
	reCLCommand = jsRe(`^(\s*)(?:([A-Za-z@#$][A-Za-z0-9@#$_]*):\s*)?((?:[A-Za-z@#$][A-Za-z0-9@#$_]*/)?[A-Za-z@#$][A-Za-z0-9@#$_]*)`)
	reCLFlow    = regexp.MustCompile(`(?i)\b(THEN|ELSE|EXEC|CMD)\b(` + `[` + jsSpaceClass + `]*\([` + jsSpaceClass + `]*|[` + jsSpaceClass + `]+` + `)?([A-Za-z@#$][A-Za-z0-9@#$_]*)?`)
	reCLParm    = regexp.MustCompile(`([A-Za-z][A-Za-z0-9]*)\(`)
	// JS: /\/\*.*$/ where "." excludes line terminators.
	reCLComment = regexp.MustCompile(`/\*[^\n\r\x{2028}\x{2029}]*$`)
)

func highlightCL(lines [][]rune) [][]Tok {
	inComment := false
	continued := false
	res := make([][]Tok, len(lines))
	for li, text := range lines {
		out := make([]Tok, len(text))
		// Comments first (they can span lines); the rest is scanned around them.
		var code [][2]int
		i := 0
		for i < len(text) {
			if inComment {
				closeAt := indexRunes(text, "*/", i)
				j := len(text)
				if closeAt >= 0 {
					j = closeAt + 2
				}
				paint(out, i, j, TokComment)
				if closeAt < 0 {
					break
				}
				inComment = false
				i = j
				continue
			}
			// Skip strings so "/*" inside quotes is not a comment.
			j := i
			open := -1
			for j < len(text) {
				if text[j] == '\'' {
					j = readString(text, j, '\'')
					continue
				}
				if text[j] == '/' && charAt(text, j+1) == '*' {
					open = j
					break
				}
				j++
			}
			end := len(text)
			if open >= 0 {
				end = open
			}
			code = append(code, [2]int{i, end})
			if open < 0 {
				break
			}
			inComment = true
			i = open
		}
		for _, c := range code {
			scanExpr(text, c[0], c[1], out, exprOptions{keywords: map[string]bool{}, cl: true})
		}
		// Command names: the first word of a statement (after an optional
		// label), and the command inside THEN(...) / ELSE / DO.
		if !continued && len(code) > 0 {
			firstCode := code[0]
			seg := jsSlice(text, firstCode[0], firstCode[1])
			if m := findIdx(reCLCommand, seg); m != nil {
				leading := m[3] - m[2]
				labelStart := firstCode[0] + leading
				if m[4] >= 0 && m[5] > m[4] {
					paint(out, labelStart, labelStart+(m[5]-m[4])+1, TokName)
				}
				cmdLen := m[7] - m[6]
				cmdStart := firstCode[0] + m[1] - cmdLen
				if cmdLen > 0 {
					paint(out, cmdStart, cmdStart+cmdLen, TokKeyword)
				}
			}
		}
		// The command inside THEN(...), EXEC(...), CMD(...) and after ELSE.
		for _, m := range findAllIdx(reCLFlow, text) {
			idx := m[0]
			if k := tokAt(out, idx); k == TokComment || k == TokString {
				continue
			}
			kwLen := m[3] - m[2]
			kw := strings.ToUpper(string(text[m[2]:m[3]]))
			flow := kw == "THEN" || kw == "ELSE"
			hasParen := m[4] >= 0 && strings.ContainsRune(string(text[m[4]:m[5]]), '(')
			if !flow && !hasParen {
				continue
			}
			kind := TokName
			if flow {
				kind = TokKeyword
			}
			paint(out, idx, idx+kwLen, kind)
			if m[6] >= 0 && m[5] > m[4] && m[4] >= 0 && (hasParen || kw == "ELSE") {
				l3 := m[7] - m[6]
				s := idx + (m[1] - m[0]) - l3
				paint(out, s, s+l3, TokKeyword)
			}
		}
		// Parameter keywords: NAME( outside strings and comments.
		for _, m := range findAllIdx(reCLParm, text) {
			at := m[0]
			if tokAt(out, at) == TokNone {
				paint(out, at, at+(m[3]-m[2]), TokName)
			}
		}
		// Only a trailing + or - continues the command; an open comment does not.
		trimmed := text
		if loc := findIdx(reCLComment, text); loc != nil {
			trimmed = text[:loc[0]]
		}
		trimmed = trimEndJS(trimmed)
		continued = len(trimmed) > 0 && (trimmed[len(trimmed)-1] == '+' || trimmed[len(trimmed)-1] == '-')
		res[li] = out
	}
	return res
}
