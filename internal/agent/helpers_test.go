package agent

import (
	"bytes"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/DerKyberTakt/tn5250-tui/internal/fakehost"
	"github.com/DerKyberTakt/tn5250-tui/internal/tn5250"
)

// lockedBuf is a bytes.Buffer safe for the audit writer and the test goroutine.
type lockedBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// startFake runs the fake host on 127.0.0.1:0 and returns its host and port.
func startFake(t *testing.T) (string, int) {
	t.Helper()
	capture, err := fakehost.LoadCapture("../../testdata/pub400-signon-3477fc.json")
	if err != nil {
		t.Fatal(err)
	}
	host, err := fakehost.StartWith("127.0.0.1:0", capture, fakehost.Options{Timeout: 5 * time.Second, Lenient: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = host.Close() })
	h, p, err := net.SplitHostPort(host.Addr())
	if err != nil {
		t.Fatal(err)
	}
	port, _ := strconv.Atoi(p)
	return h, port
}

// newTestManager builds a manager whose audit log goes to the returned buffer.
func newTestManager(t *testing.T, cfg Config) (*Manager, *lockedBuf) {
	t.Helper()
	buf := &lockedBuf{}
	cfg.AuditWriter = buf
	m, err := NewManager(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	return m, buf
}

// snapBuilder makes a Snapshot by hand: rows of text, fields as the engine lays them out (an
// attribute cell in front of each field's first data cell).
type snapBuilder struct {
	rows, cols int
	lines      map[int]string // 0-based row -> text starting at column 0
	fields     []tn5250.FieldInfo
	cursor     [2]int
}

func (b snapBuilder) build() tn5250.Snapshot {
	s := tn5250.Snapshot{Rows: b.rows, Cols: b.cols, Cells: make([]tn5250.Cell, b.rows*b.cols), Seq: 7}
	for i := range s.Cells {
		s.Cells[i] = tn5250.Cell{Ch: ' ', Attr: tn5250.AttrNormal}
	}
	for r, text := range b.lines {
		for c, ch := range []rune(text) {
			s.Cells[r*b.cols+c].Ch = ch
		}
	}
	for _, f := range b.fields {
		if f.Start > 0 {
			s.Cells[f.Start-1].Attribute = true
			s.Cells[f.Start-1].Ch = ' '
		}
		for i := f.Start; i < f.Start+f.Length; i++ {
			s.Cells[i].InField = true
			if f.Nondisplay {
				s.Cells[i].Ch = ' '
			}
		}
	}
	s.Fields = b.fields
	s.Cursor.Row, s.Cursor.Col = b.cursor[0], b.cursor[1]
	return s
}

// fieldAt makes a FieldInfo at a 0-based row and column.
func fieldAt(cols, row, col, length int, mod func(*tn5250.FieldInfo)) tn5250.FieldInfo {
	f := tn5250.FieldInfo{Start: row*cols + col, Length: length}
	if mod != nil {
		mod(&f)
	}
	return f
}
