// Package vscode imports the settings of the tn5250-terminal VS Code extension (and the
// tn5250-desktop app, which shares them) into the sites file: the saved sessions become
// sites, the defaults, colours, key overrides and the watermark become the defaults block.
// The extension's settings.json is only read, never written, and passwords never come
// across: VS Code keeps them in its secret store, which no other program can read.
package vscode

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"

	"github.com/DerKyberTakt/tn5250-tui/internal/sites"
)

// EnvSettings overrides the settings.json path.
const EnvSettings = "TN5250_VSCODE_SETTINGS"

// Candidates lists the settings.json files that may exist, most likely first: the env
// override, then VS Code, VS Code Insiders, VSCodium and Cursor for this platform.
func Candidates() []string {
	if p := os.Getenv(EnvSettings); p != "" {
		return []string{p}
	}
	var bases []string
	switch runtime.GOOS {
	case "windows":
		base := os.Getenv("APPDATA")
		if base == "" {
			if home, err := os.UserHomeDir(); err == nil {
				base = filepath.Join(home, "AppData", "Roaming")
			}
		}
		bases = []string{base}
	case "darwin":
		home, _ := os.UserHomeDir()
		bases = []string{filepath.Join(home, "Library", "Application Support")}
	default:
		base := os.Getenv("XDG_CONFIG_HOME")
		if base == "" {
			home, _ := os.UserHomeDir()
			base = filepath.Join(home, ".config")
		}
		bases = []string{base}
	}
	var out []string
	for _, b := range bases {
		for _, app := range []string{"Code", "Code - Insiders", "VSCodium", "Cursor"} {
			out = append(out, filepath.Join(b, app, "User", "settings.json"))
		}
	}
	return out
}

// Find returns the first candidate that exists.
func Find() (string, error) {
	for _, p := range Candidates() {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, nil
		}
	}
	return "", errors.New("no VS Code settings.json found (set " + EnvSettings + " or give the path)")
}

// ParseJSONC parses a settings.json: JSON with // and /* */ comments and trailing commas.
func ParseJSONC(data []byte) (map[string]any, error) {
	clean := stripJSONC(data)
	var m map[string]any
	if len(strings.TrimSpace(string(clean))) == 0 {
		return map[string]any{}, nil
	}
	if err := json.Unmarshal(clean, &m); err != nil {
		return nil, err
	}
	if m == nil {
		m = map[string]any{}
	}
	return m, nil
}

// stripJSONC removes comments (outside strings) and the comma before a closing ] or }.
func stripJSONC(src []byte) []byte {
	out := make([]byte, 0, len(src))
	inStr := false
	for i := 0; i < len(src); i++ {
		c := src[i]
		switch {
		case inStr:
			out = append(out, c)
			if c == '\\' && i+1 < len(src) {
				i++
				out = append(out, src[i])
			} else if c == '"' {
				inStr = false
			}
		case c == '"':
			inStr = true
			out = append(out, c)
		case c == '/' && i+1 < len(src) && src[i+1] == '/':
			for i < len(src) && src[i] != '\n' {
				i++
			}
			if i < len(src) {
				out = append(out, '\n')
			}
		case c == '/' && i+1 < len(src) && src[i+1] == '*':
			i += 2
			for i+1 < len(src) && !(src[i] == '*' && src[i+1] == '/') {
				i++
			}
			i++
		default:
			out = append(out, c)
		}
	}
	return trailingCommas.ReplaceAll(out, []byte("$1"))
}

var trailingCommas = regexp.MustCompile(`,(\s*[\]}])`)

// Result is what an import found.
type Result struct {
	// Defaults holds the imported default values; Found says which parts were present.
	Defaults sites.Defaults
	Found    struct {
		ScreenSize, CodePage, DeviceName, Separators, AutoSignon, Colors, Keys, Watermark bool
	}
	// Sites are the saved sessions, passwords empty.
	Sites []sites.Site
	// Notes explain what was skipped and why.
	Notes []string
}

// Import reads the tn5250.* keys of a parsed settings.json.
func Import(m map[string]any) Result {
	var r Result
	g := func(key string) (any, bool) {
		v, ok := m["tn5250."+key]
		return v, ok
	}
	if v, ok := g("connection.defaultScreenSize"); ok {
		if s, ok := v.(string); ok && (s == "24x80" || s == "27x132") {
			r.Defaults.ScreenSize, r.Found.ScreenSize = s, true
		}
	}
	if n, ok := number(g("connection.defaultCodePage")); ok && n > 0 {
		r.Defaults.CodePage, r.Found.CodePage = n, true
	}
	if s, ok := str(g("connection.deviceName")); ok {
		r.Defaults.DeviceName, r.Found.DeviceName = s, true
	}
	if s, ok := str(g("display.columnSeparators")); ok {
		r.Defaults.Separators, r.Found.Separators = s != "none", true
	}
	if b, ok := boolean(g("connection.autoSignon")); ok {
		r.Defaults.AutoSignon, r.Found.AutoSignon = b, true
	}
	for _, name := range sites.ColorNames {
		if s, ok := str(g("colors." + name)); ok {
			if !hexColour.MatchString(s) {
				r.Notes = append(r.Notes, "colour "+name+": "+s+" is not #rrggbb, skipped")
				continue
			}
			if r.Defaults.Colors == nil {
				r.Defaults.Colors = map[string]string{}
			}
			r.Defaults.Colors[name] = strings.ToLower(s)
			r.Found.Colors = true
		}
	}
	if v, ok := g("keyboard.keymap"); ok {
		if km, ok := v.(map[string]any); ok {
			keys := map[string]string{}
			for k, a := range km {
				switch a := a.(type) {
				case nil:
					keys[k] = ""
				case string:
					keys[k] = a
				}
			}
			if len(keys) > 0 {
				r.Defaults.Keys, r.Found.Keys = keys, true
			}
		}
	}
	if text, ok := str(g("watermark.text")); ok && strings.TrimSpace(text) != "" {
		typ, _ := str(g("watermark.type"))
		if typ == "image" {
			r.Notes = append(r.Notes, "watermark: an image cannot be drawn in a terminal, skipped")
		} else {
			pos, _ := str(g("watermark.position"))
			r.Defaults.Watermark = &sites.Watermark{Text: text, Position: pos}
			r.Found.Watermark = true
		}
	}

	// Saved sessions: an item stores only what differs from the defaults; the globals fill
	// the rest, as the extension does.
	defTLS, _ := boolean(g("connection.tls"))
	defPort, _ := number(g("connection.defaultPort"))
	defVerify, hasVerify := boolean(g("connection.verifyCertificate"))
	if !hasVerify {
		defVerify = true
	}
	defUser, _ := str(g("connection.signonUser"))
	defDemo, _ := boolean(g("connection.demoMode"))
	defDemoLabel, _ := str(g("connection.demoLabel"))
	if v, ok := g("sessions"); ok {
		items, _ := v.([]any)
		for i, it := range items {
			item, ok := it.(map[string]any)
			if !ok {
				continue
			}
			host, _ := str(item["host"], true)
			host = strings.TrimSpace(host)
			if host == "" {
				r.Notes = append(r.Notes, fmt.Sprintf("session %d has no host, skipped", i+1))
				continue
			}
			s := sites.Site{Host: host}
			if name, ok := str(item["name"], true); ok && strings.TrimSpace(name) != "" {
				s.Name = strings.TrimSpace(name)
			} else {
				s.Name = host
			}
			s.TLS = pick(boolean(item["tls"], true))(defTLS)
			s.Port = pickInt(number(item["port"], true))(defPort)
			verify := pick(boolean(item["verifyCertificate"], true))(defVerify)
			s.TrustAnyCertificate = !verify
			if v, ok := str(item["screenSize"], true); ok && (v == "24x80" || v == "27x132") {
				s.ScreenSize = v
			}
			if n, ok := number(item["codePage"], true); ok && n > 0 {
				s.CodePage = n
			}
			s.DeviceName, _ = str(item["deviceName"], true)
			s.AutoSignon = pick(boolean(item["autoSignon"], true))(r.Defaults.AutoSignon)
			s.User = strings.TrimSpace(pickStr(str(item["signonUser"], true))(defUser))
			s.Demo = pick(boolean(item["demoMode"], true))(defDemo)
			if s.Demo {
				s.DemoLabel = pickStr(str(item["demoLabel"], true))(defDemoLabel)
			}
			r.Sites = append(r.Sites, s)
		}
	}
	sort.Strings(r.Notes)
	return r
}

var hexColour = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// Apply merges r into f: the found defaults replace the file's, the sites are upserted by
// name keeping an existing site's password (and its user when the import has none). A site
// whose host, port or TLS setting changes (or that turns certificate checking off, or
// changes its user) loses its saved password: it must not follow the site to a new
// endpoint. It returns how many sites were added and updated, and one line per site whose
// password was dropped, saying what changed.
func Apply(f *sites.File, r Result) (added, updated int, cleared []string) {
	d := f.EffectiveDefaults()
	if r.Found.ScreenSize {
		d.ScreenSize = r.Defaults.ScreenSize
	}
	if r.Found.CodePage {
		d.CodePage = r.Defaults.CodePage
	}
	if r.Found.DeviceName {
		d.DeviceName = r.Defaults.DeviceName
	}
	if r.Found.Separators {
		d.Separators = r.Defaults.Separators
	}
	if r.Found.AutoSignon {
		d.AutoSignon = r.Defaults.AutoSignon
	}
	if r.Found.Colors {
		d.Colors = r.Defaults.Colors
	}
	if r.Found.Keys {
		d.Keys = r.Defaults.Keys
	}
	if r.Found.Watermark {
		d.Watermark = r.Defaults.Watermark
	}
	f.Defaults = &d
	for _, s := range r.Sites {
		if old, ok := f.Find(s.Name); ok {
			s.Password = old.Password
			if s.User == "" {
				s.User = old.User
			}
			if what := endpointChange(f, old, s); len(what) > 0 {
				note := "no saved password to clear"
				if old.Password != "" {
					note = "saved password cleared"
				}
				cleared = append(cleared, fmt.Sprintf("site %s changed: %s; %s", old.Name, strings.Join(what, ", "), note))
				s.Password = ""
			}
			updated++
		} else {
			added++
		}
		f.Upsert(s)
	}
	return added, updated, cleared
}

// endpointChange lists what an import changes about where a site's credentials would go.
func endpointChange(f *sites.File, old, nw sites.Site) []string {
	var what []string
	if !strings.EqualFold(old.Host, nw.Host) {
		what = append(what, fmt.Sprintf("host %s -> %s", old.Host, nw.Host))
	}
	op := f.Complete(sites.Site{Port: old.Port, TLS: old.TLS}).Port
	np := f.Complete(sites.Site{Port: nw.Port, TLS: nw.TLS}).Port
	if op != np {
		what = append(what, fmt.Sprintf("port %d -> %d", op, np))
	}
	if old.TLS != nw.TLS {
		what = append(what, fmt.Sprintf("tls %s -> %s", onOff(old.TLS), onOff(nw.TLS)))
	}
	if !old.TrustAnyCertificate && nw.TrustAnyCertificate {
		what = append(what, "certificate check off")
	}
	if nw.User != old.User {
		what = append(what, fmt.Sprintf("user %s -> %s", old.User, nw.User))
	}
	return what
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

// Summary is a one-line account of an import for the status line.
func Summary(r Result, added, updated int) string {
	var parts []string
	if n := len(r.Sites); n > 0 {
		parts = append(parts, fmt.Sprintf("%d sites (%d new, %d updated)", n, added, updated))
	}
	var defs []string
	if r.Found.ScreenSize || r.Found.CodePage || r.Found.DeviceName || r.Found.Separators || r.Found.AutoSignon {
		defs = append(defs, "defaults")
	}
	if r.Found.Colors {
		defs = append(defs, "colours")
	}
	if r.Found.Keys {
		defs = append(defs, fmt.Sprintf("%d keys", len(r.Defaults.Keys)))
	}
	if r.Found.Watermark {
		defs = append(defs, "watermark")
	}
	if len(defs) > 0 {
		parts = append(parts, strings.Join(defs, ", "))
	}
	if len(parts) == 0 {
		return "Nothing to import (no tn5250.* settings)"
	}
	msg := "Imported " + strings.Join(parts, "; ")
	if len(r.Notes) > 0 {
		msg += fmt.Sprintf(" (%d skipped)", len(r.Notes))
	}
	return msg
}

// ImportFile reads path (or the first candidate when path is "") and returns the result
// and the file it read.
func ImportFile(path string) (Result, string, error) {
	var err error
	if path == "" {
		path, err = Find()
		if err != nil {
			return Result{}, "", err
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Result{}, path, err
	}
	m, err := ParseJSONC(data)
	if err != nil {
		return Result{}, path, fmt.Errorf("%s: %w", path, err)
	}
	return Import(m), path, nil
}

// Value helpers: a trailing true means "the key may be absent" (an item override).

func str(v any, _ ...bool) (string, bool) {
	s, ok := v.(string)
	return s, ok
}

func number(v any, _ ...bool) (int, bool) {
	switch n := v.(type) {
	case float64:
		return int(n), true
	case string:
		var i int
		if _, err := fmt.Sscanf(strings.TrimSpace(n), "%d", &i); err == nil {
			return i, true
		}
	}
	return 0, false
}

func boolean(v any, _ ...bool) (bool, bool) {
	b, ok := v.(bool)
	return b, ok
}

func pick(v bool, ok bool) func(def bool) bool {
	return func(def bool) bool {
		if ok {
			return v
		}
		return def
	}
}

func pickInt(v int, ok bool) func(def int) int {
	return func(def int) int {
		if ok {
			return v
		}
		return def
	}
}

func pickStr(v string, ok bool) func(def string) string {
	return func(def string) string {
		if ok {
			return v
		}
		return def
	}
}
