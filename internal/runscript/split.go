package runscript

import "strings"

// Statement is one runnable piece of a script and the lines it spans (0-based, inclusive).
type Statement struct {
	Classified
	Line, EndLine int
}

// Split cuts a script into statements. A line that starts like a CL command or a shell
// command line is one statement on its own (CL continues over a line ending in `+`, a
// shell line over one ending in `\`); SQL runs on until a `;` outside quotes and comments
// or until a blank line. Comment-only and blank lines between statements belong to none.
func Split(text string) []Statement {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	var out []Statement
	i := 0
	for i < len(lines) {
		raw := lines[i]
		if strings.TrimSpace(raw) == "" {
			i++
			continue
		}
		// A trailing continuation mark must not hide the kind of the first line.
		probe := strings.TrimRight(raw, " \t")
		probe = strings.TrimRight(strings.TrimSuffix(strings.TrimSuffix(probe, "+"), "\\"), " \t")
		c := Classify(probe)
		switch c.Kind {
		case CL, QSH:
			start := i
			cont := CL == c.Kind && strings.HasSuffix(strings.TrimRight(raw, " \t"), "+") ||
				QSH == c.Kind && strings.HasSuffix(strings.TrimRight(raw, " \t"), "\\")
			text := raw
			for cont && i+1 < len(lines) {
				i++
				text += "\n" + lines[i]
				t := strings.TrimRight(lines[i], " \t")
				cont = c.Kind == CL && strings.HasSuffix(t, "+") || c.Kind == QSH && strings.HasSuffix(t, "\\")
			}
			cl := Classify(joinContinued(text, c.Kind))
			out = append(out, Statement{Classified: cl, Line: start, EndLine: i})
			i++
		default:
			start, end := i, i
			var buf []string
			for i < len(lines) {
				l := lines[i]
				if strings.TrimSpace(l) == "" {
					break
				}
				buf = append(buf, l)
				end = i
				if endsStatement(strings.Join(buf, "\n")) {
					break
				}
				i++
			}
			text := strings.TrimSpace(strings.Join(buf, "\n"))
			if strings.HasSuffix(text, ";") {
				text = strings.TrimSpace(strings.TrimSuffix(text, ";"))
			}
			if text != "" && !commentOnly(text) {
				cl := Classify(text)
				out = append(out, Statement{Classified: cl, Line: start, EndLine: end})
			}
			i = end + 1
		}
	}
	return out
}

// joinContinued folds CL `+` and shell `\` continuations into one line.
func joinContinued(text string, k Kind) string {
	lines := strings.Split(text, "\n")
	var b strings.Builder
	for i, l := range lines {
		t := strings.TrimRight(l, " \t")
		if i < len(lines)-1 {
			switch k {
			case CL:
				t = strings.TrimRight(strings.TrimSuffix(t, "+"), " 	")
			case QSH:
				t = strings.TrimRight(strings.TrimSuffix(t, "\\"), " 	")
			}
		}
		if i > 0 {
			t = strings.TrimLeft(t, " \t")
			b.WriteByte(' ')
		}
		b.WriteString(t)
	}
	return b.String()
}

// endsStatement reports whether the text ends with a `;` that is outside quotes and
// comments (trailing blanks and a trailing line comment are ignored).
func endsStatement(text string) bool {
	inStr, inBlock := false, false
	var quote byte
	last := byte(0)
	for i := 0; i < len(text); i++ {
		ch := text[i]
		switch {
		case inBlock:
			if ch == '*' && i+1 < len(text) && text[i+1] == '/' {
				inBlock = false
				i++
			}
		case inStr:
			if ch == quote {
				if i+1 < len(text) && text[i+1] == quote {
					i++
				} else {
					inStr = false
				}
			}
		case ch == '\'' || ch == '"':
			inStr, quote = true, ch
			last = ch
		case ch == '-' && i+1 < len(text) && text[i+1] == '-':
			for i < len(text) && text[i] != '\n' {
				i++
			}
		case ch == '/' && i+1 < len(text) && text[i+1] == '*':
			inBlock = true
			i++
		case ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r':
		default:
			last = ch
		}
	}
	return !inStr && !inBlock && last == ';'
}

// commentOnly reports whether the text holds nothing but SQL comments.
func commentOnly(text string) bool {
	return strings.TrimSpace(commentRe.ReplaceAllString(text, "")) == ""
}

// At returns the statement that spans line, or the last one above it.
func At(stmts []Statement, line int) (Statement, bool) {
	var found Statement
	ok := false
	for _, s := range stmts {
		if s.Line <= line {
			found, ok = s, true
		}
		if s.Line <= line && line <= s.EndLine {
			return s, true
		}
	}
	return found, ok
}
