package agent

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

// maxTraceLine bounds one stored trace line (a full screen is a long hex line).
const maxTraceLine = 240

// traceHead is how many of a session's first trace lines are kept for good: the telnet
// negotiation (terminal type, NEW-ENVIRON) is long gone from the ring by the first screen.
const traceHead = 60

// ring keeps the last n lines, and the first traceHead lines of the session.
type ring struct {
	mu    sync.Mutex
	n     int
	lines []string
	head  []string
}

func newRing(n int) *ring { return &ring{n: n} }

func (r *ring) Add(line string) {
	if len(line) > maxTraceLine {
		line = fmt.Sprintf("%s ...(+%d chars)", line[:maxTraceLine], len(line)-maxTraceLine)
	}
	line = time.Now().Format("15:04:05.000") + " " + line
	r.mu.Lock()
	if len(r.head) < traceHead {
		r.head = append(r.head, line)
	}
	r.lines = append(r.lines, line)
	if len(r.lines) > r.n {
		r.lines = append(r.lines[:0], r.lines[len(r.lines)-r.n:]...)
	}
	r.mu.Unlock()
}

// Last returns the last n lines, oldest first.
func (r *ring) Last(n int) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if n <= 0 || n > len(r.lines) {
		n = len(r.lines)
	}
	return append([]string(nil), r.lines[len(r.lines)-n:]...)
}

// First returns the first n lines of the session (at most traceHead), oldest first.
func (r *ring) First(n int) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if n <= 0 || n > len(r.head) {
		n = len(r.head)
	}
	return append([]string(nil), r.head[:n]...)
}

// traceBytes is the trace form of a frame: direction and hex. Callers pass the masked frame
// (the engine's forTrace) for data they send.
func traceBytes(dir string, b []byte) string {
	return dir + " " + hex.EncodeToString(b)
}

// auditor writes one JSON line per agent action to every writer.
type auditor struct {
	mu   sync.Mutex
	outs []io.Writer
}

// AuditEntry is one line of the audit log. Args never hold a password and never hold text
// typed into a hidden field.
type AuditEntry struct {
	Time      string         `json:"ts"`
	Session   string         `json:"session,omitempty"`
	Tool      string         `json:"tool"`
	Args      map[string]any `json:"args,omitempty"`
	SeqBefore uint64         `json:"seq_before"`
	SeqAfter  uint64         `json:"seq_after"`
	Error     string         `json:"error,omitempty"`
}

func (a *auditor) Log(e AuditEntry) {
	e.Time = time.Now().Format(time.RFC3339Nano)
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(e); err != nil {
		return
	}
	b := buf.Bytes() // Encode ends the line
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, w := range a.outs {
		_, _ = w.Write(b)
	}
}

// clip shortens a typed text for the audit log.
func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + fmt.Sprintf("...(%d chars)", len(r))
}

// auditText is how typed text appears in the audit log: the text (clipped), or only its length
// when it went into a hidden field.
func auditText(text string, hidden bool) string {
	if hidden {
		return fmt.Sprintf("<hidden %d chars>", len([]rune(text)))
	}
	return clip(strings.TrimRight(text, "\r\n"), 80)
}
