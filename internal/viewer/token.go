package viewer

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/DerKyberTakt/tn5250-tui/internal/sites"
)

// TokenFile tells a viewer where a running server listens. It lives next to the sites
// file in the agent directory, one file per server process, readable by the user only.
type TokenFile struct {
	PID     int       `json:"pid"`
	Addr    string    `json:"addr"` // 127.0.0.1:port
	Token   string    `json:"token"`
	Started time.Time `json:"started"`
	Label   string    `json:"label,omitempty"` // what the server was started for (site, cwd)
	path    string
}

// Path is the file the entry was read from (viewer side).
func (t TokenFile) Path() string { return t.path }

// TokenDir is the directory of the token files: <sites dir>/agent.
func TokenDir() string { return filepath.Join(filepath.Dir(sites.Path()), "agent") }

// NewToken returns 32 random hex characters.
func NewToken() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return hex.EncodeToString(b)
}

// Write stores the entry as <TokenDir>/<pid>.json with mode 0600 and returns its path.
func (t *TokenFile) Write() (string, error) {
	dir := TokenDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	data, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return "", err
	}
	p := filepath.Join(dir, strconv.Itoa(t.PID)+".json")
	if err := os.WriteFile(p, append(data, '\n'), 0o600); err != nil {
		return "", err
	}
	t.path = p
	return p, nil
}

// Remove deletes the entry's file.
func (t *TokenFile) Remove() {
	if t.path != "" {
		_ = os.Remove(t.path)
	}
}

// ListTokens reads every token file, newest first. Entries whose server no longer answers
// are removed and left out when probe is true.
func ListTokens(probe bool) ([]TokenFile, error) {
	entries, err := os.ReadDir(TokenDir())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []TokenFile
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		p := filepath.Join(TokenDir(), e.Name())
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var t TokenFile
		if json.Unmarshal(data, &t) != nil || t.Addr == "" {
			_ = os.Remove(p)
			continue
		}
		t.path = p
		if probe {
			c, err := Dial(t.Addr, t.Token, 2*time.Second)
			if err != nil {
				_ = os.Remove(p)
				continue
			}
			c.Close()
		}
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Started.After(out[j].Started) })
	return out, nil
}
