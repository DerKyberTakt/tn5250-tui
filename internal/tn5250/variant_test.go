package tn5250

import (
	"reflect"
	"strings"
	"testing"

	"github.com/DerKyberTakt/tn5250-tui/internal/ebcdic"
)

var cp273 = ebcdic.MustGet(273)

// Ported from tn5250-terminal test/engine/variant.test.ts (issue #5: ! typed under code page
// 37 reaches a 273 host as U-umlaut).
func TestVariantBytes(t *testing.T) {
	for b, want := range map[byte]bool{
		0x5a: true,  // ! in 37, U-umlaut in 273, ] in 500
		0x7c: true,  // @ in 37, O-umlaut in 273
		0x4f: true,  // | in 37, ! in 273
		0x7b: true,  // # in 37, AE in 1142
		0x81: false, // a
		0xf1: false, // 1
		0x4b: false, // .
		0x40: false, // blank
		0x00: false, // null
		0x1c: false, // control
	} {
		if got := IsVariantByte(b); got != want {
			t.Errorf("IsVariantByte(%#x) = %v, want %v", b, got, want)
		}
	}
}

func TestVariantCharsOfAPasswordAsTyped(t *testing.T) {
	for _, c := range []struct {
		name string
		text string
		want []string
	}{
		{"two variants once each, in order", "pa!s@w!rd", []string{"!", "@"}},
		{"none", "Passw0rd.", nil},
		{"all the brackets", `a[b]{c}|d\e^f~g`, []string{"[", "]", "{", "}", "|", `\`, "^", "~"}},
	} {
		if got := VariantChars(cp37.EncodeString(c.text), cp37, 0); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: VariantChars(%q) = %q, want %q", c.name, c.text, got, c.want)
		}
	}
}

func TestVariantCharsDecodeWithTheSessionCodePage(t *testing.T) {
	// ! under 273 is a variant too.
	if got := VariantChars(cp273.EncodeString("ge!heim"), cp273, 0); !reflect.DeepEqual(got, []string{"!"}) {
		t.Errorf("273: %q", got)
	}
	if b := cp273.EncodeString("!")[0]; b != 0x4f {
		t.Errorf("! in 273 is %#x, want 0x4f", b)
	}
	if got := cp273.Decode(0x5a); got != 'Ü' {
		t.Errorf("0x5a in 273 = %q", got)
	}
}

func TestVariantCharsSkipNullsAndBlanksAndCap(t *testing.T) {
	if got := VariantChars([]byte{0x00, 0x00, 0x5a, 0x40, 0x00}, cp37, 0); !reflect.DeepEqual(got, []string{"!"}) {
		t.Errorf("padded field: %q", got)
	}
	if got := VariantChars(cp37.EncodeString(`!@#$[]{}|\^~`), cp37, 3); !reflect.DeepEqual(got, []string{"!", "@", "#"}) {
		t.Errorf("limit 3: %q", got)
	}
	if got := VariantChars(cp37.EncodeString(`!@#$[]{}|\^~`), cp37, 0); len(got) != DefaultVariantLimit {
		t.Errorf("default limit: %d chars", len(got))
	}
}

// signOnShape writes a sign-on style screen: a title row, a visible user field and a
// non-display password field. user and password are typed into the fields.
func signOnShape(h *harness, title string) {
	h.host("04110008" + sba(2, 30) + e(title) +
		sba(6, 20) + e("User . . . .") + sba(6, 40) + "1d4000200010" +
		sba(8, 20) + e("Password . .") + sba(8, 40) + "1d4800270010" + "04520000")
}

func TestSignonProbeListsVariantCharsNeverThePassword(t *testing.T) {
	h := newHarness(t, Options{CodePage: 37})
	signOnShape(h, "Sign On")
	if p := h.session.SignonProbe(); !p.Screen || len(p.VariantChars) != 0 || p.CodePage != 37 {
		t.Fatalf("empty password: %+v", p)
	}
	h.session.Press(ActionTab)
	h.session.Type("pa!s@w!rd")
	p := h.session.SignonProbe()
	if !p.Screen || !reflect.DeepEqual(p.VariantChars, []string{"!", "@"}) {
		t.Fatalf("probe = %+v", p)
	}
	if strings.Contains(strings.Join(p.VariantChars, ""), "p") {
		t.Error("probe leaked password characters")
	}
	// Plain password: sign-on screen, nothing variant.
	h2 := newHarness(t, Options{CodePage: 273})
	signOnShape(h2, "Sign On")
	h2.session.Press(ActionTab)
	h2.session.Type("Passw0rd")
	if p := h2.session.SignonProbe(); !p.Screen || len(p.VariantChars) != 0 || p.CodePage != 273 {
		t.Fatalf("plain password: %+v", p)
	}
}

func TestSignonProbeNeedsTheSignonShape(t *testing.T) {
	// Customised screen without the words but with the shape: user field first, one hidden field.
	h := newHarness(t, Options{})
	signOnShape(h, "Welcome")
	if !h.session.SignonProbe().Screen {
		t.Error("shape without the title should count")
	}
	// Password field first and no "Sign On" title: not a sign-on screen.
	h2 := newHarness(t, Options{})
	h2.host("04110008" + sba(2, 30) + e("Welcome") +
		sba(6, 40) + "1d4800270010" + sba(8, 40) + "1d4000200010" + "04520000")
	if h2.session.SignonProbe().Screen {
		t.Error("hidden field first without the title is not a sign-on screen")
	}
	// ... but with the title it is.
	h3 := newHarness(t, Options{})
	h3.host("04110008" + sba(2, 30) + e("Sign On") +
		sba(6, 40) + "1d4800270010" + sba(8, 40) + "1d4000200010" + "04520000")
	if !h3.session.SignonProbe().Screen {
		t.Error("title makes it a sign-on screen")
	}
	// A Change Password screen has three hidden fields.
	h4 := newHarness(t, Options{})
	h4.host("04110008" + sba(2, 30) + e("Change Password") +
		sba(6, 40) + "1d4000200010" + sba(7, 40) + "1d4800270010" + sba(8, 40) + "1d4800270010" +
		sba(9, 40) + "1d4800270010" + "04520000")
	if h4.session.SignonProbe().Screen {
		t.Error("three hidden fields is a change-password screen")
	}
	// A menu: no fields.
	h5 := newHarness(t, Options{})
	h5.host("04110008" + sba(1, 1) + e("Main menu") + "04520000")
	if h5.session.SignonProbe().Screen {
		t.Error("menu is not a sign-on screen")
	}
}
