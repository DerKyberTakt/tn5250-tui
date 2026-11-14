package sites

import (
	"os"
	"path/filepath"
	"runtime"
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
