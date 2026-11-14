package tn5250

import (
	"strings"
	"testing"
)

func textBytes(s string) []byte { return cp37.EncodeString(s) }

func wtd(orders ...[]byte) []byte {
	out := []byte{ESC, CmdWTD, 0x00, 0x00}
	for _, o := range orders {
		out = append(out, o...)
	}
	return out
}

func sbaB(row, col int) []byte { return []byte{oSBA, byte(row), byte(col)} }

func menuRig(t testing.TB) *procRig {
	r := newRig("IBM-3477-FC")
	r.proc.process([]byte{ESC, CmdClearUnit})
	r.proc.process(wtd(sbaB(1, 1), textBytes("MAIN MENU"), sbaB(22, 1), textBytes("===> command line"), sbaB(23, 1), textBytes("F3=Exit   F12=Cancel")))
	return r
}

// The assist menus (and Esc out of them) make the host save the screen, paint over it, then
// send the saved image back: the command line and the function-key rows must come back
// exactly, for a partial save as for a full one.
func TestSaveRestoreScreenRowsComeBack(t *testing.T) {
	for _, c := range []struct {
		name  string
		cmd   byte
		extra []byte
	}{
		{"Save Screen / Restore Screen", CmdSaveScreen, nil},
		{"Save Partial Screen / Restore Partial Screen", CmdSavePartialScreen, []byte{0x00, 20, 1, 5, 80}},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := menuRig(t)
			saved := r.proc.process(append([]byte{ESC, c.cmd}, c.extra...))
			if len(saved) != 1 || saved[0].opcode != OpSaveScreen {
				t.Fatalf("saved = %+v", saved)
			}
			// The host paints a menu over the bottom rows.
			r.proc.process(wtd(sbaB(22, 1), textBytes(strings.Repeat(" ", 80)), sbaB(23, 1), textBytes(padRight("   Select one of the following", 80))))
			if strings.TrimSpace(r.row(21)) != "" || !strings.Contains(r.row(22), "Select one") {
				t.Fatalf("paint over failed: %q / %q", r.row(21), r.row(22))
			}
			// Then sends back what it saved.
			r.proc.process(saved[0].data)
			for row, want := range map[int]string{0: "MAIN MENU", 21: "===> command line", 22: "F3=Exit   F12=Cancel"} {
				if !strings.Contains(r.row(row), want) {
					t.Errorf("row %d = %q, want %q", row, r.row(row), want)
				}
			}
		})
	}
}

func padRight(s string, n int) string { return s + strings.Repeat(" ", n-len(s)) }

func TestRestoreAfterScreenWasClearedAndRewritten(t *testing.T) {
	r := menuRig(t)
	saved := r.proc.process([]byte{ESC, CmdSavePartialScreen, 0x00, 1, 1, 24, 80})
	r.proc.process([]byte{ESC, CmdClearUnit})
	r.proc.process(wtd(sbaB(1, 1), textBytes("SOMETHING ELSE")))
	if strings.TrimSpace(r.row(21)) != "" {
		t.Fatal("row 21 should be blank")
	}
	r.proc.process(saved[0].data)
	for row, want := range map[int]string{0: "MAIN MENU", 21: "===> command line", 22: "F3=Exit   F12=Cancel"} {
		if !strings.Contains(r.row(row), want) {
			t.Errorf("row %d = %q", row, r.row(row))
		}
	}
}

func TestSavePartialScreenAnswersRestorePartial(t *testing.T) {
	r := menuRig(t)
	saved := r.do(t, "0403"+"00"+"07"+"1e"+"05"+"14")
	if got := b2h(saved[0].data[:2]); got != "0413" {
		t.Fatalf("starts %s", got)
	}
}

func TestSaveScreenRebuildsFields(t *testing.T) {
	a := newRig("IBM-3477-FC")
	a.do(t, "04110008"+"110305"+e("Name:")+"1d4000200008"+e("Alex")+"20"+"130307")
	saved := a.do(t, "0402")
	if saved[0].opcode != OpSaveScreen {
		t.Fatalf("opcode %d", saved[0].opcode)
	}
	b := newRig("IBM-3477-FC")
	b.proc.process(saved[0].data)
	if got := strings.TrimSpace(b.row(2)); got != "Name: Alex" {
		t.Errorf("row 2 = %q", got)
	}
	if len(b.screen.fields) != 1 || b.screen.fields[0].length != 8 {
		t.Errorf("fields = %d", len(b.screen.fields))
	}
	if b.screen.cursor != a.screen.cursor {
		t.Errorf("cursor %d != %d", b.screen.cursor, a.screen.cursor)
	}
}

func TestRestoreScreenRestoresScreenSize(t *testing.T) {
	r := newRig("IBM-3477-FC")
	// 24x80 menu: attribute at (5,80) so the text wraps to (6,1).
	r.do(t, "0440"+"04110008"+"110550"+"30"+e(" 1.")+"130101")
	if got := r.row(5)[:3]; got != " 1." {
		t.Fatalf("row 5 = %q", got)
	}
	saved := r.do(t, "0402")[0].data
	r.do(t, "042000"+"04110008"+"110101"+e("wide"))
	if r.screen.cols != 132 {
		t.Fatal("did not switch to 132")
	}
	r.proc.process(saved)
	if r.screen.cols != 80 || r.row(5)[:3] != " 1." {
		t.Errorf("cols %d row 5 %q", r.screen.cols, r.row(5)[:3])
	}
	// And a 27x132 screen comes back as 27x132.
	r.do(t, "042000"+"04110008"+"110101"+e("wide"))
	wide := r.do(t, "0402")[0].data
	r.do(t, "0440")
	r.proc.process(wide)
	if r.screen.cols != 132 || r.screen.rows != 27 {
		t.Errorf("size %dx%d", r.screen.rows, r.screen.cols)
	}
}

func TestRestoreScreenDropsFieldsShownInBetween(t *testing.T) {
	r := newRig("IBM-3477-FC")
	r.do(t, "04110008"+"110305"+e("Name:")+"1d4000200008"+e("Alex")+"20"+"130307")
	saved := r.do(t, "0402")[0].data
	// A message screen with a long field replaces the menu...
	r.do(t, "0440"+"04110008"+"110101"+"1d5000240099"+e("Message line"))
	if r.screen.fields[0].length != 0x99 {
		t.Fatal("message field missing")
	}
	// ...then the host restores the saved image.
	r.proc.process(saved)
	if len(r.screen.fields) != 1 || r.screen.fields[0].length != 8 {
		t.Errorf("fields = %d", len(r.screen.fields))
	}
	if strings.TrimSpace(r.row(0)) != "" || strings.TrimSpace(r.row(2)) != "Name: Alex" {
		t.Errorf("rows: %q / %q", r.row(0), r.row(2))
	}
}

// A full-screen format written without IC and never read, then a window: the host saves the
// screen (cursor still at 1,1), shows the window, and restores on F12.
func TestCursorAfterWindowCloses(t *testing.T) {
	format := "04110008" + "110305" + e("Name:") + "1d4000200008" + "20"
	window := "04110008" + "110a14" + e("Pick:") + "1d4000200003" + "20" + "130a1a" + "04520000"

	t.Run("READ after the restored image homes the cursor to the first input field", func(t *testing.T) {
		r := newRig("IBM-3477-FC")
		r.do(t, format)
		if r.screen.cursor != 0 {
			t.Fatal("cursor should still be home")
		}
		saved := r.do(t, "0402")[0].data
		r.do(t, window)
		if r.screen.cursor != r.screen.addr(9, 25) {
			t.Fatalf("window cursor %d", r.screen.cursor)
		}
		r.proc.process(saved) // Restore Screen replays IC 1,1
		r.do(t, "04520000")
		if r.screen.cursor != r.screen.fields[0].start {
			t.Errorf("cursor %d, want %d", r.screen.cursor, r.screen.fields[0].start)
		}
	})

	menu := "04110008" + "0107" + "00000018" + "000804" + "111415" + "1d4020200002" + "114443" + "1d4020200008"
	other := "04110008" + "0107" + "00000018" + "000804" + "110305" + e("Other:") + "1d4000200008" + "20" + "04520000"

	t.Run("a format rewritten after Restore Screen homes the cursor (menu after F3/F12)", func(t *testing.T) {
		r := newRig("IBM-3477-FC")
		r.do(t, menu+"04520000")
		if r.screen.cursor != r.screen.addr(19, 21) {
			t.Fatalf("menu cursor %d", r.screen.cursor)
		}
		r.screen.cursor = r.screen.addr(19, 22)
		saved := r.do(t, "0402")[0].data
		r.do(t, other)
		r.proc.process(saved)
		if r.screen.cursor != r.screen.addr(19, 22) {
			t.Fatalf("restored cursor %d", r.screen.cursor)
		}
		r.do(t, menu+"04520000")
		if r.screen.cursor != r.screen.addr(19, 21) {
			t.Errorf("cursor %d, want the first field", r.screen.cursor)
		}
	})

	t.Run("Restore Screen alone keeps the saved cursor when the host reads without a new header", func(t *testing.T) {
		r := newRig("IBM-3477-FC")
		r.do(t, menu+"04520000")
		r.screen.cursor = r.screen.addr(19, 22)
		saved := r.do(t, "0402")[0].data
		r.do(t, other)
		r.proc.process(saved)
		r.do(t, "04110000"+"04520000")
		if r.screen.cursor != r.screen.addr(19, 22) {
			t.Errorf("cursor %d", r.screen.cursor)
		}
	})

	t.Run("READ keeps a deliberate Insert Cursor on a protected cell", func(t *testing.T) {
		r := newRig("IBM-3477-FC")
		r.do(t, format+"130505"+"04520000")
		if r.screen.cursor != r.screen.addr(4, 4) {
			t.Errorf("cursor %d", r.screen.cursor)
		}
	})

	t.Run("READ on a screen without input fields leaves the cursor at the top-left corner", func(t *testing.T) {
		r := newRig("IBM-3477-FC")
		r.do(t, "04110008"+"110305"+e("Just text")+"04520000")
		if r.screen.cursor != 0 {
			t.Errorf("cursor %d", r.screen.cursor)
		}
	})
}

func TestSaveScreenKeepsCommandKeysAndErrorRow(t *testing.T) {
	r := newRig("IBM-3477-FC")
	soh := func(keys string) string { return "0107" + "00000018" + keys }
	r.do(t, "04110008"+soh("000804")+"110305"+e("Name:")+"1d4000200008")
	saved := r.do(t, "0402")[0].data
	r.do(t, "0440"+"04110008"+soh("000000")+"110101"+e("win"))
	if r.state.caKeys != 0 {
		t.Fatal("window screen has no CA keys")
	}
	r.proc.process(saved)
	if r.state.caKeys != (1<<2)|(1<<11) || r.screen.errorRow != 23 {
		t.Errorf("caKeys %#x errorRow %d", r.state.caKeys, r.screen.errorRow)
	}
}

func TestWriteErrorCodeKeepsOperatorCursor(t *testing.T) {
	r := newRig("IBM-3179-2")
	twoFields := "04110008" + sba(2, 2) + "1d4000200005" + sba(3, 2) + "1d4300200006" + "0452" + "0000"
	r.do(t, twoFields)
	r.screen.cursor = r.screen.fields[1].start
	here := r.screen.cursor
	// Write Error Code with a message, followed by the host's Read MDT.
	r.do(t, "0421"+e("Roll past the end")+"0452"+"0000")
	if r.state.errorMessage != "Roll past the end" {
		t.Errorf("message %q", r.state.errorMessage)
	}
	if got := strings.TrimSpace(r.row(r.screen.errorRow)); got != "Roll past the end" {
		t.Errorf("error row %q", got)
	}
	if r.screen.cursor != here {
		t.Errorf("cursor %d, want %d (not homed to the first field)", r.screen.cursor, here)
	}
}

func TestWriteErrorCodeWindowWritesBetweenColumns(t *testing.T) {
	r := newRig("IBM-3179-2")
	// A window whose left border sits in column 5 of the error row; the host says the error
	// line runs from column 7 to 40.
	r.do(t, "04110008"+sba(24, 1)+e("Roll")+sba(24, 5)+e("|")+sba(2, 2)+"1d4000200005"+"0452"+"0000")
	r.do(t, "0422"+"07"+"28"+e("Roll up or down past the first record")+"0452"+"0000")
	row := r.row(r.screen.errorRow)
	if row[:5] != "Roll|" || strings.TrimSpace(row[6:]) != "Roll up or down past the first record" {
		t.Errorf("row = %q", row)
	}
	if !strings.Contains(r.state.errorMessage, "Roll up or down past the first record") {
		t.Errorf("message %q", r.state.errorMessage)
	}
	// The repaint restores the whole row.
	r.do(t, "04110000"+sba(2, 10)+e("x")+"0452"+"0000")
	row = r.row(r.screen.errorRow)
	if row[:5] != "Roll|" || strings.TrimSpace(row[6:]) != "" {
		t.Errorf("after repaint: %q", row)
	}
}

func TestNextWriteTakesMessageOffTheErrorLine(t *testing.T) {
	r := newRig("IBM-3179-2")
	r.do(t, "04110008"+sba(24, 2)+e("F3=Exit")+sba(2, 2)+"1d4000200005"+"0452"+"0000")
	r.do(t, "0421"+e("Roll past the end")+"0452"+"0000")
	if strings.TrimSpace(r.row(r.screen.errorRow)) != "Roll past the end" {
		t.Fatal("message not shown")
	}
	// A valid page: the host rewrites row 2 only.
	r.do(t, "04110000"+sba(2, 10)+e("page 2")+"0452"+"0000")
	if got := strings.TrimSpace(r.row(r.screen.errorRow)); got != "F3=Exit" {
		t.Errorf("error row %q", got)
	}
	if r.state.errorMessage != "" {
		t.Errorf("message %q", r.state.errorMessage)
	}
}
