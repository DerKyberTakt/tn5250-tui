package runscript

import (
	"github.com/DerKyberTakt/tn5250-tui/internal/hostserver/db/dbtest"
	"strings"
	"testing"
	"time"

	"github.com/DerKyberTakt/tn5250-tui/internal/hostserver/db"
)

// Run holds the session lock while the host works; Cancel must not wait for it.
func TestCancelDoesNotWaitForARunningStatement(t *testing.T) {
	s := New(Options{})
	s.mu.Lock() // a statement is running
	s.db = &db.Client{}
	s.publishDB()
	done := make(chan error, 1)
	go func() { done <- s.Cancel() }()
	select {
	case err := <-done:
		if err == nil {
			t.Error("a client without a job identifier cannot cancel; want its error")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Cancel blocked behind the running statement")
	}
	s.mu.Unlock()
}

// A host that refuses the sign-on is asked once: later statements on the session fail at once
// without another sign-on, so a stale password cannot disable the profile.
func TestRefusedSignonIsNotRetried(t *testing.T) {
	d, err := dbtest.New("127.0.0.1:0", "TESTUSER", "SECRET1", 2, dbHandler)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	s := New(Options{Host: "127.0.0.1", DatabasePort: d.Port(), CommandPort: 1, User: "testuser", Password: "WRONG"})
	t.Cleanup(s.Close)
	signons := func() int {
		n := 0
		for _, id := range d.Seen() {
			if id == 0x7001 {
				n++
			}
		}
		return n
	}
	if _, err := s.Run(Classify("select 1 from t"), 10); err == nil || !strings.Contains(err.Error(), "password is not correct") {
		t.Fatalf("first run: %v", err)
	}
	first := signons()
	if first == 0 {
		t.Fatal("the first run did not reach the host")
	}
	for i := 0; i < 3; i++ {
		if _, err := s.Run(Classify("select 1 from t"), 10); err == nil || !strings.Contains(err.Error(), "not signing on again") {
			t.Fatalf("run %d: %v", i+2, err)
		}
	}
	if n := signons(); n != first {
		t.Fatalf("%d sign-on attempts after the first refusal (had %d)", n-first, first)
	}
}
