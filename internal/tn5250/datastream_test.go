package tn5250

import (
	"strings"
	"testing"
)

type procRig struct {
	screen *screen
	state  *terminalState
	proc   *processor
	alarms int
}

func newRig(terminalType string) *procRig {
	r := &procRig{screen: newScreen(), state: newTerminalState()}
	r.proc = newProcessor(r.screen, r.state, terminalType, cp37, func() { r.alarms++ }, nil)
	return r
}

func (r *procRig) do(t testing.TB, hexStream string) []response {
	t.Helper()
	return r.proc.process(h2b(t, hexStream))
}

func (r *procRig) row(i int) string { return r.screen.rowText(i, cp37) }

func signOnPayload(t testing.TB) []byte {
	c := loadCapture(t, "pub400-signon-3477fc")
	var records [][]byte
	tn := newTelnetSession(telnetOptions{terminalType: "IBM-3477-FC"}, telnetCallbacks{
		send:   func(b, _ []byte) {},
		record: func(r []byte) { records = append(records, r) },
	})
	tn.feed(c.inbound(t))
	rec, err := ParseRecord(records[0])
	if err != nil {
		t.Fatal(err)
	}
	return rec.Data
}

func TestProcessorSignOnRecord(t *testing.T) {
	r := newRig("IBM-3477-FC")
	responses := r.proc.process(signOnPayload(t))
	s := r.screen
	if s.rows != 24 || s.cols != 80 {
		t.Fatalf("size %dx%d", s.rows, s.cols)
	}
	if !strings.Contains(r.row(0), "Welcome to PUB400.COM") {
		t.Errorf("row 0 = %q", r.row(0))
	}
	if !strings.HasPrefix(r.row(4), "Your user name:") || !strings.HasPrefix(r.row(5), "Password (max. 128):") {
		t.Errorf("rows 4,5 = %q %q", r.row(4), r.row(5))
	}
	if !strings.Contains(r.row(23), "(C) COPYRIGHT IBM CORP.") {
		t.Errorf("row 23 = %q", r.row(23))
	}
	if len(s.fields) != 2 {
		t.Fatalf("fields = %d", len(s.fields))
	}
	user, pw := s.fields[0], s.fields[1]
	if user.start != s.addr(4, 24) || user.length != 10 || !user.monocase() || user.attr != 0x24 {
		t.Errorf("user field %+v", user)
	}
	if pw.start != s.addr(5, 24) || pw.length != 128 || pw.attr != 0x27 || !pw.nondisplay() {
		t.Errorf("password field %+v", pw)
	}
	if user.bypass() || pw.bypass() {
		t.Error("fields must accept input")
	}
	if r.state.inputInhibited != inhibitNone || r.state.pendingRead != readMDT {
		t.Errorf("state: %s %v", r.state.inputInhibited, r.state.pendingRead)
	}
	if len(responses) != 0 {
		t.Errorf("responses = %d", len(responses))
	}
	if s.cursor != user.start {
		t.Errorf("cursor %d, want %d", s.cursor, user.start)
	}
}

func TestProcessorClearUnitAlternate(t *testing.T) {
	r := newRig("IBM-3477-FC")
	for _, c := range []struct {
		in         string
		rows, cols int
	}{{"042000", 27, 132}, {"042080", 24, 80}, {"042031", 27, 132}, {"0440", 24, 80}} {
		r.do(t, c.in)
		if r.screen.rows != c.rows || r.screen.cols != c.cols {
			t.Errorf("%s -> %dx%d", c.in, r.screen.rows, r.screen.cols)
		}
	}
}

func TestProcessorWTDWritesSBARAIC(t *testing.T) {
	r := newRig("IBM-3477-FC")
	// WTD cc1=00 cc2=08; SBA 2,5 "AB"; RA to 2,10 fill '-'; IC 3,1
	r.do(t, "04110008"+"110205"+e("AB")+"02020a"+e("-")+"130301")
	if got := r.row(1)[4:10]; got != "AB----" {
		t.Errorf("row = %q", got)
	}
	if r.screen.cursor != r.screen.addr(2, 0) {
		t.Errorf("cursor %d", r.screen.cursor)
	}
}

func TestProcessorTransparentDataEraseAndWEA(t *testing.T) {
	r := newRig("IBM-3477-FC")
	// TD: four data bytes (a, b, then X'11 01' which are data here, not an SBA); WEA's two
	// operand bytes are skipped.
	r.do(t, "04110008"+"110101"+"100004"+e("ab")+"1101"+"1200ff"+sba(3, 1)+e("z"))
	if !strings.HasPrefix(r.row(0), "ab") || r.screen.buffer[2] != 0x11 || r.screen.buffer[3] != 0x01 {
		t.Errorf("TD: row = %q buf = %x", r.row(0), r.screen.buffer[:4])
	}
	if !strings.HasPrefix(r.row(2), "z") {
		t.Errorf("after TD/WEA: row 2 = %q", r.row(2))
	}
	// EA erases from the write address to 1,5 (length 2: one attribute type byte follows).
	r.do(t, "04110008"+sba(1, 1)+e("abcdefgh")+sba(1, 1)+"03"+"0105"+"02"+"ff")
	if got := r.row(0)[:8]; got != "     fgh" {
		t.Errorf("EA: row = %q", got)
	}
}

func TestProcessorStartOfFieldCreatesFields(t *testing.T) {
	r := newRig("IBM-3477-FC")
	// SF ffw=4000 attr=20 len=5 "abc" then attr 20; second SF with FCW and MDT set.
	r.do(t, "04110008"+"110101"+"1d4000200005"+e("abc")+"20"+"110301"+"1d4800820027000a")
	if len(r.screen.fields) != 2 {
		t.Fatalf("fields = %d", len(r.screen.fields))
	}
	f1, f2 := r.screen.fields[0], r.screen.fields[1]
	if f1.start != r.screen.addr(0, 1) || f1.length != 5 || f1.mdt {
		t.Errorf("f1 = %+v", f1)
	}
	if got := cp37.DecodeBytes(r.screen.fieldBytes(f1)); got != "abc  " {
		t.Errorf("f1 text %q", got)
	}
	if len(f2.fcws) != 1 || f2.fcws[0] != 0x8200 || f2.attr != 0x27 || f2.length != 10 || !f2.mdt {
		t.Errorf("f2 = %+v", f2)
	}
}

func TestProcessorAttributeOnlySFMakesNoField(t *testing.T) {
	r := newRig("IBM-3477-FC")
	// An SF whose first byte is already the attribute (no FFW) is just an attribute cell.
	r.do(t, "04110008"+"110101"+"1d"+"22"+"0004"+e("x"))
	if len(r.screen.fields) != 0 || r.screen.buffer[0] != 0x22 {
		t.Errorf("fields %d buf0 %#x", len(r.screen.fields), r.screen.buffer[0])
	}
}

func TestProcessorCC1AndCC2(t *testing.T) {
	r := newRig("IBM-3477-FC")
	r.do(t, "04110008"+"110101"+"1d48002000"+"03"+e("xyz"))
	f := r.screen.fields[0]
	if !f.mdt {
		t.Fatal("MDT should be set from the FFW")
	}
	r.do(t, "0411a006") // CC1 = reset MDT + null all, CC2 = alarm + message light
	if r.screen.fields[0].mdt {
		t.Error("MDT not reset")
	}
	for _, b := range r.screen.fieldBytes(r.screen.fields[0]) {
		if b != 0 {
			t.Fatalf("field not nulled: %x", r.screen.fieldBytes(r.screen.fields[0]))
		}
	}
	if r.state.inputInhibited != inhibitSystem || r.alarms != 1 || !r.state.messageWaiting {
		t.Errorf("state %s alarms %d mw %v", r.state.inputInhibited, r.alarms, r.state.messageWaiting)
	}
	r.do(t, "04110009")
	if r.state.inputInhibited != inhibitNone || r.state.messageWaiting {
		t.Errorf("after unlock: %s mw %v", r.state.inputInhibited, r.state.messageWaiting)
	}
}

func TestProcessorCC1Variants(t *testing.T) {
	// 0x40 reset MDT only; 0x80 null MDT fields; 0xc0 reset + null modified fields.
	for _, c := range []struct {
		cc1       string
		wantMDT   bool
		wantNulls bool
	}{{"40", false, false}, {"80", true, true}, {"c0", false, true}, {"20", true, false}} {
		r := newRig("IBM-3477-FC")
		r.do(t, "04110008"+"110101"+"1d48002000"+"03"+e("xyz"))
		r.do(t, "0411"+c.cc1+"00")
		f := r.screen.fields[0]
		nulled := r.screen.fieldBytes(f)[0] == 0
		if f.mdt != c.wantMDT || nulled != c.wantNulls {
			t.Errorf("cc1 %s: mdt %v nulled %v", c.cc1, f.mdt, nulled)
		}
		if r.state.inputInhibited != inhibitSystem {
			t.Errorf("cc1 %s should lock the keyboard", c.cc1)
		}
	}
}

func TestProcessorReadCommandHomesCursorUnlessICGiven(t *testing.T) {
	r := newRig("IBM-3477-FC")
	r.do(t, "04110008"+"110201"+"1d4000200005"+"110301"+"1d4000200005"+"04520000")
	if r.screen.cursor != r.screen.addr(1, 1) {
		t.Fatalf("cursor %d", r.screen.cursor)
	}
	r.screen.cursor = r.screen.addr(2, 3) // user moved into the second field
	r.do(t, "04110008"+"110501"+e("msg")+"04520000")
	if r.screen.cursor != r.screen.addr(1, 1) {
		t.Errorf("repaint without IC: cursor %d", r.screen.cursor)
	}
	r.do(t, "04110008"+"130203"+"04520000")
	if r.screen.cursor != r.screen.addr(1, 2) {
		t.Errorf("IC then read: cursor %d", r.screen.cursor)
	}
}

func TestProcessorReadKinds(t *testing.T) {
	for cmd, want := range map[string]readKind{"42": readInput, "52": readMDT, "82": readMDTAlt} {
		r := newRig("IBM-3477-FC")
		r.do(t, "04110000"+"04"+cmd+"0000")
		if r.state.pendingRead != want || r.state.inputInhibited != inhibitNone {
			t.Errorf("cmd %s: pending %v inhibited %s", cmd, r.state.pendingRead, r.state.inputInhibited)
		}
	}
}

func TestProcessorRepaintRestoresErrorRow(t *testing.T) {
	r := newRig("IBM-3477-FC")
	r.do(t, "04110008"+"111801"+e("Footer")+"130101")
	r.do(t, "0421"+e("Not allowed"))
	if r.state.savedErrorRow == nil {
		t.Fatal("error row not saved")
	}
	r.do(t, "04110008"+"110101"+e("New screen")+"04520000")
	if r.state.savedErrorRow != nil || r.state.savedCursor >= 0 {
		t.Error("saved error state should be gone after a repaint")
	}
	if got := strings.TrimSpace(r.row(23)); got != "Footer" {
		t.Errorf("row 23 = %q", got)
	}
}

func TestProcessorRoll(t *testing.T) {
	r := newRig("IBM-3477-FC")
	r.do(t, "04110008"+"110501"+e("top")+"110601"+e("next"))
	r.do(t, "0423"+"01"+"05"+"06")
	if got := strings.TrimRight(r.row(4), " "); got != "next" {
		t.Errorf("row 4 = %q", got)
	}
	if strings.TrimSpace(r.row(5)) != "" {
		t.Errorf("row 5 = %q", r.row(5))
	}
	// Roll down: bit 0x80 set.
	r.do(t, "0423"+"81"+"05"+"06")
	if got := strings.TrimRight(r.row(5), " "); got != "next" {
		t.Errorf("after roll down row 5 = %q", got)
	}
}

func TestProcessorWriteErrorCode(t *testing.T) {
	r := newRig("IBM-3477-FC")
	r.do(t, "04110008"+"130a05")
	r.do(t, "0421"+e("Password incorrect"))
	if got := strings.TrimSpace(r.row(23)); got != "Password incorrect" {
		t.Errorf("row 23 = %q", got)
	}
	if r.state.errorMessage != "Password incorrect" || r.state.inputInhibited != inhibitError {
		t.Errorf("state: %q %s", r.state.errorMessage, r.state.inputInhibited)
	}
	if r.state.savedCursor != r.screen.addr(9, 4) {
		t.Errorf("savedCursor = %d", r.state.savedCursor)
	}
}

func TestProcessorQueryReplyIsPlain(t *testing.T) {
	for _, c := range []struct {
		term, machine string
		b50           byte
	}{{"IBM-3477-FC", "3477", 0x31}, {"IBM-3179-2", "3179", 0x11}} {
		r := newRig(c.term)
		resp := r.do(t, "04f30005d97000")
		if len(resp) != 1 || resp[0].opcode != OpNoop {
			t.Fatalf("responses = %+v", resp)
		}
		reply := resp[0].data
		if len(reply) != 61 {
			t.Fatalf("%s: reply length %d", c.term, len(reply))
		}
		if got := b2h(reply[:7]); got != "000088003ad970" {
			t.Errorf("header %s", got)
		}
		if got := cp37.DecodeBytes(reply[30:34]); got != c.machine {
			t.Errorf("machine %q", got)
		}
		if reply[50] != c.b50 {
			t.Errorf("byte 50 = %#x", reply[50])
		}
		// A plain display: the enhanced interface bytes stay zero.
		if reply[53] != 0 || reply[54] != 0 {
			t.Errorf("enhanced flags set: %#x %#x", reply[53], reply[54])
		}
	}
}

func TestProcessorWSFWalksEveryStructuredField(t *testing.T) {
	r := newRig("IBM-3477-FC")
	r.do(t, "04110008"+sba(2, 2)+e("hello"))
	// A Query followed by an unknown structured field whose data holds X'04 40' (which would
	// read as ESC CLEAR UNIT if scanned byte by byte).
	resp := r.do(t, "04f3"+"0005"+"d97000"+"0008"+"d97f"+"0440"+"0000")
	if len(resp) != 1 {
		t.Fatalf("responses = %d, want one Query reply", len(resp))
	}
	if strings.TrimSpace(r.row(1)) != "hello" {
		t.Errorf("screen was cleared: %q", r.row(1))
	}
	// A truncated structured field must not panic or loop.
	r.do(t, "04f3"+"00ff"+"d970")
}

func TestProcessorWDSFIsSkipped(t *testing.T) {
	r := newRig("IBM-3477-FC")
	// A Create Window structured field (no enhanced interface here): length and body skipped,
	// the data after it still lands.
	sf := "d951" + "00" + "0000" + "05" + "14"
	r.do(t, "04110008"+"11071e"+"15"+hx2(len(sf)/2+2)+sf+"110802"+e("after"))
	if len(r.screen.fields) != 0 {
		t.Error("WDSF created fields")
	}
	if !strings.Contains(r.row(7), "after") || strings.TrimSpace(r.row(6)) != "" {
		t.Errorf("rows: %q / %q", r.row(6), r.row(7))
	}
}

func TestProcessorReadImmediate(t *testing.T) {
	r := newRig("IBM-3477-FC")
	r.do(t, "04110008"+"110101"+"1d400020000"+"3"+e("ab")+"130102")
	resp := r.do(t, "0472")
	if len(resp) != 1 || resp[0].opcode != OpPutGet {
		t.Fatalf("responses = %+v", resp)
	}
	if got := b2h(resp[0].data); got != "010200"+e("ab")+"40" {
		t.Errorf("data = %s", got)
	}
}

func TestProcessorReadMDTSendsOnlyChangedFields(t *testing.T) {
	r := newRig("IBM-3477-FC")
	r.do(t, "04110008"+"110101"+"1d4000200005"+"110201"+"1d4800200005"+e("hi"))
	second := r.screen.fields[1]
	copy(r.screen.buffer[second.start:], cp37.EncodeString("hi"))
	r.screen.cursor = second.start + 2
	data := r.proc.readResponse(readMDT, 0xf1)
	// Field data starts one position after the attribute written at row 2 col 1.
	if got := b2h(data); got != "0204f1"+"110202"+e("hi") {
		t.Errorf("data = %s", got)
	}
	// The alternate format keeps blanks but still strips trailing nulls.
	copy(r.screen.buffer[second.start:], cp37.EncodeString("hi "))
	alt := r.proc.readResponse(readMDTAlt, 0xf1)
	if got := b2h(alt); got != "0204f1"+"110202"+e("hi ") {
		t.Errorf("alt data = %s", got)
	}
}

func TestProcessorReadInputFieldsFlatFormat(t *testing.T) {
	r := newRig("IBM-3477-FC")
	r.do(t, "04110008"+"110201"+"1d4000200004"+"110401"+"1d6000200003"+"130202"+"0442"+"0000")
	copy(r.screen.buffer[r.screen.fields[0].start:], cp37.EncodeString("ab"))
	// The bypass field is skipped, nulls come out as blanks, no SBA orders.
	if got := b2h(r.proc.readResponse(readInput, 0x33)); got != "0202"+"33"+e("ab")+"4040" {
		t.Errorf("flat = %s", got)
	}
	if got := b2h(r.proc.aidOnly(0x33)); got != "020233" {
		t.Errorf("aid only = %s", got)
	}
}

func TestProcessorSecretRanges(t *testing.T) {
	r := newRig("IBM-3477-FC")
	r.do(t, "04110008"+sba(6, 10)+"1d4000200010"+sba(7, 10)+"1d4800270010"+"0452"+"0000")
	copy(r.screen.buffer[r.screen.fields[0].start:], cp37.EncodeString("alex"))
	pw := r.screen.fields[1]
	copy(r.screen.buffer[pw.start:], cp37.EncodeString("s3cret"))
	data := r.proc.readResponse(readMDT, 0xf1)
	ranges := r.proc.takeSecretRanges()
	if len(ranges) != 1 || string(data[ranges[0][0]:ranges[0][1]]) != string(cp37.EncodeString("s3cret")) {
		t.Fatalf("ranges %v of %x", ranges, data)
	}
	if len(r.proc.takeSecretRanges()) != 0 {
		t.Error("ranges are handed over once")
	}
	// Read Immediate answers with the ranges attached to the response itself.
	resp := r.do(t, "0472")
	if len(resp) != 1 || len(resp[0].secret) != 1 {
		t.Fatalf("immediate response secret = %v", resp[0].secret)
	}
	// Read Screen carries the password cells too, so they are marked for the trace.
	screenResp := r.do(t, "0462")
	if len(screenResp) != 1 || len(screenResp[0].secret) != 1 ||
		string(screenResp[0].data[screenResp[0].secret[0][0]:screenResp[0].secret[0][0]+6]) != string(cp37.EncodeString("s3cret")) {
		t.Fatalf("read screen secret = %v", screenResp[0].secret)
	}
	ext := r.do(t, "0464")
	if len(ext) != 1 || len(ext[0].secret) != 1 {
		t.Fatalf("read screen ext secret = %v", ext[0].secret)
	}
	if got := string(ext[0].data[ext[0].secret[0][0] : ext[0].secret[0][0]+6]); got != string(cp37.EncodeString("s3cret")) {
		t.Errorf("ext secret range points at %x", got)
	}
	save := r.do(t, "0402")
	if len(save) != 1 || len(save[0].secret) == 0 {
		t.Fatalf("save screen secret = %v", save[0].secret)
	}
}

func TestProcessorReadScreenToPrintAnswersLikeReadScreen(t *testing.T) {
	r := newRig("IBM-3477-FC")
	r.do(t, "04110008"+"110305"+e("Name:")+"04520000")
	plain := r.do(t, "0462")
	toPrint := r.do(t, "0466")
	if len(toPrint) != 1 || toPrint[0].opcode != OpNoop || b2h(toPrint[0].data) != b2h(plain[0].data) {
		t.Fatal("Read Screen (to Print) differs from Read Screen")
	}
	if !strings.Contains(b2h(toPrint[0].data), e("Name:")) {
		t.Error("image lacks the text")
	}
	ext := r.do(t, "046e")
	if b2h(ext[0].data) != b2h(r.do(t, "0464")[0].data) {
		t.Error("Read Screen Extended (to Print) differs")
	}
	// Extended: 24 rows, each ending X'FF', trailing nulls trimmed.
	if n := strings.Count(b2h(ext[0].data), "ff"); n < 24 {
		t.Errorf("ff count %d", n)
	}
}

func TestProcessorUnknownCommandsAreSkipped(t *testing.T) {
	r := newRig("IBM-3477-FC")
	// Copy To Printer (0x16) with junk, then a valid write.
	r.do(t, "0416"+"aabbcc"+"04110008"+"110101"+e("ok"))
	if !strings.HasPrefix(r.row(0), "ok") {
		t.Errorf("row 0 = %q", r.row(0))
	}
	r.do(t, "04ee"+"0102")
	r.do(t, "04") // lone ESC
}

func TestProcessorSOHSetsErrorRowAndCAKeys(t *testing.T) {
	r := newRig("IBM-3477-FC")
	soh := func(keys string) string { return "0107" + "00000018" + keys } // flags, reserved, resequence, error row 24, keys
	r.do(t, "04110008"+soh("000804")+"04520000")
	if r.state.caKeys != (1<<2)|(1<<11) {
		t.Errorf("caKeys = %#x", r.state.caKeys)
	}
	r.do(t, "04110008"+soh("010005")+"04520000")
	if r.state.caKeys != (1<<0)|(1<<2)|(1<<16) {
		t.Errorf("caKeys = %#x", r.state.caKeys)
	}
	// A short header and Clear Unit mean "every key returns data".
	r.do(t, "04110008"+"0104"+"00000018"+"04520000")
	if r.state.caKeys != 0 {
		t.Errorf("short header: caKeys = %#x", r.state.caKeys)
	}
	r.do(t, "04110008"+soh("000804")+"04520000")
	r.do(t, "0440")
	if r.state.caKeys != 0 {
		t.Errorf("clear unit: caKeys = %#x", r.state.caKeys)
	}
	// Error row from the header.
	r.do(t, "04110008"+"0107"+"00000016"+"000000")
	if r.screen.errorRow != 21 {
		t.Errorf("errorRow = %d", r.screen.errorRow)
	}
}

func TestProcessorTraceHook(t *testing.T) {
	var lines []string
	s, st := newScreen(), newTerminalState()
	p := newProcessor(s, st, "IBM-3179-2", cp37, nil, func(l string) { lines = append(lines, l) })
	p.process(h2b(t, "04110008"+"110101"+e("x")))
	if len(lines) < 2 || !strings.HasPrefix(lines[0], "cmd 11") || !strings.Contains(lines[1], "order 11") {
		t.Errorf("trace = %q", lines)
	}
}
