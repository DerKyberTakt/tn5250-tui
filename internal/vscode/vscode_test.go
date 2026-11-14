package vscode

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DerKyberTakt/tn5250-tui/internal/sites"
)

const sample = `{
  // a comment with "quotes" and a trailing comma below
  "editor.fontSize": 14, /* block
  comment */
  "tn5250.connection.defaultScreenSize": "24x80",
  "tn5250.connection.defaultCodePage": 500,
  "tn5250.connection.tls": true,
  "tn5250.connection.verifyCertificate": false,
  "tn5250.display.columnSeparators": "none",
  "tn5250.colors.green": "#00FF00",
  "tn5250.colors.red": "red",
  "tn5250.keyboard.keymap": { "Ctrl+Home": "home", "Escape": null, "Alt+F1": "help" },
  "tn5250.watermark.text": "DEV // not a comment",
  "tn5250.watermark.position": "bottomRight",
  "tn5250.sessions": [
    { "name": "pub", "host": "PUB400.COM", "port": 992, "signonUser": "ALEX", "screenSize": "27x132", "codePage": 37 },
    { "host": "127.0.0.1", "tls": false, "verifyCertificate": true, "demoMode": true },
    { "name": "nohost" },
  ],
}`

func TestParseJSONC(t *testing.T) {
	m, err := ParseJSONC([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	if m["editor.fontSize"] != 14.0 {
		t.Errorf("fontSize: %v", m["editor.fontSize"])
	}
	if m["tn5250.watermark.text"] != "DEV // not a comment" {
		t.Errorf("string with // kept: %v", m["tn5250.watermark.text"])
	}
	if _, err := ParseJSONC([]byte("  \n")); err != nil {
		t.Errorf("empty: %v", err)
	}
	if _, err := ParseJSONC([]byte("{ oops")); err == nil {
		t.Error("bad JSON accepted")
	}
}

func TestImport(t *testing.T) {
	m, err := ParseJSONC([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	r := Import(m)
	d := r.Defaults
	if d.ScreenSize != "24x80" || d.CodePage != 500 || !r.Found.Separators || d.Separators {
		t.Errorf("defaults: %+v found %+v", d, r.Found)
	}
	if d.Colors["green"] != "#00ff00" || len(d.Colors) != 1 {
		t.Errorf("colours: %v", d.Colors)
	}
	if d.Keys["Ctrl+Home"] != "home" || d.Keys["Alt+F1"] != "help" {
		t.Errorf("keys: %v", d.Keys)
	}
	if v, ok := d.Keys["Escape"]; !ok || v != "" {
		t.Errorf("null unbinds: %v", d.Keys)
	}
	if d.Watermark == nil || d.Watermark.Position != "bottomRight" {
		t.Errorf("watermark: %+v", d.Watermark)
	}
	if len(r.Sites) != 2 {
		t.Fatalf("sites: %+v", r.Sites)
	}
	pub := r.Sites[0]
	if pub.Name != "pub" || pub.Host != "PUB400.COM" || pub.Port != 992 || !pub.TLS || !pub.TrustAnyCertificate ||
		pub.User != "ALEX" || pub.ScreenSize != "27x132" || pub.CodePage != 37 || pub.Password != "" {
		t.Errorf("pub: %+v", pub)
	}
	loc := r.Sites[1]
	if loc.Name != "127.0.0.1" || loc.TLS || loc.TrustAnyCertificate || loc.Port != 0 || loc.ScreenSize != "" || !loc.Demo || loc.DemoLabel != "" {
		t.Errorf("local: %+v", loc)
	}
	joined := strings.Join(r.Notes, "\n")
	for _, want := range []string{"colour red", "session 3 has no host"} {
		if !strings.Contains(joined, want) {
			t.Errorf("notes miss %q: %v", want, r.Notes)
		}
	}
}

func TestImportImageWatermarkSkipped(t *testing.T) {
	r := Import(map[string]any{"tn5250.watermark.type": "image", "tn5250.watermark.text": "X"})
	if r.Found.Watermark || len(r.Notes) != 1 {
		t.Errorf("%+v", r)
	}
}

func TestApplyKeepsPasswords(t *testing.T) {
	f := &sites.File{Sites: []sites.Site{
		{Name: "pub", Host: "PUB400.COM", User: "ALEX", Password: "secret"},
		{Name: "same", Host: "PUB400.COM", User: "OLD", Password: "keep"},
	}, Defaults: &sites.Defaults{TraceFile: "t.log", SourceHighlight: true}}
	r := Result{Sites: []sites.Site{
		{Name: "PUB", Host: "PUB400.COM", Port: 992}, // no user: keeps ALEX/secret
		{Name: "same", Host: "PUB400.COM", User: "NEW"},
		{Name: "new", Host: "127.0.0.1"},
	}}
	r.Defaults.Colors = map[string]string{"green": "#00ff00"}
	r.Found.Colors = true
	added, updated := Apply(f, r)
	if added != 1 || updated != 2 {
		t.Errorf("added %d updated %d", added, updated)
	}
	s, _ := f.Find("pub")
	if s.User != "ALEX" || s.Password != "secret" || s.Port != 992 {
		t.Errorf("pub: %+v", s)
	}
	s, _ = f.Find("same")
	if s.User != "NEW" || s.Password != "" {
		t.Errorf("user changed must drop the password: %+v", s)
	}
	if f.Defaults.TraceFile != "t.log" || f.Defaults.Colors["green"] != "#00ff00" || !f.Defaults.SourceHighlight {
		t.Errorf("defaults: %+v", f.Defaults)
	}
	if msg := Summary(r, added, updated); !strings.Contains(msg, "3 sites (1 new, 2 updated)") || !strings.Contains(msg, "colours") {
		t.Errorf("summary: %s", msg)
	}
	if msg := Summary(Result{}, 0, 0); !strings.HasPrefix(msg, "Nothing") {
		t.Errorf("empty summary: %s", msg)
	}
}

func TestImportFileAndCandidates(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(p, []byte(`{"tn5250.sessions":[{"host":"PUB400.COM"}],}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvSettings, p)
	if c := Candidates(); len(c) != 1 || c[0] != p {
		t.Errorf("candidates: %v", c)
	}
	r, from, err := ImportFile("")
	if err != nil || from != p || len(r.Sites) != 1 {
		t.Errorf("%v %s %+v", err, from, r)
	}
	t.Setenv(EnvSettings, filepath.Join(dir, "missing.json"))
	if _, _, err := ImportFile(""); err == nil {
		t.Error("missing file accepted")
	}
	t.Setenv(EnvSettings, "")
	if c := Candidates(); len(c) < 4 || !strings.HasSuffix(c[0], filepath.Join("Code", "User", "settings.json")) {
		t.Errorf("platform candidates: %v", c)
	}
}
