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

// File is the content of sites.json.
type File struct {
	Sites    []Site `json:"sites"`
	LastUsed string `json:"lastUsed"`
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

// WithDefaults fills empty fields: port 23 (992 with TLS), 24x80, code page 37.
func (s Site) WithDefaults() Site {
	if s.Port == 0 {
		if s.TLS {
			s.Port = 992
		} else {
			s.Port = 23
		}
	}
	if s.ScreenSize == "" {
		s.ScreenSize = "24x80"
	}
	if s.CodePage == 0 {
		s.CodePage = 37
	}
	return s
}
