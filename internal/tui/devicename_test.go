package tui

import "testing"

func TestNextDeviceName(t *testing.T) {
	cases := []struct {
		template string
		attempt  int
		want     string
	}{
		// '*' = a letter per attempt
		{"TESTPC*", 0, "TESTPCA"},
		{"TESTPC*", 1, "TESTPCB"},
		{"TESTPC*", 25, "TESTPCZ"},
		{"TESTPC*", 26, ""},
		{"*", 2, "C"},
		// '=' = a digit per attempt
		{"PC=", 0, "PC0"},
		{"PC=", 1, "PC1"},
		{"PC=", 9, "PC9"},
		{"PC=", 10, ""},
		// both: letter then digit, TypeScript order
		{"DEV*=", 0, "DEVA0"},
		{"DEV*=", 11, "DEVL1"},
		// plain name: the name, then a numeric suffix 1..9
		{"testpc", 0, "TESTPC"},
		{"TESTPC", 1, "TESTPC1"},
		{"TESTPC", 9, "TESTPC9"},
		{"TESTPC", 10, ""},
		// the base is cut to make room for the suffix (10 characters at most)
		{"ABCDEFGHIJ", 0, "ABCDEFGHIJ"},
		{"ABCDEFGHIJ", 3, "ABCDEFGHI3"},
		{"ABCDEFGHI*", 1, "ABCDEFGHIB"},
		{"ABCDEFGH*=", 1, "ABCDEFGHB1"},
		// template characters other than letters and digits are dropped; spaces trimmed
		{"  my-pc*  ", 0, "MYPCA"},
		// nothing to try
		{"", 1, ""},
		{"   ", 0, ""},
		{"TESTPC", -1, ""},
	}
	for _, c := range cases {
		if got := nextDeviceName(c.template, c.attempt); got != c.want {
			t.Errorf("nextDeviceName(%q, %d) = %q, want %q", c.template, c.attempt, got, c.want)
		}
	}
}

func TestNextDeviceNameNeverRepeats(t *testing.T) {
	for _, tpl := range []string{"TESTPC", "PC=", "PC*", "PC*="} {
		seen := map[string]int{}
		for a := 0; ; a++ {
			n := nextDeviceName(tpl, a)
			if n == "" {
				break
			}
			if len(n) > maxDeviceName {
				t.Errorf("%s attempt %d: %q is longer than %d", tpl, a, n, maxDeviceName)
			}
			if prev, dup := seen[n]; dup {
				t.Fatalf("%s: attempts %d and %d both give %q", tpl, prev, a, n)
			}
			seen[n] = a
		}
		if len(seen) < 10 {
			t.Errorf("%s: only %d names", tpl, len(seen))
		}
	}
}

func TestExpandDeviceVars(t *testing.T) {
	if got := expandDeviceVars("TESTPC"); got != "TESTPC" {
		t.Errorf("no variables: %q", got)
	}
	got := expandDeviceVars("&compn-&COMPN&usern")
	if got == "&compn-&COMPN&usern" || got == "" {
		t.Errorf("variables not replaced: %q", got)
	}
	if want := replaceFold("a&Xb&xc", "&x", "-"); want != "a-b-c" {
		t.Errorf("replaceFold = %q", want)
	}
}
