package tn5250

import (
	"regexp"
	"strings"
	"testing"
)

// Two fields: a 5-char alpha field at row 2 col 2 and a 6-char numeric-only field at row 3 col 2.
const twoFields = "04110008" + "110201" + "1d4000200005" + "110301" + "1d4300200006" + "130202" + "0452" + "0000"

// Like twoFields, but the first field is field-exit-required (ffw 4040 -> 4048).
const ferFields = "04110008" + "110201" + "1d4048200005" + "110301" + "1d4300200006" + "130202" + "0452" + "0000"

func kbHarness(t *testing.T, o ...Options) *harness {
	opt := Options{}
	if len(o) > 0 {
		opt = o[0]
	}
	return newHarness(t, opt)
}

func (h *harness) wantError(code string) {
	h.t.Helper()
	if !regexp.MustCompile("^" + code + " ").MatchString(h.state().errorMessage) {
		h.t.Fatalf("errorMessage = %q, want it to start with %s", h.state().errorMessage, code)
	}
	if h.state().inputInhibited != inhibitError {
		h.t.Fatalf("inputInhibited = %s, want error", h.state().inputInhibited)
	}
}

func (h *harness) wantText(i int, want string) {
	h.t.Helper()
	if got := h.fieldText(i); got != want {
		h.t.Fatalf("field %d = %q, want %q", i, got, want)
	}
}

func (h *harness) wantCursor(want int) {
	h.t.Helper()
	if got := h.screen().cursor; got != want {
		h.t.Fatalf("cursor = %d (row %d col %d), want %d", got, got/h.screen().cols, got%h.screen().cols, want)
	}
}

func TestKeyboardPUB400SignOn(t *testing.T) {
	h := kbHarness(t, Options{ScreenSize: "27x132", CodePage: 37})
	h.session.Feed(loadCapture(t, "pub400-signon-3477fc").inbound(t))
	if h.state().inputInhibited != inhibitNone {
		t.Fatal("keyboard should be unlocked")
	}
	h.session.Type("alex")
	if !strings.Contains(h.rowText(4), "ALEX") {
		t.Errorf("row 4 = %q (user field is monocase)", h.rowText(4))
	}
	h.session.Press(ActionTab)
	h.wantCursor(h.screen().addr(5, 24))
	h.session.Type("secret")
	h.session.Press(ActionEnter)
	if len(h.sent) != 1 {
		t.Fatalf("sent %d records", len(h.sent))
	}
	rec := h.sent[0]
	if rec.opcode != OpPutGet {
		t.Errorf("opcode %d", rec.opcode)
	}
	// Cursor row 6 col 31, AID Enter, user field then password field, trailing blanks stripped.
	if want := "061f" + "f1" + "110519" + e("ALEX") + "110619" + e("secret"); rec.data != want {
		t.Errorf("data = %s\nwant   %s", rec.data, want)
	}
	if h.state().inputInhibited != inhibitSystem {
		t.Error("keyboard should lock after Enter")
	}
	h.session.Type("ignored while locked")
	if len(h.sent) != 1 {
		t.Error("typing while locked sent something")
	}
	// The trace copy of that frame has the password blanked but its length kept.
	last := len(h.wire) - 1
	if h.trace[last] == h.wire[last] {
		t.Fatal("password frame has no separate trace copy")
	}
	if strings.Contains(h.trace[last], e("secret")) || !strings.Contains(h.trace[last], e("ALEX")) || len(h.trace[last]) != len(h.wire[last]) {
		t.Errorf("trace copy %s", h.trace[last])
	}
	at := strings.Index(h.wire[last], e("secret"))
	if got := h.trace[last][at : at+len(e("secret"))]; got != strings.Repeat("40", 6) {
		t.Errorf("blanked bytes %s", got)
	}
}

func TestKeyboardProtectedAreaIs0005AndResetClears(t *testing.T) {
	h := kbHarness(t)
	h.host(twoFields)
	h.screen().cursor = 0
	h.session.Type("x")
	h.wantError("0005")
	h.session.Press(ActionFieldExit)
	if h.state().inputInhibited != inhibitError {
		t.Error("editing keys stay blocked in the error state")
	}
	h.session.Press(ActionReset)
	if h.state().inputInhibited != inhibitNone || h.state().errorMessage != "" {
		t.Errorf("after reset: %s %q", h.state().inputInhibited, h.state().errorMessage)
	}
	// Cursor movement and clicks also leave the error state.
	h.screen().cursor = 0
	h.session.Type("x")
	h.wantError("0005")
	h.session.Press(ActionTab)
	if h.state().inputInhibited != inhibitNone {
		t.Error("Tab should leave the error state")
	}
	h.wantCursor(h.screen().fields[0].start)
	h.session.Type("x")
	h.screen().cursor = 0
	h.session.Type("x")
	h.session.SetCursor(1, 2)
	if h.state().inputInhibited != inhibitNone {
		t.Error("a click should leave the error state")
	}
}

func TestKeyboardAutoAdvance(t *testing.T) {
	h := kbHarness(t)
	h.host(twoFields)
	h.session.Type("abcde")
	h.wantCursor(h.screen().addr(2, 1))
	if !h.screen().fields[0].mdt {
		t.Error("MDT not set")
	}
}

func TestKeyboardNumericOnly0009(t *testing.T) {
	h := kbHarness(t)
	h.host(twoFields)
	h.session.Press(ActionTab)
	h.session.Type("1a")
	h.wantError("0009")
	h.wantText(1, "1     ")
}

func TestKeyboardShiftChecks(t *testing.T) {
	cases := []struct {
		name, ffw, typed, code string
	}{
		{"alpha only", "4100", "1", "0008"},
		{"numeric only", "4300", "a", "0009"},
		{"digits only", "4500", "-", "0010"},
		{"signed numeric", "4700", "a", "0011"},
		{"input/output", "4600", "a", "0004"},
	}
	for _, c := range cases {
		h := kbHarness(t)
		h.host("04110008" + "110201" + "1d" + c.ffw + "200005" + "130202" + "04520000")
		h.session.Type(c.typed)
		h.wantError(c.code)
		if h.screen().buffer[h.screen().fields[0].start] != 0 {
			t.Errorf("%s: rejected key still changed the field", c.name)
		}
	}
	// Accepted characters.
	h := kbHarness(t)
	h.host("04110008" + "110201" + "1d4100200008" + "130202" + "04520000")
	h.session.Type("ab ,.-")
	if h.state().inputInhibited != inhibitNone {
		t.Errorf("alpha-only refused: %s", h.state().errorMessage)
	}
}

func TestKeyboardInsertMode(t *testing.T) {
	h := kbHarness(t)
	h.host(twoFields)
	h.session.Type("ace")
	h.session.Press(ActionHome)
	h.session.Press(ActionRight)
	h.session.Press(ActionInsert)
	if !h.state().insert {
		t.Fatal("insert should be on")
	}
	h.session.Type("b")
	h.wantText(0, "abce ")
	h.session.Type("d")
	h.session.Type("x")
	h.wantError("0012")
	h.session.Press(ActionReset)
	if h.state().insert {
		t.Error("Reset turns insert off")
	}
}

func TestKeyboardFieldExitAndFieldMinus(t *testing.T) {
	h := kbHarness(t)
	h.host(twoFields)
	h.session.Type("abcde")
	h.session.Press(ActionBacktab)
	h.session.Press(ActionRight)
	h.session.Press(ActionRight)
	h.session.Press(ActionFieldExit)
	h.wantText(0, "ab   ")
	h.wantCursor(h.screen().fields[1].start)
	h.session.Type("125")
	h.session.Press(ActionFieldMinus)
	b := h.screen().fieldBytes(h.screen().fields[1])
	if b[0] != 0xf1 || b[1] != 0xf2 || b[2] != 0xd5 {
		t.Errorf("field minus bytes %x", b[:3])
	}
	// Field Minus wrapped the cursor to the alphabetic field; it is not allowed there.
	h.wantCursor(h.screen().fields[0].start)
	h.session.Press(ActionFieldMinus)
	h.wantError("0016")
}

func TestKeyboardFieldMinusNeedsNumericLastDigit(t *testing.T) {
	h := kbHarness(t)
	h.host("04110008" + "110301" + "1d4300200006" + "130302" + "04520000")
	h.session.Type("1+")
	h.session.Press(ActionFieldMinus)
	h.wantError("0026")
}

func TestKeyboardRightAdjustAndSignedNumeric(t *testing.T) {
	h := kbHarness(t)
	// field 1: numeric only, right adjust zero fill (ffw 4305); field 2: signed numeric (ffw 4700), len 5
	h.host("04110008" + "110201" + "1d4305200006" + "110301" + "1d4700200005" + "130202" + "04520000")
	h.session.Type("42")
	h.session.Press(ActionFieldExit)
	h.wantText(0, "000042")
	h.session.Type("17")
	h.session.Press(ActionFieldMinus)
	h.wantText(1, "  17-")
}

func TestKeyboardRightAdjustBlankAndMandatoryFill(t *testing.T) {
	h := kbHarness(t)
	// field 1: right adjust blank fill (ffw 4006), len 5; field 2: mandatory fill (ffw 4007), len 4.
	h.host("04110008" + "110201" + "1d4006200005" + "110301" + "1d4007200004" + "130202" + "04520000")
	h.session.Type("ab")
	h.session.Press(ActionFieldExit)
	h.wantText(0, "   ab")
	h.session.Type("12")
	h.session.Press(ActionFieldExit)
	h.wantError("0014")
	h.session.Press(ActionReset)
	h.session.Type("34")
	h.session.Press(ActionFieldExit)
	if h.state().inputInhibited != inhibitNone {
		t.Errorf("a filled mandatory-fill field should exit: %s", h.state().errorMessage)
	}
}

func TestKeyboardEraseEOFDeleteBackspace(t *testing.T) {
	h := kbHarness(t)
	h.host(twoFields)
	h.session.Type("abcde")
	h.session.Press(ActionBacktab)
	h.session.Press(ActionRight)
	h.session.Press(ActionDelete)
	h.wantText(0, "acde ")
	h.session.Press(ActionRight)
	h.session.Press(ActionBackspace)
	h.wantText(0, "ade  ")
	h.session.Press(ActionRight)
	h.session.Press(ActionEraseEOF)
	h.wantText(0, "ad   ")
}

func TestKeyboardEraseInput(t *testing.T) {
	h := kbHarness(t)
	h.host(twoFields)
	h.session.Type("abcde")
	h.session.Type("123")
	h.session.Press(ActionEraseInput)
	for _, f := range h.screen().fields {
		if f.mdt {
			t.Error("MDT left on")
		}
	}
	if strings.TrimSpace(h.rowText(1)) != "" {
		t.Errorf("row 1 = %q", h.rowText(1))
	}
	h.wantCursor(h.screen().fields[0].start)
}

func TestKeyboardDup(t *testing.T) {
	h := kbHarness(t)
	h.host("04110008" + "110201" + "1d5000200004" + "110301" + "1d4000200004" + "130202" + "04520000")
	h.session.Press(ActionDup)
	for _, b := range h.screen().fieldBytes(h.screen().fields[0]) {
		if b != 0x1c {
			t.Fatalf("dup bytes %x", h.screen().fieldBytes(h.screen().fields[0]))
		}
	}
	h.wantCursor(h.screen().fields[1].start)
	h.session.Press(ActionDup)
	h.wantError("0019")
}

func TestKeyboardFieldMark(t *testing.T) {
	h := kbHarness(t)
	h.host(twoFields)
	h.session.Press(ActionFieldMark)
	if h.screen().buffer[h.screen().fields[0].start] != 0x1e || !h.screen().fields[0].mdt {
		t.Error("field mark not written")
	}
	h.wantCursor(h.screen().fields[0].start + 1)
	h.screen().cursor = 0
	h.session.Press(ActionFieldMark)
	h.wantError("0019")
}

func TestKeyboardAttnSysReqTestRequest(t *testing.T) {
	h := kbHarness(t)
	h.host(twoFields)
	h.session.Press(ActionAttn)
	h.session.Press(ActionSysReq)
	h.session.Press(ActionTestRequest)
	want := []sent{{OpNoop, FlagATN, ""}, {OpNoop, FlagSRQ, ""}, {OpNoop, FlagTRQ, ""}}
	if len(h.sent) != 3 || h.sent[0] != want[0] || h.sent[1] != want[1] || h.sent[2] != want[2] {
		t.Fatalf("sent %+v", h.sent)
	}
}

func TestKeyboardAttnStillGoesOutWhileLocked(t *testing.T) {
	h := kbHarness(t)
	h.host(twoFields)
	h.session.Press(ActionEnter)
	if h.state().inputInhibited != inhibitSystem {
		t.Fatal("should be locked")
	}
	h.session.Press(PF(3)) // ordinary keys stay locked out
	h.session.Press(ActionSysReq)
	h.session.Press(ActionAttn)
	if len(h.sent) != 3 || h.sent[1] != (sent{OpNoop, FlagSRQ, ""}) || h.sent[2] != (sent{OpNoop, FlagATN, ""}) {
		t.Fatalf("sent %+v", h.sent)
	}
}

func TestKeyboardFunctionKeysSendTheirAID(t *testing.T) {
	h := kbHarness(t)
	h.host("04110008" + "110201" + "1d4000200003" + "130202" + "0442" + "0000")
	h.session.Type("ab")
	h.session.Press(PF(3))
	if want := "0204" + "33" + e("ab") + "40"; h.sent[0].data != want {
		t.Errorf("Read Input Fields response %s, want %s", h.sent[0].data, want)
	}
	h.host("04110008" + "0452" + "0000")
	h.session.Press(PF(13))
	if got := h.sent[1].data[4:6]; got != "b1" {
		t.Errorf("F13 AID %s", got)
	}
}

func TestKeyboardAIDTable(t *testing.T) {
	cases := map[Action]byte{
		ActionEnter: 0xf1, ActionClear: 0xbd, ActionHelp: 0xf3, ActionPageUp: 0xf4, ActionPageDown: 0xf5,
		ActionPrint: 0xf6, ActionRecordBackspace: 0xf8,
		PF(1): 0x31, PF(9): 0x39, PF(10): 0x3a, PF(12): 0x3c, PF(13): 0xb1, PF(24): 0xbc,
	}
	for a, want := range cases {
		got, ok := AIDOf(a)
		if !ok || got != want {
			t.Errorf("AIDOf(%s) = %#x,%v want %#x", a, got, ok, want)
		}
	}
	for _, a := range []Action{ActionTab, "pf0", "pf25", "pfx", "pf07", PF(0), PF(25), ""} {
		if _, ok := AIDOf(a); ok {
			t.Errorf("AIDOf(%q) should be false", a)
		}
	}
	// Each of them reaches the host as that AID.
	for a, want := range cases {
		h := kbHarness(t)
		h.host("04110008" + "110201" + "1d4000200003" + "130202" + "0452" + "0000")
		h.session.Press(a)
		if len(h.sent) != 1 || h.sent[0].data[4:6] != hx(int(want)) {
			t.Errorf("%s: sent %+v", a, h.sent)
		}
	}
}

func TestKeyboardAutoEnterAndMandatoryEnter(t *testing.T) {
	h := kbHarness(t)
	// field 1 auto-enter (ffw 4080) len 2; field 2 mandatory enter (ffw 4008) len 3
	h.host("04110008" + "110201" + "1d4080200002" + "110301" + "1d4008200003" + "130202" + "04520000")
	h.session.Press(ActionEnter)
	if len(h.sent) != 0 {
		t.Fatal("Enter went out with a mandatory field empty")
	}
	h.wantError("0007")
	h.wantCursor(h.screen().fields[1].start)
	h.session.Press(ActionReset)
	h.session.Type("xyz")
	h.session.Press(ActionHome)
	h.session.Type("12")
	if len(h.sent) != 1 || h.sent[0].data[4:6] != "3f" {
		t.Fatalf("sent %+v", h.sent)
	}
}

func TestKeyboardClearAndHelpSkipMandatoryEnterCheck(t *testing.T) {
	h := kbHarness(t)
	h.host("04110008" + "110201" + "1d4008200003" + "130202" + "04520000")
	h.session.Press(ActionHelp)
	if len(h.sent) != 1 || h.sent[0].data[4:6] != "f3" {
		t.Fatalf("Help: %+v", h.sent)
	}
	h.host("04110008" + "110201" + "1d4008200003" + "130202" + "04520000")
	h.session.Press(ActionClear)
	if len(h.sent) != 2 || h.sent[1].data[4:6] != "bd" {
		t.Fatalf("Clear: %+v", h.sent)
	}
}

func TestKeyboardCursorKeysWrapAndNewline(t *testing.T) {
	h := kbHarness(t)
	h.host(twoFields)
	h.screen().cursor = 0
	h.session.Press(ActionLeft)
	h.wantCursor(h.screen().size() - 1)
	h.session.Press(ActionDown)
	h.wantCursor(h.screen().cols - 1)
	h.session.Press(ActionNewline)
	h.wantCursor(h.screen().fields[0].start)
	h.screen().cursor = 5
	h.session.Press(ActionUp)
	h.wantCursor(5 - 80 + h.screen().size())
	h.session.Press(ActionRight)
	h.wantCursor(5 - 80 + h.screen().size() + 1)
}

func TestKeyboardTabAndBacktabAcrossFields(t *testing.T) {
	h := kbHarness(t)
	h.host(twoFields)
	f0, f1 := h.screen().fields[0], h.screen().fields[1]
	h.wantCursor(f0.start)
	h.session.Press(ActionTab)
	h.wantCursor(f1.start)
	h.session.Press(ActionTab) // wraps
	h.wantCursor(f0.start)
	h.session.Press(ActionBacktab) // wraps back
	h.wantCursor(f1.start)
	h.session.Press(ActionRight)
	h.session.Press(ActionBacktab) // to the start of the current field first
	h.wantCursor(f1.start)
	h.session.Press(ActionBacktab)
	h.wantCursor(f0.start)
	h.session.Press(ActionHome)
	h.wantCursor(f0.start)
}

func TestKeyboardFastMoves(t *testing.T) {
	h := kbHarness(t)
	h.host(twoFields)
	h.screen().cursor = 0
	h.session.Press(ActionFastRight)
	h.wantCursor(3)
	h.session.Press(ActionFastDown)
	h.wantCursor(3 + 3*h.screen().cols)
	h.screen().cursor = 0
	h.session.Press(ActionFastLeft)
	h.wantCursor(h.screen().size() - 3)
	h.screen().cursor = 0
	h.session.Press(ActionFastUp)
	h.wantCursor(h.screen().size() - 3*h.screen().cols)
}

func TestKeyboardWordMoves(t *testing.T) {
	h := kbHarness(t)
	h.host(twoFields)
	f0, f1 := h.screen().fields[0], h.screen().fields[1]
	h.screen().cursor = f0.start
	h.session.Type("ab")
	h.session.Press(ActionTab)
	h.session.Type("12")
	h.screen().cursor = f0.start
	h.session.Press(ActionWordRight)
	h.wantCursor(f1.start)
	h.session.Press(ActionWordRight) // wraps to the first word again
	h.wantCursor(f0.start)
	h.screen().cursor = f1.start + 1
	h.session.Press(ActionWordLeft) // start of the current word first
	h.wantCursor(f1.start)
	h.session.Press(ActionWordLeft)
	h.wantCursor(f0.start)
}

func TestKeyboardBeginningOfField(t *testing.T) {
	h := kbHarness(t)
	h.host(twoFields)
	f0 := h.screen().fields[0]
	h.screen().cursor = f0.start + 3
	h.session.Press(ActionBeginningOfField)
	h.wantCursor(f0.start)
	h.screen().cursor = 0
	h.session.Press(ActionBeginningOfField)
	h.wantError("0005")
}

func TestKeyboardDeleteWord(t *testing.T) {
	h := kbHarness(t)
	h.host(twoFields)
	f0 := h.screen().fields[0]
	h.screen().cursor = f0.start
	h.session.Type("ab cd") // fills the 5-char field and auto-advances
	h.screen().cursor = f0.start
	h.session.Press(ActionDeleteWord)
	if got := b2h(h.screen().fieldBytes(f0)); got != e("cd")+"000000" {
		t.Errorf("field = %s", got)
	}
	if !f0.mdt {
		t.Error("MDT")
	}
	h.wantCursor(f0.start)
	if got := b2h(h.screen().fieldBytes(h.screen().fields[1])); got != "000000000000" {
		t.Errorf("second field changed: %s", got)
	}
}

func TestKeyboardEraseField(t *testing.T) {
	h := kbHarness(t)
	h.host(twoFields)
	f0 := h.screen().fields[0]
	h.screen().cursor = f0.start
	h.session.Type("abc")
	h.screen().cursor = f0.start + 1
	h.session.Press(ActionEraseField)
	if got := b2h(h.screen().fieldBytes(f0)); got != "0000000000" {
		t.Errorf("field = %s", got)
	}
	h.wantCursor(f0.start)
	if !f0.mdt {
		t.Error("MDT")
	}
}

func TestKeyboardPastTheEndOfFieldExitRequiredField(t *testing.T) {
	t.Run("the last character stays on typing and survives Field Exit", func(t *testing.T) {
		h := kbHarness(t)
		h.host(ferFields)
		h.session.Type("abcde")
		h.wantCursor(h.screen().fields[0].end() - 1)
		h.session.Press(ActionFieldExit)
		h.wantText(0, "abcde")
		h.wantCursor(h.screen().fields[1].start)
	})
	t.Run("typing past the end reports 0020 until the cursor moves", func(t *testing.T) {
		h := kbHarness(t)
		h.host(ferFields)
		h.session.Type("abcdef")
		h.wantError("0020")
		h.wantText(0, "abcde")
		h.session.Press(ActionReset)
		h.session.Type("x")
		h.wantError("0020")
		h.session.Press(ActionLeft) // onto the 'd'
		h.session.Type("x")
		h.wantText(0, "abcxe")
	})
	t.Run("Erase EOF past the end keeps the field; one cell back it erases", func(t *testing.T) {
		h := kbHarness(t)
		h.host(ferFields)
		h.session.Type("abcde")
		h.session.Press(ActionEraseEOF)
		h.wantText(0, "abcde")
		h.session.Press(ActionLeft)
		h.session.Press(ActionEraseEOF)
		h.wantText(0, "abc  ")
	})
	t.Run("Backspace past the end removes the last character, then works as usual", func(t *testing.T) {
		h := kbHarness(t)
		h.host(ferFields)
		h.session.Type("abcde")
		h.session.Press(ActionBackspace)
		h.wantText(0, "abcd ")
		h.wantCursor(h.screen().fields[0].end() - 1)
		h.session.Press(ActionBackspace)
		h.wantText(0, "abc  ")
		h.session.Type("xy")
		h.wantText(0, "abcxy")
	})
	for _, leave := range []string{"click", "host", "insert"} {
		t.Run("a "+leave+" ends the past-the-end state", func(t *testing.T) {
			h := kbHarness(t)
			h.host(ferFields)
			h.session.Type("abcde")
			switch leave {
			case "click":
				h.session.SetCursor(1, 5)
			case "host":
				h.host("04110008" + "110501" + e("msg") + "130206" + "04520000")
			case "insert":
				h.session.Press(ActionInsert)
			}
			h.session.Press(ActionFieldExit)
			h.wantText(0, "abcd ")
		})
	}
}

func TestKeyboardFieldExitRightAfterAutoAdvance(t *testing.T) {
	setup := func() *harness {
		h := kbHarness(t)
		h.host(twoFields)
		h.session.SetCursor(2, 1)
		h.session.Type("42")
		h.session.SetCursor(1, 1)
		h.session.Type("abcde") // fills field 1, advances to field 2
		h.wantCursor(h.screen().fields[1].start)
		return h
	}
	h := setup()
	h.session.Press(ActionFieldExit)
	h.wantText(1, "42    ")
	h.wantCursor(h.screen().fields[1].start)
	h.session.Press(ActionFieldExit)
	h.wantText(1, "      ")
	// Another key in between: erases as usual.
	h = setup()
	h.session.Press(ActionRight)
	h.session.Press(ActionLeft)
	h.session.Press(ActionFieldExit)
	h.wantText(1, "      ")
}

func TestKeyboardAutoEnter(t *testing.T) {
	aeFields := "04110008" + "110201" + "1d4080200005" + "110301" + "1d4300200006" + "130202" + "0452" + "0000"
	aeFerFields := "04110008" + "110201" + "1d40c0200005" + "110301" + "1d4300200006" + "130202" + "0452" + "0000"
	t.Run("sends the Enter when the last character fills the field", func(t *testing.T) {
		h := kbHarness(t)
		h.host(aeFields)
		h.session.Type("abcde")
		if len(h.sent) != 1 || h.sent[0].data[4:6] != "3f" {
			t.Fatalf("sent %+v", h.sent)
		}
	})
	t.Run("with Field Exit Required waits for the Field Exit, as ACS does", func(t *testing.T) {
		h := kbHarness(t)
		h.host(aeFerFields)
		h.session.Type("abcde")
		if len(h.sent) != 0 || !h.state().cursorPastEnd {
			t.Fatalf("sent %+v pastEnd %v", h.sent, h.state().cursorPastEnd)
		}
		h.wantText(0, "abcde")
		h.session.Press(ActionFieldExit)
		if len(h.sent) != 1 || h.sent[0].data[4:6] != "3f" {
			t.Fatalf("sent %+v", h.sent)
		}
		h.wantText(0, "abcde")
	})
}

func TestKeyboardEndOfField(t *testing.T) {
	t.Run("moves just after the last typed character", func(t *testing.T) {
		h := kbHarness(t)
		h.host(twoFields)
		h.session.Type("ab")
		h.session.Press(ActionHome)
		h.session.Press(ActionEndOfField)
		h.wantCursor(h.screen().fields[0].start + 2)
	})
	t.Run("on a full field goes to the last cell past the end", func(t *testing.T) {
		h := kbHarness(t)
		h.host(ferFields)
		h.session.Type("abcde")
		h.session.Press(ActionHome)
		h.session.Press(ActionEndOfField)
		h.wantCursor(h.screen().fields[0].end() - 1)
		h.session.Press(ActionFieldExit)
		h.wantText(0, "abcde")
	})
	t.Run("goes to the start of an empty field and reports 0005 outside a field", func(t *testing.T) {
		h := kbHarness(t)
		h.host(twoFields)
		h.session.Press(ActionRight)
		h.session.Press(ActionEndOfField)
		h.wantCursor(h.screen().fields[0].start)
		h.screen().cursor = 0
		h.session.Press(ActionEndOfField)
		h.wantError("0005")
	})
}

func TestKeyboardCommandAttentionKeysSendNoFieldData(t *testing.T) {
	// Alpha field at row 2 col 2; the header marks F3 and F12 as CA keys.
	ca := "04110008" + "0107" + "00000018" + "000804" + "110201" + "1d4000200005" + "130202" + "0452" + "0000"
	t.Run("F3 sends only the cursor and AID; F5 and Enter send what was typed", func(t *testing.T) {
		h := kbHarness(t)
		h.host(ca)
		h.session.Type("1")
		h.session.Press(PF(3))
		if len(h.sent) != 1 || h.sent[0].data != "0203"+"33" {
			t.Fatalf("sent %+v", h.sent)
		}
		h.host(ca)
		h.session.Type("1")
		h.session.Press(PF(5))
		if h.sent[1].data != "0203"+"35"+"110202"+e("1") {
			t.Errorf("F5 data %s", h.sent[1].data)
		}
	})
	t.Run("a CA key does not trip the mandatory-enter check", func(t *testing.T) {
		mand := "04110008" + "0107" + "00000018" + "000804" + "110201" + "1d4008200005" + "130202" + "04520000"
		h := kbHarness(t)
		h.host(mand)
		h.session.Press(PF(3))
		if len(h.sent) != 1 || h.state().errorMessage != "" {
			t.Fatalf("sent %+v err %q", h.sent, h.state().errorMessage)
		}
		h.host(mand)
		h.session.Press(ActionEnter)
		h.wantError("0007")
	})
	t.Run("without the header bytes every key sends data", func(t *testing.T) {
		h := kbHarness(t)
		h.host(twoFields)
		h.session.Type("1")
		h.session.Press(PF(3))
		if h.sent[0].data != "0203"+"33"+"110202"+e("1") {
			t.Errorf("data %s", h.sent[0].data)
		}
	})
}

func TestKeyboardCursorProgression(t *testing.T) {
	// Three fields on rows 2, 3, 4; the first has an FCW X'8803' sending a forward exit to
	// field number 3.
	h := kbHarness(t)
	h.host("04110008" + "110201" + "1d4000" + "8803" + "200003" + "110301" + "1d4000200003" + "110401" + "1d4000200003" + "130202" + "04520000")
	if len(h.screen().fields) != 3 || h.screen().fields[0].cursorProgression() != 3 {
		t.Fatalf("fields: %d", len(h.screen().fields))
	}
	h.session.Press(ActionTab)
	h.wantCursor(h.screen().fields[2].start)
	h.session.Press(ActionBacktab) // back out of field 3: to its source, field 1
	h.wantCursor(h.screen().fields[0].start)
}

func TestKeyboardOperatorErrorTexts(t *testing.T) {
	for code, text := range OperatorErrors {
		if len(code) != 4 || text == "" {
			t.Errorf("odd operator error %q %q", code, text)
		}
	}
	for _, code := range []string{"0005", "0007", "0010", "0014", "0020", "0026", "0004", "0008", "0009", "0011", "0012", "0016", "0019"} {
		if OperatorErrors[code] == "" {
			t.Errorf("no text for %s", code)
		}
	}
}

func TestKeyboardIgnoresKeysWhileLocked(t *testing.T) {
	h := kbHarness(t) // locked: no host write yet
	h.session.Type("abc")
	h.session.Press(ActionTab)
	h.session.Press(ActionEnter)
	if len(h.sent) != 0 || h.state().inputInhibited != inhibitSystem {
		t.Errorf("sent %+v inhibited %s", h.sent, h.state().inputInhibited)
	}
}

func TestKeyboardMonocaseLeavesOtherFieldsAlone(t *testing.T) {
	h := kbHarness(t)
	h.host("04110008" + "110201" + "1d4020200005" + "110301" + "1d4000200005" + "130202" + "04520000")
	h.session.Type("ab")
	h.wantText(0, "AB   ")
	h.session.Press(ActionTab)
	h.session.Type("ab")
	h.wantText(1, "ab   ")
}

func TestKeyboardUnknownActionDoesNothing(t *testing.T) {
	h := kbHarness(t)
	h.host(twoFields)
	h.session.Press("bogus")
	if len(h.sent) != 0 || h.state().inputInhibited != inhibitNone {
		t.Error("unknown action changed state")
	}
}
