package tn5250

import (
	"strings"
	"testing"
)

// Ported from tn5250-terminal test/engine/datastream.test.ts: the screen read commands, the
// Query reply next to a Save Screen at 27x132, and the Print key flow.

// readScreenRig shows "AB" at 1,1 and "Z" at 3,5 on a 24x80 screen; everything else is null.
func readScreenRig(t testing.TB) *procRig {
	r := newRig("IBM-3477-FC")
	r.do(t, "0440"+"04110008"+sba(1, 1)+e("AB")+sba(3, 5)+e("Z"))
	return r
}

func TestReadScreenAnswersTheImage(t *testing.T) {
	r := readScreenRig(t)
	for _, cmd := range []string{"0462", "0466"} {
		resp := r.do(t, cmd)
		if len(resp) != 1 || resp[0].opcode != OpNoop {
			t.Fatalf("%s: responses %+v", cmd, resp)
		}
		if len(resp[0].data) != 24*80 {
			t.Fatalf("%s: image is %d bytes, want %d", cmd, len(resp[0].data), 24*80)
		}
		if got := b2h(resp[0].data[:2]); got != e("AB") {
			t.Errorf("%s: row 1 starts %s", cmd, got)
		}
		if got := b2h(resp[0].data[2*80+4 : 2*80+5]); got != e("Z") {
			t.Errorf("%s: row 3 col 5 = %s", cmd, got)
		}
		if resp[0].data[79] != 0x00 {
			t.Errorf("%s: unwritten cells are not nulls", cmd)
		}
	}
}

func TestReadScreenExtendedAnswersRowsEndingInFF(t *testing.T) {
	r := readScreenRig(t)
	// Each row is its cells with trailing nulls cut, then X'FF'; leading nulls stay.
	want := e("AB") + "ff" + // row 1
		"ff" + // row 2: all null
		"00000000" + e("Z") + "ff" + // row 3
		strings.Repeat("ff", 21) // rows 4-24
	for _, cmd := range []string{"0464", "046e"} {
		resp := r.do(t, cmd)
		if len(resp) != 1 || resp[0].opcode != OpNoop {
			t.Fatalf("%s: responses %+v", cmd, resp)
		}
		if got := b2h(resp[0].data); got != want {
			t.Errorf("%s:\n got %s\nwant %s", cmd, got, want)
		}
		if len(resp[0].secret) != 0 {
			t.Errorf("%s: secret ranges %v without a non-display field", cmd, resp[0].secret)
		}
	}
	// A full row keeps all its bytes.
	r.do(t, "04110008"+sba(5, 1)+strings.Repeat(e("x"), 80))
	data := r.do(t, "0464")[0].data
	if got := strings.Count(b2h(data), strings.Repeat(e("x"), 80)); got != 1 {
		t.Errorf("full row appears %d times", got)
	}
}

func TestReadScreenExtendedAt27x132(t *testing.T) {
	r := newRig("IBM-3477-FC")
	r.do(t, "042000"+"04110008"+sba(27, 132)+e("E"))
	if r.screen.rows != 27 || r.screen.cols != 132 {
		t.Fatalf("size %dx%d", r.screen.rows, r.screen.cols)
	}
	if img := r.do(t, "0462")[0].data; len(img) != 27*132 {
		t.Errorf("Read Screen image is %d bytes, want %d", len(img), 27*132)
	}
	ext := r.do(t, "0464")[0].data
	if n := strings.Count(b2h(ext), "ff"); n < 27 {
		t.Errorf("rows ended by FF: %d, want 27", n)
	}
	// 26 empty rows (just FF), then 131 nulls and the E of the last row, then FF.
	if got, want := len(ext), 26+131+1+1; got != want {
		t.Errorf("extended image is %d bytes, want %d", got, want)
	}
	if ext[len(ext)-1] != 0xff || ext[len(ext)-2] != cp37.EncodeString("E")[0] {
		t.Errorf("tail %x", ext[len(ext)-3:])
	}
}

func TestReadScreenExtendedMarksPasswordRanges(t *testing.T) {
	r := newRig("IBM-3477-FC")
	r.do(t, "04110008"+sba(6, 10)+"1d4800270010")
	copy(r.screen.buffer[r.screen.fields[0].start:], cp37.EncodeString("s3cret"))
	ext := r.do(t, "046e")
	if len(ext) != 1 || len(ext[0].secret) != 1 {
		t.Fatalf("secret = %+v", ext)
	}
	rng := ext[0].secret[0]
	if got := string(ext[0].data[rng[0] : rng[0]+6]); got != string(cp37.EncodeString("s3cret")) {
		t.Errorf("range points at %x", got)
	}
}

// The Query reply (a plain 27x132 display) and a Save Screen can share one record; the reply
// comes first, leaves the screen alone, and the saved image restores the 27x132 screen.
func TestQueryReplyBesideSaveScreenAt27x132(t *testing.T) {
	r := newRig("IBM-3477-FC")
	r.do(t, "042000"+"04110008"+sba(1, 1)+e("WIDE MENU")+sba(27, 1)+e("F3=Exit"))
	resp := r.do(t, "04f30005d97000"+"0402")
	if len(resp) != 2 {
		t.Fatalf("responses %d, want the Query reply and the saved screen", len(resp))
	}
	reply, saved := resp[0], resp[1]
	if reply.opcode != OpNoop || len(reply.data) != 61 || reply.data[50] != 0x31 {
		t.Errorf("query reply opcode %d len %d byte50 %#x", reply.opcode, len(reply.data), reply.data[50])
	}
	if got := cp37.DecodeBytes(reply.data[30:34]); got != "3477" {
		t.Errorf("machine %q", got)
	}
	if saved.opcode != OpSaveScreen {
		t.Fatalf("saved opcode %d", saved.opcode)
	}
	// ESC Restore Screen, then Clear Unit Alternate for the 132-column screen.
	if got := b2h(saved.data[:5]); got != hx(ESC)+hx(CmdRestoreScreen)+hx(ESC)+hx(CmdClearUnitAlt)+"00" {
		t.Errorf("saved stream starts %s", got)
	}
	// Show a 24x80 screen, then send the image back: 27x132 and its text return.
	r.do(t, "0440"+"04110008"+sba(1, 1)+e("small"))
	if r.screen.cols != 80 {
		t.Fatal("did not go back to 80 columns")
	}
	r.proc.process(saved.data)
	if r.screen.rows != 27 || r.screen.cols != 132 {
		t.Fatalf("restored %dx%d", r.screen.rows, r.screen.cols)
	}
	if !strings.HasPrefix(r.row(0), "WIDE MENU") || !strings.HasPrefix(r.row(26), "F3=Exit") {
		t.Errorf("rows %q / %q", r.row(0), r.row(26))
	}
}

// The Print key sends AID F6; the host then reads the screen with Read Screen to Print
// (X'66', or X'6E' extended) to spool it, and the answer is the plain image.
func TestPrintKeyFlow(t *testing.T) {
	h := newHarness(t, Options{ScreenSize: "27x132"})
	h.host("042000" + "04110008" + sba(3, 5) + e("Name:") + sba(3, 12) + "1d4000200008" + "13030d" + "0452" + "0000")
	h.session.Type("Alex")
	n := len(h.sent)
	h.session.Press(ActionPrint)
	if len(h.sent) != n+1 {
		t.Fatalf("Print sent %d records, want 1", len(h.sent)-n)
	}
	answer := h.sent[n]
	if answer.opcode != OpPutGet || answer.data[4:6] != "f6" {
		t.Fatalf("Print answer opcode %d data %s, want AID F6", answer.opcode, answer.data)
	}
	if h.state().inputInhibited != inhibitSystem {
		t.Errorf("keyboard %s after Print, want X SYSTEM until the host answers", h.state().inputInhibited)
	}
	// The host asks for the image to print.
	h.host("0466")
	if len(h.sent) != n+2 {
		t.Fatalf("Read Screen to Print: %d records sent, want 1 more", len(h.sent)-n-1)
	}
	image := h.sent[n+1]
	if image.opcode != OpNoop {
		t.Errorf("image opcode %d, want %d", image.opcode, OpNoop)
	}
	if len(image.data) != 2*27*132 { // hex digits: 27x132 cells, one byte each
		t.Errorf("image is %d bytes, want %d", len(image.data)/2, 27*132)
	}
	if !strings.Contains(image.data, e("Name:")) || !strings.Contains(image.data, e("Alex")) {
		t.Error("image lacks the screen text")
	}
	// Extended: rows ending in FF; the same content as a plain Read Screen Extended.
	h.host("046e")
	ext := h.sent[n+2]
	if ext.opcode != OpNoop || !strings.Contains(ext.data, e("Name:")) || !strings.HasSuffix(ext.data, "ff") {
		t.Errorf("extended image opcode %d data %.60s...", ext.opcode, ext.data)
	}
	h.host("0464")
	if h.sent[n+3].data != ext.data {
		t.Error("Read Screen Extended differs from Read Screen Extended to Print")
	}
}
