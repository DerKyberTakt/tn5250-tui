package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveViewerToken(t *testing.T) {
	none := func(string) string { return "" }
	env := func(k string) string {
		if k == EnvViewerToken {
			return " envtok "
		}
		return ""
	}
	file := filepath.Join(t.TempDir(), "tok")
	if err := os.WriteFile(file, []byte("filetok\nignored\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	empty := filepath.Join(t.TempDir(), "empty")
	if err := os.WriteFile(empty, []byte("\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, flag, file string
		getenv           func(string) string
		want             string
		fail             bool
	}{
		{"flag still works", "flagtok", "", none, "flagtok", false},
		{"env wins over the flag", "flagtok", "", env, "envtok", false},
		{"file wins over both", "flagtok", file, env, "filetok", false},
		{"missing file", "", filepath.Join(t.TempDir(), "nope"), none, "", true},
		{"empty file", "", empty, none, "", true},
	} {
		got, err := resolveViewerToken(tc.flag, tc.file, tc.getenv)
		if (err != nil) != tc.fail || got != tc.want {
			t.Errorf("%s: got %q, %v", tc.name, got, err)
		}
	}
}
