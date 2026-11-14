package sites

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "sub", "sites.json")
	t.Setenv(EnvConfig, p)
	if Path() != p {
		t.Fatalf("Path = %q", Path())
	}
	f, err := Load()
	if err != nil || len(f.Sites) != 0 {
		t.Fatalf("missing file: %v %v", f, err)
	}
	f.Upsert(Site{Name: "pub400", Host: "PUB400.COM", Port: 992, TLS: true, User: "u", Password: "p", ScreenSize: "27x132", CodePage: 37, AutoSignon: true})
	f.Upsert(Site{Name: "other", Host: "PUB400.COM"})
	f.LastUsed = "pub400"
	if err := f.Save(); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		st, _ := os.Stat(p)
		if st.Mode().Perm() != 0o600 {
			t.Fatalf("mode %v", st.Mode())
		}
	}
	g, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	s, ok := g.Find("PUB400")
	if !ok || s.Host != "PUB400.COM" || s.Port != 992 || !s.TLS || s.Password != "p" || !s.AutoSignon || s.ScreenSize != "27x132" {
		t.Fatalf("got %+v ok=%v", s, ok)
	}
	if g.LastUsed != "pub400" || len(g.Sites) != 2 {
		t.Fatalf("file %+v", g)
	}
}

func TestUpsertDelete(t *testing.T) {
	f := &File{}
	f.Upsert(Site{Name: "a", Host: "h1"})
	f.Upsert(Site{Name: "A", Host: "h2"})
	if len(f.Sites) != 1 || f.Sites[0].Host != "h2" {
		t.Fatalf("%+v", f.Sites)
	}
	f.LastUsed = "a"
	if !f.Delete("a") || len(f.Sites) != 0 || f.LastUsed != "" {
		t.Fatalf("%+v", f)
	}
	if f.Delete("a") {
		t.Fatal("second delete")
	}
}

func TestBadJSON(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.json")
	_ = os.WriteFile(p, []byte("{nope"), 0o600)
	if _, err := LoadFrom(p); err == nil {
		t.Fatal("expected error")
	}
}

func TestWithDefaults(t *testing.T) {
	s := Site{Host: "x", TLS: true}.WithDefaults()
	if s.Port != 992 || s.ScreenSize != "27x132" || s.CodePage != 37 {
		t.Fatalf("%+v", s)
	}
	if (Site{}).WithDefaults().Port != 23 {
		t.Fatal("plain port")
	}
}

func TestPathDefaults(t *testing.T) {
	t.Setenv(EnvConfig, "")
	if runtime.GOOS == "windows" {
		t.Setenv("APPDATA", `C:\x`)
		if Path() != `C:\x\tn5250-tui\sites.json` {
			t.Fatal(Path())
		}
		return
	}
	t.Setenv("XDG_CONFIG_HOME", "/xdg")
	if Path() != "/xdg/tn5250-tui/sites.json" {
		t.Fatal(Path())
	}
}

func TestDefaultsRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.json")
	f := &File{}
	f.Defaults = &Defaults{ScreenSize: "24x80", CodePage: 273, DeviceName: "KW%n", Separators: true,
		SourceHighlight: false, AutoSignon: true, TraceFile: "t.log", KeepAliveSeconds: 30}
	if err := f.SaveTo(p); err != nil {
		t.Fatal(err)
	}
	g, err := LoadFrom(p)
	if err != nil {
		t.Fatal(err)
	}
	if g.Defaults == nil || *g.Defaults != *f.Defaults {
		t.Fatalf("got %+v want %+v", g.Defaults, f.Defaults)
	}
}

func TestOldFileWithoutDefaultsLoads(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.json")
	_ = os.WriteFile(p, []byte(`{"sites":[{"name":"a","host":"PUB400.COM"}],"lastUsed":"a"}`), 0o600)
	f, err := LoadFrom(p)
	if err != nil || f.Defaults != nil || len(f.Sites) != 1 {
		t.Fatalf("%+v %v", f, err)
	}
	d := f.EffectiveDefaults()
	if d.ScreenSize != "27x132" || d.CodePage != 37 || !d.SourceHighlight || d.Separators || d.KeepAliveSeconds != 0 {
		t.Fatalf("%+v", d)
	}
	// saving an untouched file does not invent the block
	_ = f.SaveTo(p)
	if b, _ := os.ReadFile(p); strings.Contains(string(b), "defaults") {
		t.Fatalf("block written: %s", b)
	}
}

func TestDefaultsBlockKeepsHighlightOnWhenKeyMissing(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.json")
	_ = os.WriteFile(p, []byte(`{"sites":[],"defaults":{"screenSize":"24x80"}}`), 0o600)
	f, err := LoadFrom(p)
	if err != nil || f.Defaults == nil || !f.Defaults.SourceHighlight || f.Defaults.ScreenSize != "24x80" {
		t.Fatalf("%+v %v", f.Defaults, err)
	}
}

func TestFileDefaultsBeforeBuiltin(t *testing.T) {
	f := &File{Defaults: &Defaults{ScreenSize: "24x80", CodePage: 273, DeviceName: "KW"}}
	s := f.Complete(Site{Host: "h"})
	if s.ScreenSize != "24x80" || s.CodePage != 273 || s.DeviceName != "KW" || s.Port != 23 {
		t.Fatalf("%+v", s)
	}
	// the site's own values win
	s = f.Complete(Site{Host: "h", ScreenSize: "27x132", CodePage: 37, DeviceName: "X", TLS: true})
	if s.ScreenSize != "27x132" || s.CodePage != 37 || s.DeviceName != "X" || s.Port != 992 {
		t.Fatalf("%+v", s)
	}
	// a block with empty size / code page falls back to the built-in values
	g := &File{Defaults: &Defaults{}}
	s = g.Complete(Site{Host: "h"})
	if s.ScreenSize != "27x132" || s.CodePage != 37 {
		t.Fatalf("%+v", s)
	}
	var nilFile *File
	if nilFile.Complete(Site{}).ScreenSize != "27x132" {
		t.Fatal("nil file")
	}
}
