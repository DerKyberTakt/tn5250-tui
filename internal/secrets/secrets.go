// Package secrets keeps site passwords out of the sites file.
//
// A password is stored in the operating system's secret store (Windows Credential Manager,
// macOS Keychain, Linux Secret Service) and the sites file only carries the marker
// "keyring:". Where no secret store exists (a headless Linux box, a container, an SSH session
// without D-Bus) the password is encrypted with AES-256-GCM under a random key kept in a
// 0600 file next to the sites file, and stored as "enc:<base64>". That second form is an
// obfuscation against copies and backups of the sites file, not a boundary: whoever can read
// the key file can decrypt it. The wire is a separate matter (use TLS).
//
// The backend a value was sealed with is part of the stored form, so reading never guesses.
// Legacy clear-text values are read as they are; the sites package re-seals them on load.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/zalando/go-keyring"
)

// Service is the keyring service (Credential Manager "internet or network address") name.
const Service = "tn5250-tui"

// EnvMode selects the backend: "keyring" (default: the OS store, encrypted file where there
// is none), "file" (always the encrypted file), "plain" (clear text in the sites file, the
// 0.1.0 behaviour; only for a machine you trust completely).
const EnvMode = "TN5250_TUI_SECRETS"

const (
	prefixKeyring = "keyring:"
	prefixEnc     = "enc:"
	// KeyFile is the name of the encryption key next to the sites file.
	KeyFile = "secret.key"
)

// Store seals and opens one password per site name.
type Store interface {
	// Seal returns the form to write into the sites file for plain; "" stays "".
	Seal(name, plain string) (string, error)
	// Open returns the password for a stored form (a clear-text legacy value comes back as is).
	Open(name, stored string) (string, error)
	// Forget removes whatever the store holds for name (no error when it holds nothing).
	Forget(name string) error
}

// Plain reports whether a stored value is a legacy clear-text password.
func Plain(stored string) bool {
	return stored != "" && !strings.HasPrefix(stored, prefixKeyring) && !strings.HasPrefix(stored, prefixEnc)
}

// Account is the keyring account name for a site.
func Account(name string) string { return "site:" + strings.ToLower(strings.TrimSpace(name)) }

// New returns the store for the sites file directory dir, chosen by EnvMode. Inside `go test`
// it returns an in-memory store so tests never touch the user's keyring.
func New(dir string) Store {
	if testing.Testing() && os.Getenv(EnvMode) == "" {
		return Memory()
	}
	switch strings.ToLower(os.Getenv(EnvMode)) {
	case "plain":
		return plainStore{}
	case "file":
		return &fileStore{dir: dir}
	case "memory":
		return Memory()
	}
	return &keyringStore{fallback: &fileStore{dir: dir}}
}

// --- plain ---

type plainStore struct{}

func (plainStore) Seal(_, plain string) (string, error) { return plain, nil }
func (plainStore) Open(_, stored string) (string, error) {
	if !Plain(stored) && stored != "" {
		return "", errors.New("password is sealed; unset " + EnvMode + " to read it")
	}
	return stored, nil
}
func (plainStore) Forget(string) error { return nil }

// --- memory (tests) ---

type memStore struct {
	mu sync.Mutex
	m  map[string]string
}

// Memory returns a store that keeps passwords in memory under the keyring marker.
func Memory() Store { return &memStore{m: map[string]string{}} }

func (s *memStore) Seal(name, plain string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if plain == "" {
		delete(s.m, Account(name))
		return "", nil
	}
	s.m[Account(name)] = plain
	return prefixKeyring, nil
}

func (s *memStore) Open(name, stored string) (string, error) {
	if Plain(stored) || stored == "" {
		return stored, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.m[Account(name)]
	if !ok {
		return "", missing(name)
	}
	return v, nil
}

func (s *memStore) Forget(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, Account(name))
	return nil
}

func missing(name string) error {
	return fmt.Errorf("the password for site %q is not in the secret store any more; enter it again", name)
}

// --- OS keyring with the encrypted file as fallback ---

type keyringStore struct {
	fallback *fileStore
	once     sync.Once
	usable   bool
	probeErr error
}

// available probes the OS store once per process: a Set/Get/Delete round trip on a probe
// account. On Windows the store is always there, so a failure is reported, not worked around.
func (s *keyringStore) available() bool {
	s.once.Do(func() {
		const probe = "probe"
		if err := keyring.Set(Service, probe, "ok"); err != nil {
			s.probeErr = err
			return
		}
		v, err := keyring.Get(Service, probe)
		_ = keyring.Delete(Service, probe)
		s.usable = err == nil && v == "ok"
		if !s.usable {
			s.probeErr = err
		}
	})
	return s.usable
}

func (s *keyringStore) Seal(name, plain string) (string, error) {
	if plain == "" {
		return "", s.Forget(name)
	}
	if s.available() {
		if err := keyring.Set(Service, Account(name), plain); err != nil {
			return "", fmt.Errorf("secret store: %w", err)
		}
		_ = s.fallback.Forget(name) // no stale copy under the other form
		return prefixKeyring, nil
	}
	if runtime.GOOS == "windows" {
		return "", fmt.Errorf("Credential Manager is not usable (%v); set %s=file to use an encrypted file instead", s.probeErr, EnvMode)
	}
	return s.fallback.Seal(name, plain)
}

func (s *keyringStore) Open(name, stored string) (string, error) {
	switch {
	case stored == "" || Plain(stored):
		return stored, nil
	case strings.HasPrefix(stored, prefixEnc):
		return s.fallback.Open(name, stored)
	}
	v, err := keyring.Get(Service, Account(name))
	if errors.Is(err, keyring.ErrNotFound) {
		return "", missing(name)
	}
	if err != nil {
		return "", fmt.Errorf("secret store: %w", err)
	}
	return v, nil
}

func (s *keyringStore) Forget(name string) error {
	var errs []error
	if err := keyring.Delete(Service, Account(name)); err != nil && !errors.Is(err, keyring.ErrNotFound) && s.available() {
		errs = append(errs, err)
	}
	if err := s.fallback.Forget(name); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// --- encrypted file ---

type fileStore struct {
	dir string
	mu  sync.Mutex
	key []byte
}

func (s *fileStore) path() string { return filepath.Join(s.dir, KeyFile) }

// loadKey reads the key file, creating it (0600, exclusive) the first time. A key file that
// other users could read is refused rather than used.
func (s *fileStore) loadKey(create bool) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.key != nil {
		return s.key, nil
	}
	p := s.path()
	data, err := os.ReadFile(p)
	switch {
	case err == nil:
		if err := checkPrivate(p); err != nil {
			return nil, err
		}
		raw, derr := base64.StdEncoding.DecodeString(strings.TrimSpace(string(data)))
		if derr != nil || len(raw) != 32 {
			return nil, fmt.Errorf("%s: not a valid key file", p)
		}
		s.key = raw
		return s.key, nil
	case errors.Is(err, fs.ErrNotExist) && create:
		raw := make([]byte, 32)
		if _, err := rand.Read(raw); err != nil {
			return nil, err
		}
		if err := os.MkdirAll(s.dir, 0o700); err != nil {
			return nil, err
		}
		f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return nil, err
		}
		_, werr := f.WriteString(base64.StdEncoding.EncodeToString(raw) + "\n")
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		if werr != nil {
			_ = os.Remove(p)
			return nil, werr
		}
		s.key = raw
		return s.key, nil
	case errors.Is(err, fs.ErrNotExist):
		return nil, fmt.Errorf("%s is missing, so the encrypted passwords cannot be read; enter them again", p)
	}
	return nil, err
}

// checkPrivate refuses a key file readable by group or others (Unix; Windows relies on the
// profile directory's ACL).
func checkPrivate(p string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	st, err := os.Stat(p)
	if err != nil {
		return err
	}
	if st.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("%s is readable by other users (mode %o); run chmod 600 on it", p, st.Mode().Perm())
	}
	return nil
}

func (s *fileStore) Seal(name, plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	key, err := s.loadKey(true)
	if err != nil {
		return "", err
	}
	gcm, err := newGCM(key)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	ct := gcm.Seal(nil, nonce, []byte(plain), []byte(Account(name)))
	return prefixEnc + base64.StdEncoding.EncodeToString(append(nonce, ct...)), nil
}

func (s *fileStore) Open(name, stored string) (string, error) {
	switch {
	case stored == "" || Plain(stored):
		return stored, nil
	case strings.HasPrefix(stored, prefixKeyring):
		return "", missing(name)
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(stored, prefixEnc))
	if err != nil {
		return "", fmt.Errorf("site %q: the stored password is damaged", name)
	}
	key, err := s.loadKey(false)
	if err != nil {
		return "", err
	}
	gcm, err := newGCM(key)
	if err != nil {
		return "", err
	}
	if len(raw) < gcm.NonceSize() {
		return "", fmt.Errorf("site %q: the stored password is damaged", name)
	}
	pt, err := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], []byte(Account(name)))
	if err != nil {
		return "", fmt.Errorf("site %q: the stored password does not match the key file; enter it again", name)
	}
	return string(pt), nil
}

// Forget has nothing to remove: the ciphertext lives in the sites file entry itself.
func (s *fileStore) Forget(string) error { return nil }

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
