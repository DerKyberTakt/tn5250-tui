// Package sites stores the list of 5250 systems the terminal knows about as a
// plain JSON file. The password is kept in clear text by design.
package sites

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

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
}

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
	return f, nil
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
	data, err := json.MarshalIndent(f, "", "  ")
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
