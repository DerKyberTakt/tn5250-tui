package highlight

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// Word lists (verbatim from sourceHighlight.ts).

func words(s string) map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(s) {
		m[strings.ToUpper(w)] = true
	}
	return m
}

var rpgKeywords = words(`
  dcl-s dcl-c dcl-ds dcl-f dcl-pr dcl-pi dcl-proc dcl-subf dcl-parm dcl-enum end-ds end-pr end-pi end-proc end-enum
  ctl-opt if elseif else endif dow dou enddo for to downto by endfor select when when-is when-in other endsl
  monitor on-error on-exit endmon begsr endsr exsr leavesr return leave iter eval evalr eval-corr callp call callb
  chain read reade readp readpe readc setll setgt write update delete unlock open close commit rolbk exfmt
  sorta clear reset dsply dump except feod post in out acq rel force next test xml-into xml-sax data-into data-gen
  snd-msg sorta cab cabeq cabne caseq cas endcs comp do doweq downe doueq goto tag lookup mvr move movel movea
  add sub mult div z-add z-sub scan xlate check checkr cat subst time occur kfld klist plist parm define
  adddur subdur extrct bitoff biton testb testn testz shtdn deallocate alloc realloc
  char varchar graph vargraph ucs2 varucs2 int uns packed zoned bindec float date time timestamp ind pointer object
  like likeds likerec likefile dim qualified inz template const value options extpgm extproc export import based
  overlay pos static varying noopt ascend descend ctdata perrcd alt extname extfld prefix occurs nullind samepos
  dftactgrp actgrp bnddir option datfmt timfmt decedit copyright main nomain alwnull debug expropts ccsid text
  usropn infds infsr sfile rename extfile extmbr indds keyed usage workstn printer disk seq special oflind extdesc
  handler block commit maxdev devid sfile rcdlen saveds saveind ignore include pgmname procptr opdesc rtnparm
  nopass omit varsize string trim exact
`)

var sqlKeywords = words(`
  select insert update delete merge from where and or not in is null like between exists join left right inner
  outer full cross on using group by order having union intersect except all distinct as case when then else end
  into values set create replace table view index procedure function trigger alter drop declare cursor for open
  fetch close next prior first last rows row only with exec sql begin commit rollback savepoint label comment grant
  revoke return returns language specific deterministic called reads modifies contains data external name
  parameter style asc desc limit offset call if elseif while loop repeat until leave iterate signal resignal
  handler continue exit condition default primary key foreign references constraint unique check generated always
  identity char character varchar integer int bigint smallint decimal dec numeric date time timestamp clob blob
  double real float boolean graphic vargraphic varbinary binary current schema system nolock ur cs rs rr nc
  include sqlca sqlda describe prepare execute immediate get diagnostics connect disconnect release lock share
  exclusive mode result sets new old referencing each statement after before instead of option naming
`)

// Fixed-format C-spec operations whose factor 2 is an expression (columns 36-80).
var extFactor2 = words(`eval evalr eval-corr if elseif dow dou when for callp return on-error sorta xml-into data-into data-gen dsply snd-msg on-exit when-is when-in`)

// JavaScript's \s (and String.prototype.trim) cover more than Go's ASCII-only
// \s; this class is substituted for \s / \S in the patterns below so the
// regexes behave the same on any screen text.
const jsSpaceClass = `\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}`

// jsRe compiles a pattern written with JavaScript's \s and \S.
func jsRe(p string) *regexp.Regexp {
	p = strings.ReplaceAll(p, `\S`, `[^`+jsSpaceClass+`]`)
	p = strings.ReplaceAll(p, `\s`, `[`+jsSpaceClass+`]`)
	return regexp.MustCompile(p)
}

func isJSSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xa0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff:
		return true
	}
	return r >= 0x2000 && r <= 0x200a
}

func trimJS(s []rune) string {
	return strings.TrimFunc(string(s), isJSSpace)
}

func trimEndJS(s []rune) []rune {
	n := len(s)
	for n > 0 && isJSSpace(s[n-1]) {
		n--
	}
	return s[:n]
}

func trimStartJS(s []rune) []rune {
	i := 0
	for i < len(s) && isJSSpace(s[i]) {
		i++
	}
	return s[i:]
}

// charAt returns the rune at i, or 0 when i is out of range (JS: undefined).
func charAt(t []rune, i int) rune {
	if i < 0 || i >= len(t) {
		return 0
	}
	return t[i]
}

func isAlpha(c rune) bool { return (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') }
func isDigit(c rune) bool { return c >= '0' && c <= '9' }
func isAlnum(c rune) bool { return isAlpha(c) || isDigit(c) }

// [A-Za-z@#$_]
func isWordStart(c rune) bool { return isAlpha(c) || c == '@' || c == '#' || c == '$' || c == '_' }

// [A-Za-z0-9@#$_-]
func isWord(c rune) bool { return isWordStart(c) || isDigit(c) || c == '-' }

// [A-Za-z0-9@#$_]
func isWordNoDash(c rune) bool { return isWordStart(c) || isDigit(c) }

// jsSlice mirrors String.prototype.slice(a, b) on a rune slice.
func jsSlice(t []rune, a, b int) []rune {
	n := len(t)
	norm := func(x int) int {
		if x < 0 {
			return max(n+x, 0)
		}
		return min(x, n)
	}
	a, b = norm(a), norm(b)
	if a >= b {
		return nil
	}
	return t[a:b]
}

func jsSliceFrom(t []rune, a int) []rune { return jsSlice(t, a, len(t)) }

// indexRunes mirrors String.prototype.indexOf(pat, from) for a non-empty pat.
func indexRunes(t []rune, pat string, from int) int {
	p := []rune(pat)
	if from < 0 {
		from = 0
	}
	for i := from; i+len(p) <= len(t); i++ {
		ok := true
		for k, r := range p {
			if t[i+k] != r {
				ok = false
				break
			}
		}
		if ok {
			return i
		}
	}
	return -1
}

// byteToRune converts a byte offset in s to a rune offset.
func byteToRune(s string, b int) int {
	if b < 0 {
		return b
	}
	return utf8.RuneCountInString(s[:b])
}

// findIdx runs re on t and returns the submatch spans as rune offsets
// (-1 for a group that did not take part), or nil when there is no match.
func findIdx(re *regexp.Regexp, t []rune) []int {
	s := string(t)
	loc := re.FindStringSubmatchIndex(s)
	if loc == nil {
		return nil
	}
	for i := range loc {
		loc[i] = byteToRune(s, loc[i])
	}
	return loc
}

// findAllIdx is the matchAll counterpart of findIdx.
func findAllIdx(re *regexp.Regexp, t []rune) [][]int {
	s := string(t)
	all := re.FindAllStringSubmatchIndex(s, -1)
	for _, loc := range all {
		for i := range loc {
			loc[i] = byteToRune(s, loc[i])
		}
	}
	return all
}

func paint(out []Tok, from, to int, kind Tok) {
	for i := max(0, from); i < min(len(out), to); i++ {
		out[i] = kind
	}
}

// tokAt reads out[i], or 255 when out of range (JS: undefined).
func tokAt(out []Tok, i int) Tok {
	if i < 0 || i >= len(out) {
		return 255
	}
	return out[i]
}

// readString reads a quoted string starting at i (doubled quote = literal
// quote). Returns the end index.
func readString(text []rune, i int, quote rune) int {
	j := i + 1
	for j < len(text) {
		if text[j] == quote {
			if charAt(text, j+1) == quote {
				j += 2
				continue
			}
			return j + 1
		}
		j++
	}
	return len(text)
}

type exprOptions struct {
	keywords        map[string]bool
	slashComments   bool // "//" starts a comment
	callsAreBuiltin bool // words followed by "(" that are not keywords count as functions
	cl              bool // "&NAME" variables, "%BUILTIN"
	sql             bool // ":host" variables, "--" comments
}

var reIndicator = regexp.MustCompile(`(?i)^\*IN(\d\d|[A-Z]\d|LR|RT|H\d|U\d)$`)

// scanExpr tokenizes the expression region [from, to) of a line.
func scanExpr(text []rune, from, to int, out []Tok, o exprOptions) {
	i := from
	end := min(to, len(text))
	for i < end {
		c := text[i]
		if c == ' ' {
			i++
			continue
		}
		if o.slashComments && c == '/' && charAt(text, i+1) == '/' {
			paint(out, i, end, TokComment)
			return
		}
		if o.sql && c == '-' && charAt(text, i+1) == '-' {
			paint(out, i, end, TokComment)
			return
		}
		if c == '\'' {
			j := min(readString(text, i, '\''), end)
			paint(out, i, j, TokString)
			i = j
			continue
		}
		if c == '%' && isAlpha(charAt(text, i+1)) {
			j := i + 1
			for j < end && isAlnum(text[j]) {
				j++
			}
			paint(out, i, j, TokBuiltin)
			i = j
			continue
		}
		if c == '*' && isAlpha(charAt(text, i+1)) {
			j := i + 1
			for j < end && isAlnum(text[j]) {
				j++
			}
			kind := TokSpecial
			if reIndicator.MatchString(string(text[i:j])) {
				kind = TokIndicator
			}
			paint(out, i, j, kind)
			i = j
			continue
		}
		if o.cl && c == '&' && isWordStart(charAt(text, i+1)) {
			j := i + 1
			for j < end && isWordNoDash(text[j]) {
				j++
			}
			paint(out, i, j, TokVariable)
			i = j
			continue
		}
		if o.sql && c == ':' && isWordStart(charAt(text, i+1)) {
			j := i + 1
			for j < end && (isWordNoDash(text[j]) || text[j] == '.') {
				j++
			}
			paint(out, i, j, TokVariable)
			i = j
			continue
		}
		if isDigit(c) && !isWord(charAt(text, i-1)) {
			j := i
			for j < end && (isDigit(text[j]) || text[j] == '.') {
				j++
			}
			if !isWord(charAt(text, j)) {
				paint(out, i, j, TokNumber)
			}
			i = j
			continue
		}
		if isWordStart(c) {
			j := i
			// SQL and CL words have no "-" inside; RPG has dcl-s, end-proc, on-error ...
			for j < end && ((o.sql || o.cl) && isWordNoDash(text[j]) || !(o.sql || o.cl) && isWord(text[j])) {
				j++
			}
			word := strings.ToUpper(string(text[i:j]))
			if o.keywords[word] {
				paint(out, i, j, TokKeyword)
			} else if o.callsAreBuiltin && charAt(text, j) == '(' {
				paint(out, i, j, TokBuiltin)
			}
			i = j
			continue
		}
		i++
	}
}
