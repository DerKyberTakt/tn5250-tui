package tn5250

import (
	"strings"
	"testing"
)

// Ported from tn5250-terminal test/engine/pco.test.ts.

// pcoScreen is what STRPCCMD writes: a 1034-byte non-display field at row 1 col 1 with the PCO
// header, the flag byte ('a' = PAUSE(*NO)), the command, then the fallback text and a Read
// MDT Fields.
func pcoScreen(command, flag, attr string) string {
	header := "80fc" + e("PCO ") + "8380a180" + e(flag) + e(command)
	return "04110028" +
		"110101" + "1d4820" + attr + "040a" + header +
		"111012" + "20" + e("Required PC program (PCO.EXE) is not active") + "20" +
		"111212" + "20" + e("Press ENTER to resume") + "20" +
		"04520000"
}

func TestPcCommandFoundWithItsPauseFlag(t *testing.T) {
	h := newHarness(t, Options{ScreenSize: "24x80"})
	h.host(pcoScreen("START http://pub400.com/demo?x=1", "a", "27"))
	got, ok := h.session.PcCommand()
	if !ok || got != (PcCommand{Command: "START http://pub400.com/demo?x=1", Wait: false}) {
		t.Fatalf("PcCommand = %+v, %v", got, ok)
	}
	h2 := newHarness(t, Options{})
	h2.host(pcoScreen(`NOTEPAD C:\TEMP\A.TXT`, "b", "27"))
	got, ok = h2.session.PcCommand()
	if !ok || got != (PcCommand{Command: `NOTEPAD C:\TEMP\A.TXT`, Wait: true}) {
		t.Fatalf("PcCommand = %+v, %v", got, ok)
	}
}

func TestPcCommandIgnoresOrdinaryScreens(t *testing.T) {
	// The same field but visible (attribute 20): not a request.
	h := newHarness(t, Options{})
	h.host(pcoScreen("START http://pub400.com/", "a", "20"))
	if c, ok := h.session.PcCommand(); ok {
		t.Errorf("visible field read as a PC command: %+v", c)
	}
	// PCO text in a visible field of an ordinary screen.
	h2 := newHarness(t, Options{})
	h2.host("04110028" + "110101" + "1d402020" + "0010" + e("PCO START x") + "04520000")
	if c, ok := h2.session.PcCommand(); ok {
		t.Errorf("ordinary screen read as a PC command: %+v", c)
	}
	// A wrong header.
	h3 := newHarness(t, Options{})
	h3.host(strings.Replace(pcoScreen("DIR", "a", "27"), "80fc"+e("PCO "), "80fc"+e("XYZ "), 1))
	if c, ok := h3.session.PcCommand(); ok {
		t.Errorf("wrong header read as a PC command: %+v", c)
	}
}

func TestPcCommandOnlyWhileTheHostWaitsForARead(t *testing.T) {
	h := newHarness(t, Options{})
	h.host(pcoScreen("START https://pub400.com/", "a", "27"))
	if _, ok := h.session.PcCommand(); !ok {
		t.Fatal("request not seen")
	}
	// Enter answers the read; the MDT field goes back with what the host wrote, as PC
	// Organizer does, and the keyboard waits for the host.
	h.session.Press(ActionEnter)
	last := h.sent[len(h.sent)-1].data
	if !strings.Contains(last, "f1"+"110102"+"80fc"+e("PCO ")) {
		t.Errorf("answer %s lacks the PCO field", last)
	}
	if h.state().inputInhibited != inhibitSystem {
		t.Errorf("keyboard %s after Enter", h.state().inputInhibited)
	}
	if c, ok := h.session.PcCommand(); ok {
		t.Errorf("still reported after the read was answered: %+v", c)
	}
	// The password-like field is blanked in the trace copy.
	if got := h.trace[len(h.trace)-1]; strings.Contains(got, e("PCO ")) {
		t.Error("trace copy holds the non-display field")
	}
}

func TestPcCommandTrimsPaddingAndControlBytes(t *testing.T) {
	h := newHarness(t, Options{})
	cmd := e("DIR") + "00" + e("C:") + "000040"
	h.host("04110028" + "110101" + "1d4820" + "27" + "040a" + "80fc" + e("PCO ") + "8380a180" + "81" + cmd + "04520000")
	got, ok := h.session.PcCommand()
	if !ok || got.Command != "DIR C:" {
		t.Fatalf("PcCommand = %+v, %v", got, ok)
	}
}
