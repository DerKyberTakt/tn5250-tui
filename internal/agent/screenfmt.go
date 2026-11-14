package agent

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/DerKyberTakt/tn5250-tui/internal/tn5250"
)

// Pos is a 1-based screen position.
type Pos struct {
	Row int `json:"row"`
	Col int `json:"col"`
}

// FieldOut is one field of the format table in tool output. Rows, columns and the index are
// 1-based. Absent booleans are false.
type FieldOut struct {
	Index      int    `json:"index" jsonschema:"1-based field number, as accepted by type, fill and wait"`
	Row        int    `json:"row"`
	Col        int    `json:"col"`
	Length     int    `json:"length"`
	Label      string `json:"label,omitempty" jsonschema:"the text just left of the field on its row"`
	Bypass     bool   `json:"bypass,omitempty" jsonschema:"protected: cannot be typed into"`
	Nondisplay bool   `json:"nondisplay,omitempty" jsonschema:"hidden (a password): its text is never shown"`
	MDT        bool   `json:"mdt,omitempty" jsonschema:"modified since the host wrote the screen"`
	Text       string `json:"text,omitempty"`
}

// OIAOut is the status line under the screen.
type OIAOut struct {
	Connected      bool   `json:"connected"`
	Inhibited      string `json:"inhibited" jsonschema:"none (keyboard open), system (host has the keyboard) or error (reset needed)"`
	Insert         bool   `json:"insert,omitempty"`
	MessageWaiting bool   `json:"message_waiting,omitempty"`
	Message        string `json:"message,omitempty" jsonschema:"operator or host error text"`
	Device         string `json:"device,omitempty"`
	QueuedKeys     int    `json:"queued_keys,omitempty"`
}

// ScreenOut is the screen as an agent sees it.
type ScreenOut struct {
	// Text is the rows joined by newlines with trailing blanks trimmed and blank rows kept, so
	// the line number is the row number (with a rows filter: the first line is the first row of
	// the range).
	Text      string     `json:"text,omitempty"`
	Rows      string     `json:"rows,omitempty" jsonschema:"the row range text covers when a rows filter was given"`
	Cursor    Pos        `json:"cursor"`
	Fields    []FieldOut `json:"fields,omitempty"`
	OIA       OIAOut     `json:"oia"`
	Seq       uint64     `json:"seq"`
	Signature string     `json:"signature" jsonschema:"8 hex digits of the SHA-1 of the text: equal signature, equal screen"`
	Size      string     `json:"size"`
	Settled   *bool      `json:"settled,omitempty" jsonschema:"present after a key: true when the host answered and the keyboard is open"`
	Closed    string     `json:"closed,omitempty" jsonschema:"set when the connection has ended"`
}

// ScreenOpts select what formatScreen returns.
type ScreenOpts struct {
	Rows       string // "4-20", "5", "4-", "-10"; empty = all
	FieldsOnly bool
}

// fieldView is a field with its derived data.
type fieldView struct {
	Index      int // 1-based
	Row, Col   int // 1-based
	Start      int
	Length     int
	Bypass     bool
	Nondisplay bool
	MDT        bool
	Text       string
	Label      string // as shown
}

var labelTail = regexp.MustCompile(`[\s.:]+$`)

// normLabel is the form labels are compared in: lower case, single spaces, trailing ":" and
// ". . ." leaders dropped.
func normLabel(s string) string {
	s = strings.ToLower(strings.Join(strings.Fields(s), " "))
	return strings.TrimSpace(labelTail.ReplaceAllString(s, ""))
}

// labelBefore returns the text immediately left of the field on its row: blanks are skipped,
// then characters are collected leftwards until two blanks in a row, another field, or the row
// start.
func labelBefore(snap tn5250.Snapshot, f tn5250.FieldInfo) string {
	if snap.Cols <= 0 {
		return ""
	}
	rowStart := (f.Start / snap.Cols) * snap.Cols
	var rev []rune
	blanks := 0
	started := false
	for i := f.Start - 1; i >= rowStart && i >= 0; i-- {
		c := snap.Cells[i]
		if c.InField && !c.Attribute {
			break
		}
		if c.Attribute || c.Ch == ' ' || c.Ch == 0 {
			if started {
				blanks++
				if blanks >= 2 {
					break
				}
				rev = append(rev, ' ')
			}
			continue
		}
		started = true
		blanks = 0
		rev = append(rev, c.Ch)
	}
	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}
	return strings.TrimSpace(string(rev))
}

// fieldText is the visible text of a field, trailing blanks trimmed, "" for a hidden one.
func fieldText(snap tn5250.Snapshot, f tn5250.FieldInfo) string {
	if f.Nondisplay {
		return ""
	}
	var b strings.Builder
	for i := f.Start; i < f.Start+f.Length && i < len(snap.Cells); i++ {
		b.WriteRune(snap.Cells[i].Ch)
	}
	return strings.TrimRight(b.String(), " ")
}

// fieldViews lists the format table, 1-based.
func fieldViews(snap tn5250.Snapshot) []fieldView {
	out := make([]fieldView, len(snap.Fields))
	for i, f := range snap.Fields {
		fv := fieldView{
			Index: i + 1, Start: f.Start, Length: f.Length,
			Bypass: f.Bypass, Nondisplay: f.Nondisplay, MDT: f.MDT,
			Text: fieldText(snap, f),
		}
		if snap.Cols > 0 {
			fv.Row, fv.Col = f.Start/snap.Cols+1, f.Start%snap.Cols+1
		}
		if !f.Bypass {
			fv.Label = labelBefore(snap, f)
		}
		out[i] = fv
	}
	return out
}

// parseRows turns "4-20" into 1-based inclusive bounds clamped to the screen.
func parseRows(spec string, total int) (lo, hi int, err error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return 1, total, nil
	}
	lo, hi = 1, total
	a, b, isRange := strings.Cut(spec, "-")
	num := func(s string) (int, error) {
		n, err := strconv.Atoi(strings.TrimSpace(s))
		if err != nil || n < 1 {
			return 0, fmt.Errorf("bad rows %q: use \"5\", \"4-20\", \"4-\" or \"-10\" (1-based)", spec)
		}
		return n, nil
	}
	if !isRange {
		if lo, err = num(a); err != nil {
			return 0, 0, err
		}
		hi = lo
	} else {
		if strings.TrimSpace(a) != "" {
			if lo, err = num(a); err != nil {
				return 0, 0, err
			}
		}
		if strings.TrimSpace(b) != "" {
			if hi, err = num(b); err != nil {
				return 0, 0, err
			}
		}
	}
	if lo > hi {
		return 0, 0, fmt.Errorf("bad rows %q: start is after end", spec)
	}
	if lo > total {
		return 0, 0, fmt.Errorf("rows %q is below the last row (%d)", spec, total)
	}
	if hi > total {
		hi = total
	}
	return lo, hi, nil
}

// screenText is the rows lo..hi (1-based) trimmed of trailing blanks, joined by newlines.
func screenText(snap tn5250.Snapshot, lo, hi int) string {
	rows := make([]string, 0, hi-lo+1)
	for r := lo; r <= hi; r++ {
		rows = append(rows, strings.TrimRight(snap.RowText(r-1), " "))
	}
	return strings.Join(rows, "\n")
}

func signature(text string) string {
	sum := sha1.Sum([]byte(text))
	return hex.EncodeToString(sum[:])[:8]
}

// formatScreen builds the tool output.
func formatScreen(snap tn5250.Snapshot, oia tn5250.OIA, o ScreenOpts) (ScreenOut, error) {
	lo, hi, err := parseRows(o.Rows, snap.Rows)
	if err != nil {
		return ScreenOut{}, err
	}
	full := screenText(snap, 1, snap.Rows)
	out := ScreenOut{
		Cursor:    Pos{Row: snap.Cursor.Row + 1, Col: snap.Cursor.Col + 1},
		Seq:       snap.Seq,
		Signature: signature(full),
		Size:      fmt.Sprintf("%dx%d", snap.Rows, snap.Cols),
		OIA: OIAOut{
			Connected: oia.Connected, Inhibited: oia.InputInhibited, Insert: oia.Insert,
			MessageWaiting: oia.MessageWaiting, Message: oia.Message, Device: oia.DeviceName,
			QueuedKeys: oia.QueuedKeys,
		},
	}
	if out.OIA.Inhibited == "" {
		out.OIA.Inhibited = "none"
	}
	filtered := lo != 1 || hi != snap.Rows
	if !o.FieldsOnly {
		if filtered {
			out.Text = screenText(snap, lo, hi)
			out.Rows = fmt.Sprintf("%d-%d", lo, hi)
		} else {
			out.Text = full
		}
	}
	for _, fv := range fieldViews(snap) {
		if fv.Row < lo || fv.Row > hi {
			continue
		}
		fo := FieldOut{
			Index: fv.Index, Row: fv.Row, Col: fv.Col, Length: fv.Length, Label: fv.Label,
			Bypass: fv.Bypass, Nondisplay: fv.Nondisplay, MDT: fv.MDT,
		}
		// A protected field's text is in Text already; keep it only when Text is left out.
		if !fv.Bypass || o.FieldsOnly {
			fo.Text = fv.Text
		}
		out.Fields = append(out.Fields, fo)
	}
	return out, nil
}

// Hit is one result of find.
type Hit struct {
	Row  int    `json:"row"`
	Col  int    `json:"col"`
	Line string `json:"line" jsonschema:"the whole row, trailing blanks trimmed"`
}

func foldRunes(s string, caseSensitive bool) []rune {
	r := []rune(s)
	if !caseSensitive {
		for i, c := range r {
			r[i] = unicode.ToLower(c)
		}
	}
	return r
}

// findText returns every occurrence of text on the screen (1-based positions).
func findText(snap tn5250.Snapshot, text string, caseSensitive bool) []Hit {
	hits := []Hit{}
	if text == "" {
		return hits
	}
	needle := foldRunes(text, caseSensitive)
	for r := 0; r < snap.Rows; r++ {
		row := snap.RowText(r)
		hay := foldRunes(row, caseSensitive)
		trimmed := strings.TrimRight(row, " ")
		for c := 0; c+len(needle) <= len(hay); c++ {
			if runesEqual(hay[c:c+len(needle)], needle) {
				hits = append(hits, Hit{Row: r + 1, Col: c + 1, Line: trimmed})
			}
		}
	}
	return hits
}

func runesEqual(a, b []rune) bool {
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// containsFold reports whether the rows lo..hi (1-based) of the screen contain text,
// case-insensitively.
func containsFold(snap tn5250.Snapshot, text string, lo, hi int) bool {
	want := foldRunes(text, false)
	for r := lo; r <= hi; r++ {
		hay := foldRunes(snap.RowText(r-1), false)
		for c := 0; c+len(want) <= len(hay); c++ {
			if runesEqual(hay[c:c+len(want)], want) {
				return true
			}
		}
	}
	return false
}

// dumpScreen is the debugging view: the rows, then one line of attribute hex per row (two
// digits per cell, the 5250 attribute byte in effect there).
func dumpScreen(snap tn5250.Snapshot) string {
	var b strings.Builder
	b.WriteString(screenText(snap, 1, snap.Rows))
	b.WriteString("\n--attr--\n")
	for r := 0; r < snap.Rows; r++ {
		for _, c := range snap.Cells[r*snap.Cols : (r+1)*snap.Cols] {
			fmt.Fprintf(&b, "%02x", c.Attr)
		}
		if r < snap.Rows-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// fieldRef names a field by 1-based index, by 1-based position, or by label.
type fieldRef struct {
	Index    int
	Row, Col int
	Label    string
}

// fieldTable describes the input fields for error messages.
func fieldTable(views []fieldView) string {
	var b strings.Builder
	n := 0
	for _, v := range views {
		if v.Bypass {
			continue
		}
		n++
		if n > 1 {
			b.WriteString("; ")
		}
		fmt.Fprintf(&b, "%d r%dc%d len%d", v.Index, v.Row, v.Col, v.Length)
		if v.Label != "" {
			fmt.Fprintf(&b, " %q", v.Label)
		}
		if v.Nondisplay {
			b.WriteString(" hidden")
		}
	}
	if n == 0 {
		return "there are no input fields on this screen"
	}
	return "input fields: " + b.String()
}

// resolveField picks the field a ref names. A protected field is an error that lists the input
// fields; so is a missing or ambiguous label.
func resolveField(views []fieldView, ref fieldRef) (fieldView, error) {
	switch {
	case ref.Index > 0:
		if ref.Index > len(views) {
			return fieldView{}, fmt.Errorf("no field %d (the screen has %d fields). %s", ref.Index, len(views), fieldTable(views))
		}
		return checkInput(views, views[ref.Index-1])
	case ref.Row > 0 && ref.Col > 0:
		for _, v := range views {
			if v.Row == ref.Row && v.Col <= ref.Col && ref.Col < v.Col+v.Length {
				return checkInput(views, v)
			}
		}
		return fieldView{}, fmt.Errorf("no field at row %d col %d. %s", ref.Row, ref.Col, fieldTable(views))
	case strings.TrimSpace(ref.Label) != "":
		want := normLabel(ref.Label)
		var exact, partial []fieldView
		for _, v := range views {
			if v.Bypass || v.Label == "" {
				continue
			}
			l := normLabel(v.Label)
			switch {
			case l == want:
				exact = append(exact, v)
			case strings.Contains(l, want):
				partial = append(partial, v)
			}
		}
		c := exact
		if len(c) == 0 {
			c = partial
		}
		switch len(c) {
		case 1:
			return c[0], nil
		case 0:
			return fieldView{}, fmt.Errorf("no input field is labelled %q. %s", ref.Label, fieldTable(views))
		}
		return fieldView{}, fmt.Errorf("label %q is ambiguous. %s", ref.Label, fieldTable(c))
	}
	return fieldView{}, fmt.Errorf("a field needs an index, a row and col, or a label")
}

func checkInput(views []fieldView, v fieldView) (fieldView, error) {
	if v.Bypass {
		return fieldView{}, fmt.Errorf("field %d (row %d col %d) is protected and cannot be typed into. %s", v.Index, v.Row, v.Col, fieldTable(views))
	}
	return v, nil
}

// fieldAtCursor is the field that holds the cursor.
func fieldAtCursor(snap tn5250.Snapshot, views []fieldView) (fieldView, bool) {
	pos := snap.Cursor.Row*snap.Cols + snap.Cursor.Col
	for _, v := range views {
		if pos >= v.Start && pos < v.Start+v.Length {
			return v, true
		}
	}
	return fieldView{}, false
}

// isCommandField reports whether the field is a command line ("===>" or "Selection or
// command").
func isCommandField(v fieldView) bool {
	l := strings.ToLower(v.Label)
	return strings.Contains(l, "===>") || strings.Contains(l, "command")
}
