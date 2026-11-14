package highlight

import (
	"regexp"
	"strings"
)

var reDDSKeywordHint = regexp.MustCompile(`^[A-Z][A-Z0-9]{1,9}$`)

// highlightDDS colours A-specs by column; firstColumn is the source column of
// the first character of each line.
func highlightDDS(lines [][]rune, firstColumn int) [][]Tok {
	res := make([][]Tok, len(lines))
	for li, text := range lines {
		out := make([]Tok, len(text))
		res[li] = out
		at := func(c int) int { return c - firstColumn } // source column (1-based) -> index
		seg := func(c1, c2 int) []rune { return jsSlice(text, max(0, at(c1)), max(0, at(c2)+1)) }
		put := func(c1, c2 int, kind Tok) {
			if trimJS(seg(c1, c2)) != "" {
				paint(out, max(0, at(c1)), at(c2)+1, kind)
			}
		}
		put(1, 5, TokMarker)
		if string(seg(7, 7)) == "*" {
			put(6, 6, TokSpec)
			paint(out, max(0, at(7)), len(text), TokComment)
			continue
		}
		put(6, 6, TokSpec)
		put(7, 7, TokKeyword) // A/O conditioning
		for _, c := range []int{8, 11, 14} {
			put(c, c+2, TokIndicator)
		}
		put(17, 17, TokKeyword) // R K S O J H
		put(19, 28, TokName)
		put(29, 29, TokSpecial)
		put(30, 34, TokNumber)
		put(35, 35, TokSpecial)
		put(36, 37, TokNumber)
		put(38, 38, TokSpecial)
		put(39, 44, TokNumber)
		// Functions area 45-80: keywords at paren depth 0, strings, numbers.
		from := max(0, at(45))
		to := min(len(text), at(80)+1)
		depth := 0
		i := from
		for i < to {
			c := text[i]
			if c == '\'' {
				j := min(readString(text, i, '\''), to)
				paint(out, i, j, TokString)
				i = j
				continue
			}
			if c == '(' {
				depth++
			} else if c == ')' {
				depth = max(0, depth-1)
			}
			if isWordStart(c) {
				j := i
				for j < to && isWordNoDash(text[j]) {
					j++
				}
				w := string(text[i:j])
				if depth == 0 && reDDSKeywordHint.MatchString(strings.ToUpper(w)) {
					paint(out, i, j, TokKeyword)
				}
				i = j
				continue
			}
			if c == '*' && isAlpha(charAt(text, i+1)) {
				j := i + 1
				for j < to && isAlnum(text[j]) {
					j++
				}
				paint(out, i, j, TokSpecial)
				i = j
				continue
			}
			if isDigit(c) && !isWord(charAt(text, i-1)) {
				j := i
				for j < to && (isDigit(text[j]) || text[j] == '.') {
					j++
				}
				paint(out, i, j, TokNumber)
				i = j
				continue
			}
			i++
		}
		if len(text) > to {
			paint(out, to, len(text), TokComment)
		}
	}
	return res
}
