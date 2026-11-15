// Package sites stores the list of 5250 systems the terminal knows about as a JSON file.
// Passwords are not kept in it: Load opens them from the secrets store (OS keyring, or an
// encrypted file where there is none) and Save seals them back, so in memory a Site always
// carries the plain password and on disk only a marker or ciphertext (see package secrets).
package sites

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/DerKyberTakt/tn5250-tui/internal/secrets"
)

var (
	storeMu sync.Mutex
	store   secrets.Store
)

// Secrets returns the password store for the sites file's directory.
func Secrets() secrets.Store {
	storeMu.Lock()
	defer storeMu.Unlock()
	if store == nil {
		store = secrets.New(filepath.Dir(Path()))
	}
	return store
}

// UseStore replaces the password store (tests, or a caller with its own).
func UseStore(s secrets.Store) {
	storeMu.Lock()
	defer storeMu.Unlock()
	store = s
}

// EnvConfig overrides the sites file path (used by tests).
const EnvConfig = "TN5250_TUI_CONFIG"

// Site is one saved connection target.
type Site struct {
	Name                string `json:"name"`
	Host                string `json:"host"`
	Port                int    `json:"port"`
	TLS                 bool   `json:"tls"`
	TrustAnyCertificate bool   `json:"trustAnyCertificate"`
	User                string `json:"user"`
	Password            string `json:"password"`
	ScreenSize          string `json:"screenSize"` // "24x80" or "27x132"
	CodePage            int    `json:"codePage"`
	DeviceName          string `json:"deviceName"`
	AutoSignon          bool   `json:"autoSignon"`
	// Demo marks a demo/test system: a tiled DEMO watermark and DEMO on the status line
	// (the emulators' demo mode). DemoLabel replaces the word DEMO.
	Demo      bool   `json:"demo,omitempty"`
	DemoLabel string `json:"demoLabel,omitempty"`
}

// sealed remembers the stored form last written for a site's password, so Save does not
// rewrite the keyring entry when nothing changed.
type sealed struct{ plain, stored string }

// Watermark is a text drawn in a dim colour on the blank parts of the screen.
type Watermark struct {
	Text string `json:"text"`
	// Position is "center" (default), "topLeft", "topRight", "bottomLeft", "bottomRight" or
	// "tile" (the emulators' names; "custom" draws as "center").
	Position string `json:"position,omitempty"`
}

// ColorNames are the 5250 attribute colours a palette entry can override.
var ColorNames = []string{"green", "white", "red", "turquoise", "yellow", "pink", "blue"}

// Defaults are the settings used where a site (or a host target) leaves a value empty. They
// are stored in sites.json as "defaults" and edited with the :settings page.
type Defaults struct {
	ScreenSize       string `json:"screenSize,omitempty"` // "24x80" or "27x132"
	CodePage         int    `json:"codePage,omitempty"`
	DeviceName       string `json:"deviceName,omitempty"`       // template, see the device name variables
	Separators       bool   `json:"separators"`                 // draw column separators
	SourceHighlight  bool   `json:"sourceHighlight"`            // SEU syntax colouring (default true)
	AutoSignon       bool   `json:"autoSignon"`                 // preset for new sites
	TraceFile        string `json:"traceFile,omitempty"`        // trace every connection to this file
	KeepAliveSeconds int    `json:"keepAliveSeconds,omitempty"` // 0 = the client's 15 s default
	// Colors overrides the palette: attribute colour name (see ColorNames) -> "#rrggbb".
	Colors map[string]string `json:"colors,omitempty"`
	// Keys overrides the key table with the emulators' key names ("Ctrl+Home", "Alt+F1",
	// "Shift+Escape") -> engine action; "" unbinds the key. Names the console cannot
	// deliver are ignored.
	Keys map[string]string `json:"keys,omitempty"`
	// Watermark, when set with a text, is drawn on the blank parts of every screen.
	Watermark *Watermark `json:"watermark,omitempty"`
}

// UnmarshalJSON starts from the built-in values, so a block without "sourceHighlight" keeps
// the colouring on.
func (d *Defaults) UnmarshalJSON(b []byte) error {
	type plain Defaults
	v := plain{SourceHighlight: true}
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	*d = Defaults(v)
	return nil
}

// File is the content of sites.json.
type File struct {
	Sites    []Site    `json:"sites"`
	LastUsed string    `json:"lastUsed"`
	Defaults *Defaults `json:"defaults,omitempty"`
	// Warnings collects what Load could not do (a password the store has lost); show them once.
	Warnings []string `json:"-"`

	sealedForms map[string]sealed
}

// Built-in defaults.
const (
	BuiltinScreenSize = "27x132"
	BuiltinCodePage   = 37
)

// EffectiveDefaults returns the file's defaults with the built-in values filled in. It is
// safe on a nil File.
func (f *File) EffectiveDefaults() Defaults {
	d := Defaults{SourceHighlight: true}
	if f != nil && f.Defaults != nil {
		d = *f.Defaults
	}
	if d.ScreenSize == "" {
		d.ScreenSize = BuiltinScreenSize
	}
	if d.CodePage == 0 {
		d.CodePage = BuiltinCodePage
	}
	return d
}

// Complete is Site.WithDefaults with the file's defaults tried before the built-in ones.
func (f *File) Complete(s Site) Site {
	if f == nil || f.Defaults == nil {
		return s.WithDefaults()
	}
	return s.WithDefaultsFrom(f.Defaults)
}

// Path returns the location of the sites file: $TN5250_TUI_CONFIG when set,
// else %APPDATA%\tn5250-tui\sites.json on Windows, else
// $XDG_CONFIG_HOME/tn5250-tui/sites.json or ~/.config/tn5250-tui/sites.json.
func Path() string {
	if p := os.Getenv(EnvConfig); p != "" {
		return p
	}
	if runtime.GOOS == "windows" {
		base := os.Getenv("APPDATA")
		if base == "" {
			if home, err := os.UserHomeDir(); err == nil {
				base = filepath.Join(home, "AppData", "Roaming")
			}
		}
		return filepath.Join(base, "tn5250-tui", "sites.json")
	}
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "tn5250-tui", "sites.json")
}

// Load reads the sites file. A missing file yields an empty File.
func Load() (*File, error) { return LoadFrom(Path()) }

// LoadFrom reads the sites file at path. A missing file yields an empty File.
func LoadFrom(path string) (*File, error) {
	f := &File{}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return f, nil
	}
	if err != nil {
		return nil, err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return f, nil
	}
	if err := json.Unmarshal(data, f); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	f.open(path)
	return f, nil
}

// open replaces the stored password forms with the plain passwords. A legacy clear-text
// password is sealed and the file rewritten at once, so a sites file never keeps one for
// longer than the first start after an upgrade (or after the VS Code extension wrote it).
// A password the store has lost is cleared and reported in Warnings, not fatal.
func (f *File) open(path string) {
	st := Secrets()
	f.sealedForms = map[string]sealed{}
	migrate := false
	for i := range f.Sites {
		s := &f.Sites[i]
		stored := s.Password
		if stored == "" {
			continue
		}
		if secrets.Plain(stored) {
			migrate = true
			continue
		}
		plain, err := st.Open(s.Name, stored)
		if err != nil {
			f.Warnings = append(f.Warnings, err.Error())
			s.Password = ""
			continue
		}
		s.Password = plain
		f.sealedForms[strings.ToLower(s.Name)] = sealed{plain, stored}
	}
	if migrate {
		if err := f.SaveTo(path); err != nil {
			f.Warnings = append(f.Warnings, "passwords are still in clear text in "+path+": "+err.Error())
		}
	}
}

// Save writes the file to Path() with mode 0600, creating the directory.
func (f *File) Save() error { return f.SaveTo(Path()) }

// SaveTo writes the file to path with mode 0600, creating the directory.
func (f *File) SaveTo(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if f.Sites == nil {
		f.Sites = []Site{}
	}
	out, err := f.sealedCopy()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// sealedCopy returns the file with every password replaced by its stored form.
func (f *File) sealedCopy() (*File, error) {
	st := Secrets()
	if f.sealedForms == nil {
		f.sealedForms = map[string]sealed{}
	}
	out := *f
	out.Sites = make([]Site, len(f.Sites))
	copy(out.Sites, f.Sites)
	for i := range out.Sites {
		s := &out.Sites[i]
		key := strings.ToLower(s.Name)
		if c, ok := f.sealedForms[key]; ok && c.plain == s.Password {
			s.Password = c.stored
			continue
		}
		stored, err := st.Seal(s.Name, s.Password)
		if err != nil {
			return nil, fmt.Errorf("site %q: %w", s.Name, err)
		}
		f.sealedForms[key] = sealed{s.Password, stored}
		s.Password = stored
	}
	return &out, nil
}

// Find returns the site called name (case-insensitive).
func (f *File) Find(name string) (Site, bool) {
	for _, s := range f.Sites {
		if strings.EqualFold(s.Name, name) {
			return s, true
		}
	}
	return Site{}, false
}

// Upsert adds the site or replaces the one with the same name.
func (f *File) Upsert(s Site) {
	for i := range f.Sites {
		if strings.EqualFold(f.Sites[i].Name, s.Name) {
			f.Sites[i] = s
			return
		}
	}
	f.Sites = append(f.Sites, s)
}

// Delete removes the named site and reports whether it existed.
func (f *File) Delete(name string) bool {
	for i := range f.Sites {
		if strings.EqualFold(f.Sites[i].Name, name) {
			f.Sites = append(f.Sites[:i], f.Sites[i+1:]...)
			if strings.EqualFold(f.LastUsed, name) {
				f.LastUsed = ""
			}
			_ = Secrets().Forget(name)
			delete(f.sealedForms, strings.ToLower(name))
			return true
		}
	}
	return false
}

// WithDefaults fills empty fields from the built-in defaults: port 23 (992 with TLS), 27x132
// (the wide screen most IBM i screens are designed for; 24x80 is a site setting), code page 37.
func (s Site) WithDefaults() Site { return s.WithDefaultsFrom(nil) }

// WithDefaultsFrom fills empty fields: port 23 (992 with TLS), then the screen size, code page
// and device name from d (when not nil), then the built-in 27x132 and code page 37.
func (s Site) WithDefaultsFrom(d *Defaults) Site {
	if s.Port == 0 {
		if s.TLS {
			s.Port = 992
		} else {
			s.Port = 23
		}
	}
	if d != nil {
		if s.ScreenSize == "" {
			s.ScreenSize = d.ScreenSize
		}
		if s.CodePage == 0 {
			s.CodePage = d.CodePage
		}
		if s.DeviceName == "" {
			s.DeviceName = d.DeviceName
		}
	}
	if s.ScreenSize == "" {
		s.ScreenSize = BuiltinScreenSize
	}
	if s.CodePage == 0 {
		s.CodePage = BuiltinCodePage
	}
	return s
}
