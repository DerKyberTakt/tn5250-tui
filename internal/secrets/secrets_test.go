package secrets

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPlainDetection(t *testing.T) {
	for v, want := range map[string]bool{"": false, "pw": true, "keyring:": false, "enc:abc": false} {
		if got := Plain(v); got != want {
			t.Errorf("Plain(%q) = %v", v, got)
		}
	}
	if Account(" Agent ") != "site:agent" {
		t.Fatalf("account %q", Account(" Agent "))
	}
}

func TestMemoryStore(t *testing.T) {
	s := Memory()
	stored, err := s.Seal("pub", "pw")
	if err != nil || stored != "keyring:" {
		t.Fatalf("seal %q %v", stored, err)
	}
	if v, err := s.Open("PUB", stored); err != nil || v != "pw" {
		t.Fatalf("open %q %v", v, err)
	}
	if v, err := s.Open("pub", "legacy"); err != nil || v != "legacy" {
		t.Fatalf("legacy %q %v", v, err)
	}
	if _, err := s.Open("other", stored); err == nil || !strings.Contains(err.Error(), "enter it again") {
		t.Fatalf("missing: %v", err)
	}
	if stored, _ := s.Seal("pub", ""); stored != "" {
		t.Fatalf("empty seal %q", stored)
	}
	if _, err := s.Open("pub", "keyring:"); err == nil {
		t.Fatal("sealing an empty password should forget the old one")
	}
}

func TestFileStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s := &fileStore{dir: filepath.Join(dir, "cfg")}
	stored, err := s.Seal("pub", "s3cret")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(stored, "enc:") || strings.Contains(stored, "s3cret") {
		t.Fatalf("stored form %q", stored)
	}
	st, err := os.Stat(filepath.Join(dir, "cfg", KeyFile))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && st.Mode().Perm() != 0o600 {
		t.Fatalf("key file mode %o", st.Mode().Perm())
	}
	// a fresh store on the same directory reads it back
	s2 := &fileStore{dir: s.dir}
	if v, err := s2.Open("PUB", stored); err != nil || v != "s3cret" {
		t.Fatalf("open %q %v", v, err)
	}
	// bound to the site name
	if _, err := s2.Open("other", stored); err == nil {
		t.Fatal("ciphertext opened under another site name")
	}
	// damaged value
	if _, err := s2.Open("pub", "enc:!!!"); err == nil || !strings.Contains(err.Error(), "damaged") {
		t.Fatalf("damaged: %v", err)
	}
	// a keyring marker cannot be opened by the file store
	if _, err := s2.Open("pub", "keyring:"); err == nil {
		t.Fatal("keyring marker opened by the file store")
	}
	// two seals of the same value differ (random nonce)
	again, _ := s.Seal("pub", "s3cret")
	if again == stored {
		t.Fatal("nonce reused")
	}
	// a missing key file is an error that says so, never a new key
	s3 := &fileStore{dir: filepath.Join(dir, "nokey")}
	if _, err := s3.Open("pub", stored); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("missing key: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "nokey", KeyFile)); err == nil {
		t.Fatal("Open created a key file")
	}
}

func TestFileStoreRefusesSharedKey(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("mode bits are not meaningful on Windows")
	}
	dir := t.TempDir()
	s := &fileStore{dir: dir}
	stored, err := s.Seal("pub", "pw")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(dir, KeyFile), 0o644); err != nil {
		t.Fatal(err)
	}
	s2 := &fileStore{dir: dir}
	if _, err := s2.Open("pub", stored); err == nil || !strings.Contains(err.Error(), "chmod 600") {
		t.Fatalf("shared key accepted: %v", err)
	}
}

func TestNewHonoursMode(t *testing.T) {
	t.Setenv(EnvMode, "plain")
	if _, ok := New(t.TempDir()).(plainStore); !ok {
		t.Fatal("plain mode")
	}
	t.Setenv(EnvMode, "file")
	if _, ok := New(t.TempDir()).(*fileStore); !ok {
		t.Fatal("file mode")
	}
	t.Setenv(EnvMode, "")
	if _, ok := New(t.TempDir()).(*memStore); !ok {
		t.Fatal("tests must get the memory store")
	}
	p := plainStore{}
	if v, _ := p.Seal("x", "pw"); v != "pw" {
		t.Fatal("plain seal")
	}
	if _, err := p.Open("x", "keyring:"); err == nil {
		t.Fatal("plain mode must not pretend to read a sealed value")
	}
}
