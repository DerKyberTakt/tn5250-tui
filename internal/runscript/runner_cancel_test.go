package runscript

import (
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
