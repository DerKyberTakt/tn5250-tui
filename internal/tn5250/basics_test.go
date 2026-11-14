package tn5250

import (
	"errors"
	"strings"
	"testing"
)

func TestGDSParsesSignOnRecord(t *testing.T) {
	c := loadCapture(t, "pub400-signon-3477fc")
	var records [][]byte
	tn := newTelnetSession(telnetOptions{terminalType: "IBM-3477-FC"}, telnetCallbacks{
		send:   func(b, _ []byte) {},
		record: func(r []byte) { records = append(records, r) },
	})
	tn.feed(c.inbound(t))
	if len(records) != 1 {
		t.Fatalf("records = %d", len(records))
	}
	rec, err := ParseRecord(records[0])
	if err != nil {
		t.Fatal(err)
	}
	if rec.Length != 0x032d || rec.Opcode != OpPutGet || rec.Flags1 != 0 {
		t.Fatalf("header = %+v", rec)
	}
	if got := b2h(rec.Data[:4]); got != "04400411" {
		t.Fatalf("data starts %s", got)
	}
}

func TestGDSBuildRecord(t *testing.T) {
	rec := BuildRecord(OpPutGet, 0, h2b(t, "0101f1"))
	if got := b2h(rec); got != "000d12a00000040000030101f1" {
		t.Fatalf("built %s", got)
	}
	parsed, err := ParseRecord(rec)
	if err != nil || parsed.Opcode != OpPutGet || b2h(parsed.Data) != "0101f1" {
		t.Fatalf("parsed %+v %v", parsed, err)
	}
}

func TestGDSRejectsMalformedRecords(t *testing.T) {
	for _, in := range []string{"0005", "000a12a1000004000003", "000a12a00000030000"} {
		if _, err := ParseRecord(h2b(t, in)); !errors.Is(err, ErrGDS) {
			t.Errorf("ParseRecord(%s) error = %v, want ErrGDS", in, err)
		}
	}
	// A variable header longer than the record leaves no data instead of panicking.
	rec, err := ParseRecord(h2b(t, "000a12a0000020000003"))
	if err != nil || len(rec.Data) != 0 {
		t.Fatalf("oversized header: %+v %v", rec, err)
	}
}

func TestDecodeAttr(t *testing.T) {
	type want struct {
		color                                          string
		reverse, underline, blink, columns, nondisplay bool
	}
	cases := map[byte]want{
		0x20: {color: "green"},
		0x21: {color: "green", reverse: true},
		0x22: {color: "white"},
		0x23: {color: "white", reverse: true},
		0x24: {color: "green", underline: true},
		0x25: {color: "green", underline: true, reverse: true},
		0x26: {color: "white", underline: true},
		0x27: {color: "white", underline: true, reverse: true, nondisplay: true},
		0x28: {color: "red"},
		0x29: {color: "red", reverse: true},
		0x2a: {color: "red", blink: true},
		0x2b: {color: "red", reverse: true, blink: true},
		0x2c: {color: "red", underline: true},
		0x2d: {color: "red", underline: true, reverse: true},
		0x2e: {color: "red", underline: true, blink: true},
		0x2f: {color: "red", underline: true, reverse: true, blink: true, nondisplay: true},
		0x30: {color: "turquoise", columns: true},
		0x31: {color: "turquoise", reverse: true, columns: true},
		0x32: {color: "yellow", columns: true},
		0x33: {color: "yellow", reverse: true, columns: true},
		0x34: {color: "turquoise", underline: true, columns: true},
		0x35: {color: "turquoise", underline: true, reverse: true, columns: true},
		0x36: {color: "yellow", underline: true, columns: true},
		0x37: {color: "yellow", underline: true, reverse: true, columns: true, nondisplay: true},
		0x38: {color: "pink"},
		0x39: {color: "pink", reverse: true},
		0x3a: {color: "blue"},
		0x3b: {color: "blue", reverse: true},
		0x3c: {color: "pink", underline: true},
		0x3d: {color: "pink", underline: true, reverse: true},
		0x3e: {color: "blue", underline: true},
		0x3f: {color: "blue", underline: true, reverse: true, nondisplay: true},
	}
	for b, w := range cases {
		got := DecodeAttr(b)
		exp := Attr{Color: w.color, Reverse: w.reverse, Underline: w.underline, Blink: w.blink, ColumnSeparators: w.columns, Nondisplay: w.nondisplay}
		if got != exp {
			t.Errorf("DecodeAttr(%#x) = %+v, want %+v", b, got, exp)
		}
	}
	// Bytes that are not attributes decode as the normal attribute.
	if DecodeAttr(0x1f) != DecodeAttr(0x20) || DecodeAttr(0x40) != DecodeAttr(0x20) {
		t.Error("non-attribute bytes should decode as green")
	}
	if !isAttribute(0x20) || !isAttribute(0x3f) || isAttribute(0x1f) || isAttribute(0x40) {
		t.Error("isAttribute must cover exactly 0x20..0x3f")
	}
}

func TestHostNLS(t *testing.T) {
	cases := []struct {
		ccsid int
		kbd   string
		want  hostNLS
		ok    bool
	}{
		{37, "", hostNLS{"697", "USB"}, true},
		{273, "", hostNLS{"697", "AGB"}, true},
		{500, "", hostNLS{"697", "INB"}, true},
		{1141, "", hostNLS{"695", "AGE"}, true},
		{1140, "", hostNLS{"695", "USE"}, true},
		{1148, "", hostNLS{"695", "INB"}, true},
		{500, " agb ", hostNLS{"697", "AGB"}, true},
		{12345, "", hostNLS{}, false},
		{12345, "AGB", hostNLS{}, false},
	}
	for _, c := range cases {
		got, ok := lookupNLS(c.ccsid, c.kbd)
		if ok != c.ok || got != c.want {
			t.Errorf("lookupNLS(%d,%q) = %v,%v want %v,%v", c.ccsid, c.kbd, got, ok, c.want, c.ok)
		}
	}
}

// telnetHarness records what a telnet layer sends and delivers.
type telnetHarness struct {
	tn      *telnetSession
	sent    []string
	records [][]byte
	env     []map[string]string
}

func newTelnetHarness(o telnetOptions) *telnetHarness {
	h := &telnetHarness{}
	h.tn = newTelnetSession(o, telnetCallbacks{
		send:        func(b, _ []byte) { h.sent = append(h.sent, b2h(b)) },
		record:      func(r []byte) { h.records = append(h.records, r) },
		environment: func(v map[string]string) { h.env = append(h.env, v) },
	})
	return h
}

func TestTelnetReplaysPUB400Negotiation(t *testing.T) {
	c := loadCapture(t, "pub400-signon-3477fc")
	h := newTelnetHarness(telnetOptions{terminalType: "IBM-3477-FC"})
	// Feed every "in" chunk in order and compare, event by event, what we send with what the
	// capture recorded.
	var want []string
	for _, ev := range c.Events {
		if ev.Dir == "in" {
			h.tn.feed(h2b(t, ev.Hex))
		} else {
			want = append(want, ev.Hex)
		}
	}
	if strings.Join(h.sent, "") != strings.Join(want, "") {
		t.Fatalf("sent %s\nwant %s", strings.Join(h.sent, "|"), strings.Join(want, "|"))
	}
	if len(h.sent) != len(want) {
		t.Fatalf("sent %d chunks, capture has %d", len(h.sent), len(want))
	}
	for i := range want {
		if h.sent[i] != want[i] {
			t.Errorf("chunk %d = %s, want %s", i, h.sent[i], want[i])
		}
	}
	if len(h.records) != 1 || len(h.records[0]) != 0x032d || b2h(h.records[0][:4]) != "032d12a0" {
		t.Fatalf("records = %d", len(h.records))
	}
	for _, opt := range []int{OptBinary, OptEOR, OptTerminalType, OptNewEnviron} {
		if !h.tn.agreed[opt] {
			t.Errorf("option %d not agreed", opt)
		}
	}
	if len(h.tn.agreed) != 4 {
		t.Errorf("agreed = %v", h.tn.agreed)
	}
}

func TestTelnetSplitRecordsAndEscapedIAC(t *testing.T) {
	h := newTelnetHarness(telnetOptions{terminalType: "IBM-3477-FC"})
	h.tn.feed(h2b(t, "0102ff"))
	h.tn.feed(h2b(t, "ff03ff"))
	if len(h.records) != 0 {
		t.Fatal("record delivered early")
	}
	h.tn.feed(h2b(t, "ef"))
	if len(h.records) != 1 || b2h(h.records[0]) != "0102ff03" {
		t.Fatalf("records = %x", h.records)
	}
}

func TestTelnetEscapesIACWhenSending(t *testing.T) {
	h := newTelnetHarness(telnetOptions{terminalType: "IBM-3477-FC"})
	h.tn.sendRecord(h2b(t, "01ff02"), nil)
	if len(h.sent) != 1 || h.sent[0] != "01ffff02ffef" {
		t.Fatalf("sent %v", h.sent)
	}
}

func TestTelnetTraceCopyIsFramedToo(t *testing.T) {
	var wire, trace []byte
	tn := newTelnetSession(telnetOptions{}, telnetCallbacks{send: func(b, f []byte) { wire, trace = b, f }, record: func([]byte) {}})
	tn.sendRecord(h2b(t, "01ff02"), h2b(t, "014002"))
	if b2h(wire) != "01ffff02ffef" || b2h(trace) != "014002ffef" {
		t.Fatalf("wire %x trace %x", wire, trace)
	}
}

func TestTelnetRefusesUnsupportedOptions(t *testing.T) {
	h := newTelnetHarness(telnetOptions{terminalType: "IBM-3477-FC"})
	h.tn.feed(h2b(t, "fffd05fffb20"))
	if len(h.sent) != 2 || h.sent[0] != "fffc05" || h.sent[1] != "fffe20" {
		t.Fatalf("sent %v", h.sent)
	}
}

func TestTelnetAnswersSupportedOptions(t *testing.T) {
	h := newTelnetHarness(telnetOptions{terminalType: "IBM-3477-FC"})
	// DO BINARY/EOR/TERMINAL-TYPE/NEW-ENVIRON -> WILL; WILL BINARY/EOR/SGA -> DO.
	h.tn.feed(h2b(t, "fffd00fffd19fffd18fffd27"+"fffb00fffb19fffb03"))
	want := []string{"fffb00", "fffb19", "fffb18", "fffb27", "fffd00", "fffd19", "fffd03"}
	if strings.Join(h.sent, ",") != strings.Join(want, ",") {
		t.Fatalf("sent %v", h.sent)
	}
	// An option split across two chunks waits for its third byte.
	h2 := newTelnetHarness(telnetOptions{})
	h2.tn.feed(h2b(t, "fffd"))
	h2.tn.feed(h2b(t, "00"))
	if len(h2.sent) != 1 || h2.sent[0] != "fffb00" {
		t.Fatalf("split negotiation: %v", h2.sent)
	}
}

func TestTelnetTerminalTypeSend(t *testing.T) {
	h := newTelnetHarness(telnetOptions{terminalType: "IBM-3179-2"})
	h.tn.feed(h2b(t, "fffa1801fff0"))
	want := "fffa1800" + b2h([]byte("IBM-3179-2")) + "fff0"
	if len(h.sent) != 1 || h.sent[0] != want {
		t.Fatalf("sent %v want %s", h.sent, want)
	}
}

func envReply(t *testing.T, o telnetOptions) string {
	t.Helper()
	h := newTelnetHarness(o)
	h.tn.feed(h2b(t, "fffa2701fff0"))
	if len(h.sent) != 1 {
		t.Fatalf("sent %v", h.sent)
	}
	return string(h2b(t, h.sent[0]))
}

func TestTelnetNewEnvironDeviceName(t *testing.T) {
	text := envReply(t, telnetOptions{terminalType: "IBM-3179-2", deviceName: "mydev", codePage: 37})
	for _, want := range []string{"DEVNAME\x01MYDEV", "KBDTYPE\x01USB", "CODEPAGE\x0137", "CHARSET\x01697"} {
		if !strings.Contains(text, want) {
			t.Errorf("reply lacks %q: %q", want, text)
		}
	}
	if !strings.HasPrefix(text, "\xff\xfa\x27\x00\x03DEVNAME") || !strings.HasSuffix(text, "\xff\xf0") {
		t.Errorf("framing: %q", text)
	}
	// Without a device name the host assigns one: the reply is empty.
	if got := envReply(t, telnetOptions{terminalType: "IBM-3179-2", codePage: 37}); got != "\xff\xfa\x27\x00\xff\xf0" {
		t.Errorf("no device name: %q", got)
	}
}

func TestTelnetNewEnvironFollowsCodePage(t *testing.T) {
	de := envReply(t, telnetOptions{terminalType: "IBM-3179-2", deviceName: "mydev", codePage: 1141})
	for _, want := range []string{"KBDTYPE\x01AGE", "CODEPAGE\x011141", "CHARSET\x01695"} {
		if !strings.Contains(de, want) {
			t.Errorf("1141 reply lacks %q", want)
		}
	}
	uk := envReply(t, telnetOptions{terminalType: "IBM-3179-2", deviceName: "mydev", codePage: 500, keyboardType: "ukb"})
	if !strings.Contains(uk, "KBDTYPE\x01UKB") || !strings.Contains(uk, "CHARSET\x01697") {
		t.Errorf("override reply %q", uk)
	}
	unknown := envReply(t, telnetOptions{terminalType: "IBM-3179-2", deviceName: "mydev", codePage: 9999})
	if !strings.Contains(unknown, "DEVNAME\x01MYDEV") || strings.Contains(unknown, "KBDTYPE") || strings.Contains(unknown, "CHARSET") {
		t.Errorf("unknown code page reply %q", unknown)
	}
}

func TestParseEnvironment(t *testing.T) {
	vars := parseEnvironment([]byte("\x03DEVNAME\x01QPADEV0001\x00USER\x01ALEX\x03ESC\x01A\x02\x03B"))
	if vars["DEVNAME"] != "QPADEV0001" || vars["USER"] != "ALEX" || vars["ESC"] != "A\x03B" {
		t.Fatalf("vars = %q", vars)
	}
}

func TestTelnetEnvironmentIsAndInfoReachTheCallback(t *testing.T) {
	h := newTelnetHarness(telnetOptions{})
	h.tn.feed(append([]byte{0xff, 0xfa, 0x27, 0x00, 0x03}, append([]byte("DEVNAME\x01QPADEV0002"), 0xff, 0xf0)...))
	h.tn.feed(append([]byte{0xff, 0xfa, 0x27, 0x02, 0x03}, append([]byte("DEVNAME\x01QPADEV0003"), 0xff, 0xf0)...))
	if len(h.env) != 2 || h.env[0]["DEVNAME"] != "QPADEV0002" || h.env[1]["DEVNAME"] != "QPADEV0003" {
		t.Fatalf("env = %v", h.env)
	}
}

func TestTelnetIgnoresOtherCommandsAndResets(t *testing.T) {
	h := newTelnetHarness(telnetOptions{})
	h.tn.feed(h2b(t, "01"+"fff1"+"02"+"ffef"))
	if len(h.records) != 1 || b2h(h.records[0]) != "0102" {
		t.Fatalf("records %x", h.records)
	}
	h.tn.feed(h2b(t, "fffd00"))
	h.tn.reset()
	if len(h.tn.agreed) != 0 {
		t.Fatal("reset kept options")
	}
}

func TestScreenRoll(t *testing.T) {
	s := newScreen()
	fill := func(row int, text string) { copy(s.buffer[row*s.cols:], cp37.EncodeString(text)) }
	fill(5, "five")
	fill(6, "six")
	fill(7, "seven")
	s.roll(1, 5, 7)
	if got := strings.TrimRight(s.rowText(5, cp37), " "); got != "six" {
		t.Errorf("row 5 = %q", got)
	}
	if got := strings.TrimRight(s.rowText(6, cp37), " "); got != "seven" {
		t.Errorf("row 6 = %q", got)
	}
	if strings.TrimSpace(s.rowText(7, cp37)) != "" {
		t.Error("vacated row not blank")
	}
	s.roll(-2, 5, 7)
	if strings.TrimSpace(s.rowText(5, cp37)) != "" || strings.TrimSpace(s.rowText(6, cp37)) != "" {
		t.Error("rows 5-6 should be blank")
	}
	if got := strings.TrimRight(s.rowText(7, cp37), " "); got != "six" {
		t.Errorf("row 7 = %q", got)
	}
	// Out-of-range regions from a bad stream do not panic.
	s.roll(1, -1, 99)
	s.roll(1, 30, 40)
}

func TestScreenAttributeAt(t *testing.T) {
	s := newScreen()
	if s.attributeAt(100) != 0x20 {
		t.Error("default attribute")
	}
	s.buffer[50] = 0x22
	s.buffer[200] = 0x28
	for addr, want := range map[int]byte{49: 0x20, 50: 0x22, 199: 0x22, 200: 0x28} {
		if got := s.attributeAt(addr); got != want {
			t.Errorf("attributeAt(%d) = %#x want %#x", addr, got, want)
		}
	}
}

func TestScreenFieldsStayOrderedAndRedefinitionsReplace(t *testing.T) {
	s := newScreen()
	s.addField(newField(300, 10, 0x4000, nil, 0x20))
	s.addField(newField(100, 5, 0x4000, nil, 0x20))
	s.addField(newField(300, 12, 0x4800, nil, 0x24))
	if len(s.fields) != 2 || s.fields[0].start != 100 || s.fields[1].start != 300 || s.fields[1].length != 12 {
		t.Fatalf("fields = %+v %+v", s.fields[0], s.fields[1])
	}
	if !s.fields[1].mdt {
		t.Error("MDT bit of the FFW lost")
	}
	if f := s.fieldAt(305); f == nil || f.start != 300 {
		t.Error("fieldAt(305)")
	}
	if s.fieldAt(312) != nil {
		t.Error("fieldAt(312) should be nil")
	}
}

func TestFieldFormatWord(t *testing.T) {
	f := newField(0, 8, 0x4000|0x2000|0x0700|0x0080|0x0005, nil, 0x20)
	if !f.bypass() || f.shift() != shiftSignedNumeric || !f.autoEnter() || f.adjust() != adjustRightZero || !f.numeric() {
		t.Errorf("first FFW misdecoded: %+v", f)
	}
	g := newField(0, 8, 0x4000|0x0300|0x0006, nil, 0x20)
	if g.shift() != shiftNumericOnly || g.adjust() != adjustRightBlank || g.bypass() {
		t.Errorf("second FFW misdecoded")
	}
	all := newField(0, 8, 0x4000|FFWDupEnable|FFWFieldExitRequired|FFWMonocase|FFWMandatoryEnter|7, []uint16{0x8812}, 0x27)
	if !all.dupEnable() || !all.fieldExitRequired() || !all.monocase() || !all.mandatoryEnter() ||
		all.adjust() != adjustMandatoryFill || all.cursorProgression() != 0x12 || !all.nondisplay() {
		t.Errorf("flag bits misdecoded")
	}
}

func TestScreenSnapshot(t *testing.T) {
	s := newScreen()
	s.buffer[0] = 0x22
	copy(s.buffer[1:], cp37.EncodeString("Hi"))
	s.cursor = s.addr(2, 3)
	s.addField(newField(10, 4, 0x4000, nil, 0x24))
	// A password field: its text must not appear.
	s.addField(newField(20, 4, 0x4000, nil, 0x27))
	copy(s.buffer[19:], []byte{0x27})
	copy(s.buffer[20:], cp37.EncodeString("pw!!"))
	snap := s.snapshot(cp37)
	if snap.Rows != 24 || snap.Cols != 80 || len(snap.Cells) != 24*80 {
		t.Fatal("size")
	}
	if got := snap.RowText(0)[:3]; got != " Hi" {
		t.Errorf("text %q", got)
	}
	if snap.Cells[3].Ch != ' ' {
		t.Error("null cell should be a space")
	}
	if snap.Cells[0].Attr != 0x22 || snap.Cells[1].Attr != 0x22 {
		t.Errorf("attribute in effect: %#x %#x", snap.Cells[0].Attr, snap.Cells[1].Attr)
	}
	if snap.Cells[20].Attr != 0x27 || snap.Cells[20].Ch != ' ' || snap.Cells[19].Attr != 0x27 {
		t.Errorf("password cells: %+v %+v", snap.Cells[19], snap.Cells[20])
	}
	if strings.Contains(snap.RowText(0), "pw") {
		t.Error("password text leaked into the snapshot")
	}
	if len(snap.Fields) != 2 || snap.Fields[0] != (FieldInfo{Start: 10, Length: 4}) || !snap.Fields[1].Nondisplay {
		t.Errorf("fields = %+v", snap.Fields)
	}
	if snap.Cursor.Row != 2 || snap.Cursor.Col != 3 {
		t.Errorf("cursor = %+v", snap.Cursor)
	}
	if !strings.Contains(snap.Text(), "Hi") || snap.RowText(99) != "" || strings.Count(snap.Text(), "\n") != 23 {
		t.Error("Text/RowText")
	}
}

// TestSnapshotNullAndAttributeCells: the snapshot tells a null cell, the attribute position and
// a field's data cells apart, so a renderer can keep underlines inside fields only.
func TestSnapshotNullAndAttributeCells(t *testing.T) {
	h := newHarness(t, Options{})
	// row 1: attribute 0x24, field of 5, attribute 0x20, then nulls to the end of the row.
	h.host("0440" + "04110000" + sba(1, 1) + "1d" + "4000" + "24" + "0005" + sba(1, 7) + "20")
	snap := h.session.Snapshot()
	if !snap.Cells[0].Attribute || snap.Cells[0].Null || snap.Cells[0].InField {
		t.Errorf("cell 0 = %+v, want the attribute position", snap.Cells[0])
	}
	for i := 1; i <= 5; i++ {
		if !snap.Cells[i].InField || !snap.Cells[i].Null || snap.Cells[i].Attr != 0x24 {
			t.Errorf("cell %d = %+v, want a null field cell under 0x24", i, snap.Cells[i])
		}
	}
	if !snap.Cells[6].Attribute || snap.Cells[6].Attr != 0x20 {
		t.Errorf("cell 6 = %+v, want the closing attribute", snap.Cells[6])
	}
	if c := snap.Cells[10]; !c.Null || c.InField || c.Attribute || c.Attr != 0x20 {
		t.Errorf("cell 10 = %+v, want a null outside any field", c)
	}
}
