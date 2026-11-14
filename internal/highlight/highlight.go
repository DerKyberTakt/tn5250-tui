// Package highlight colours the SEU (Source Entry Utility) edit and browse
// screens: CL, DDS, RPG/RPGLE (fixed, free and fully free) and DB2 SQL,
// including SQL embedded in RPG. Colouring only: nothing is sent to the host.
//
// It mirrors the emulators' shared sourceHighlight.ts (the TypeScript module
// in the VS Code terminal extension and the desktop app); that file is the
// source of truth, so behaviour changes land there first and are ported here.
// The language is worked out from the source file name in the SEU header
// (QRPGLESRC, QCLLESRC, QDDSSRC, QSQLSRC ...) and falls back to the content.
//
// All indexes are rune indexes: one rune per screen cell, as in the TS, where
// one UTF-16 unit is one character.
package highlight

import (
	"regexp"
	"strconv"
	"strings"
)

// Tok is a token kind; TokNone means "leave the host colour".
type Tok uint8

// Token kinds (same numbering as the TS Tok object).
const (
	TokNone Tok = iota
	TokComment
	TokKeyword
	TokBuiltin
	TokString
	TokNumber
	TokSpecial
	TokVariable
	TokIndicator
	TokName
	TokMarker
	TokSpec
)

// Lang is a source language.
type Lang string

// Languages.
const (
	LangRPG Lang = "rpg"
	LangCL  Lang = "cl"
	LangDDS Lang = "dds"
	LangSQL Lang = "sql"
)

// SeuLine is one source line found on an SEU screen.
type SeuLine struct {
	Row int
	// Col is the screen column of the first visible source character.
	Col  int
	Text string
}

// SeuScreen is an SEU edit/browse screen.
type SeuScreen struct {
	Lang Lang
	// FirstColumn is the source column of the first visible character
	// ("Columns . . . : 1 71").
	FirstColumn int
	SourceFile  string
	Lines       []SeuLine
}

// Field describes one screen field for DetectSeuFields: Start is the
// row*cols+col position of its first cell.
type Field struct {
	Start  int
	Length int
	Bypass bool
}

// ---------------------------------------------------------------------------
// Language detection

var (
	reLangRPG = regexp.MustCompile(`RPG`)
	reLangCL  = regexp.MustCompile(`^Q?CL|CLLE|CLP|QCL`)
	reLangDDS = regexp.MustCompile(`DDS|DSPF|PRTF|QPF|QLF`)
	reLangSQL = regexp.MustCompile(`SQL`)
)

// LanguageFromFile guesses the language from the SEU source file name.
func LanguageFromFile(sourceFile string) (Lang, bool) {
	f := strings.ToUpper(sourceFile)
	switch {
	case reLangRPG.MatchString(f):
		return LangRPG, true
	case reLangCL.MatchString(f):
		return LangCL, true
	case reLangDDS.MatchString(f):
		return LangDDS, true
	case reLangSQL.MatchString(f):
		return LangSQL, true
	}
	return "", false
}

var (
	reContentFree = jsRe(`(?i)^\s*(\*\*free|dcl-|ctl-opt)`)
	reContentCL   = jsRe(`(?i)^\s*(\w+:\s*)?(PGM|ENDPGM|DCL|DCLF|CHGVAR|MONMSG|CALL|SNDPGMMSG|RTVJOBA|OVRDBF|DLTOVR|GOTO|IF|ELSE|DO|ENDDO)\b`)
	reContentSQL  = jsRe(`(?i)^\s*(select|insert|update|delete|create|alter|drop|with|set|call|values|merge)\b`)
)

// LanguageFromContent guesses the language from the visible lines (source
// file names are not always standard).
func LanguageFromContent(lines []string, firstColumn int) (Lang, bool) {
	rpg, dds, cl, sql := 0, 0, 0, 0
	for _, line := range lines {
		t := []rune(line)
		c6 := charAt(t, 6-firstColumn)
		if reContentFree.MatchString(line) {
			rpg += 3
		}
		switch {
		case (c6 == 'A' || c6 == 'a') && firstColumn <= 6:
			dds++
		case strings.ContainsRune("HFDCPhfdcp", c6) && c6 != 0 && firstColumn <= 6:
			rpg++
		}
		if reContentCL.MatchString(line) {
			cl++
		}
		if reContentSQL.MatchString(line) {
			sql++
		}
	}
	best := max(rpg, dds, cl, sql)
	switch {
	case best == 0:
		return "", false
	case best == rpg:
		return LangRPG, true
	case best == dds:
		return LangDDS, true
	case best == cl:
		return LangCL, true
	}
	return LangSQL, true
}

// ---------------------------------------------------------------------------
// SEU screen detection

var (
	reSeq      = regexp.MustCompile(`^(\d{4}\.\d{2}|\d{6,7}|'{7}|,{7})`)
	reColumns  = jsRe(`Columns\s*\.\s*\.\s*\.\s*:\s*(\d+)\s+(\d+)`)
	reSeuCmd   = jsRe(`SEU\s*==>`)
	reLibFile  = jsRe(`(\S+)/(\S+)\s*$`)
	reDataMark = jsRe(`(?i)\*{5,}\s*(Beginning|End) of data`)
)

// DetectSeu recognises an SEU edit/browse screen and returns its source
// lines. rowText returns the characters of a row as a string of exactly cols
// runes (blank for nulls and attribute bytes). Without field information a
// data row is recognised by its sequence number only; use DetectSeuFields
// when the screen's fields are known.
func DetectSeu(rows, cols int, rowText func(row int) string) (SeuScreen, bool) {
	return DetectSeuFields(rows, cols, rowText, nil)
}

// DetectSeuFields is DetectSeu with the screen's fields: in edit mode each
// data line is [sequence/line command][source], so a long input field that
// starts in the first 20 columns marks a data row (and where its source
// starts) even when the sequence number is not recognisable.
func DetectSeuFields(rows, cols int, rowText func(row int) string, fields []Field) (SeuScreen, bool) {
	if rows < 10 {
		return SeuScreen{}, false
	}
	rt := func(r int) []rune {
		return []rune(strings.ReplaceAll(rowText(r), "\x00", " "))
	}
	head := string(rt(0))
	cm := reColumns.FindStringSubmatch(head)
	if cm == nil {
		return SeuScreen{}, false
	}
	seuLine := false
	for r := 0; r < 3; r++ {
		if reSeuCmd.MatchString(string(rt(r))) {
			seuLine = true
		}
	}
	if !seuLine {
		return SeuScreen{}, false
	}
	first, err := strconv.ParseFloat(cm[1], 64)
	if err != nil {
		return SeuScreen{}, false
	}
	firstColumn := int(first)
	sourceFile := ""
	if fm := reLibFile.FindStringSubmatch(head); fm != nil {
		sourceFile = fm[2]
	}

	longFieldStart := map[int]int{}
	for _, f := range fields {
		if f.Bypass || f.Length < 20 || cols <= 0 {
			continue
		}
		row := f.Start / cols
		col := f.Start % cols
		if _, ok := longFieldStart[row]; col > 0 && col < 20 && !ok {
			longFieldStart[row] = col
		}
	}
	var lines []SeuLine
	for r := 2; r < rows-2; r++ {
		t := rt(r)
		fieldCol, hasField := longFieldStart[r]
		seqOk := reSeq.MatchString(string(jsSlice(trimStartJS(t), 0, 7))) || reSeq.MatchString(string(jsSlice(t, 0, 7)))
		if !hasField && !seqOk {
			continue
		}
		if reDataMark.MatchString(string(t)) {
			continue
		}
		col := 8
		if hasField {
			col = fieldCol
		}
		lines = append(lines, SeuLine{Row: r, Col: col, Text: string(jsSliceFrom(t, col))})
	}
	if len(lines) == 0 {
		return SeuScreen{}, false
	}
	lang, ok := LanguageFromFile(sourceFile)
	if !ok {
		texts := make([]string, len(lines))
		for i, l := range lines {
			texts[i] = l.Text
		}
		lang, ok = LanguageFromContent(texts, firstColumn)
		if !ok {
			lang = LangRPG
		}
	}
	return SeuScreen{Lang: lang, FirstColumn: firstColumn, SourceFile: sourceFile, Lines: lines}, true
}

// Lines returns the token kinds for each line of source text (one entry per
// character).
func Lines(lang Lang, lines []string, firstColumn int) [][]Tok {
	rs := make([][]rune, len(lines))
	for i, l := range lines {
		rs[i] = []rune(l)
	}
	switch lang {
	case LangCL:
		return highlightCL(rs)
	case LangDDS:
		return highlightDDS(rs, firstColumn)
	case LangSQL:
		return highlightSQL(rs)
	}
	return highlightRPG(rs, firstColumn)
}

// Screen returns per-cell token kinds (rows*cols) for an SEU screen, or false
// when the screen is not SEU.
func Screen(rows, cols int, rowText func(int) string) ([]Tok, bool) {
	return ScreenFields(rows, cols, rowText, nil)
}

// ScreenFields is Screen with the screen's fields (see DetectSeuFields).
func ScreenFields(rows, cols int, rowText func(int) string, fields []Field) ([]Tok, bool) {
	seu, ok := DetectSeuFields(rows, cols, rowText, fields)
	if !ok {
		return nil, false
	}
	cells := make([]Tok, rows*cols)
	texts := make([]string, len(seu.Lines))
	for i, l := range seu.Lines {
		texts[i] = l.Text
	}
	kinds := Lines(seu.Lang, texts, seu.FirstColumn)
	for i, l := range seu.Lines {
		k := kinds[i]
		base := l.Row*cols + l.Col
		n := min(len(k), cols-l.Col)
		if n <= 0 || base < 0 {
			continue
		}
		n = min(n, len(cells)-base)
		if n > 0 {
			copy(cells[base:base+n], k[:n])
		}
	}
	return cells, true
}
