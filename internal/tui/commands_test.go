package tui

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseCommand(t *testing.T) {
	cases := []struct {
		in   string
		want Command
	}{
		{"connect pub400.com:23 --tls", Command{Name: "connect", Args: []string{"pub400.com:23"}, TLS: true}},
		{":connect --tls pub400.com", Command{Name: "connect", Args: []string{"pub400.com"}, TLS: true}},
		{"connect", Command{Name: "connect"}},
		{"  CONNECT  mysite ", Command{Name: "connect", Args: []string{"mysite"}}},
		{"disconnect", Command{Name: "disconnect"}},
		{"q", Command{Name: "quit"}},
		{"quit", Command{Name: "quit"}},
		{"key pf13", Command{Name: "key", Args: []string{"pf13"}}},
		{"send hello  world", Command{Name: "send", Args: []string{"hello  world"}}},
		{"send --tls x", Command{Name: "send", Args: []string{"--tls x"}}},
		{"trace on out.log", Command{Name: "trace", Args: []string{"on", "out.log"}}},
		{`dump "my file.txt"`, Command{Name: "dump", Args: []string{"my file.txt"}}},
		{"size 27x132", Command{Name: "size", Args: []string{"27x132"}}},
		{"cp 273", Command{Name: "cp", Args: []string{"273"}}},
	}
	for _, c := range cases {
		got, err := ParseCommand(c.in)
		if err != nil {
			t.Errorf("%q: %v", c.in, err)
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%q: got %+v; want %+v", c.in, got, c.want)
		}
	}
	for _, bad := range []string{"", "   ", ":", "send", `dump "oops`} {
		if _, err := ParseCommand(bad); err == nil {
			t.Errorf("%q should fail", bad)
		}
	}
}

func TestParseTarget(t *testing.T) {
	cases := []struct {
		in   string
		host string
		port int
		bad  bool
	}{
		{"PUB400.COM", "PUB400.COM", 0, false},
		{"pub400.com:23", "pub400.com", 23, false},
		{"pub400.com:992", "pub400.com", 992, false},
		{"[::1]:2323", "::1", 2323, false},
		{"[::1]", "::1", 0, false},
		{"::1", "::1", 0, false},
		{"host:0", "", 0, true},
		{"host:99999", "", 0, true},
		{"host:abc", "", 0, true},
		{":23", "", 0, true},
		{"", "", 0, true},
	}
	for _, c := range cases {
		h, p, err := ParseTarget(c.in)
		if c.bad {
			if err == nil {
				t.Errorf("%q should fail", c.in)
			}
			continue
		}
		if err != nil || h != c.host || p != c.port {
			t.Errorf("%q: got %q %d %v; want %q %d", c.in, h, p, err, c.host, c.port)
		}
	}
}

func TestConnectCommandBuildsTarget(t *testing.T) {
	r := newRig(t, nil)
	r.connect(t, "pub400.com:23 --tls")
	o, _ := r.d.last()
	if o.Host != "pub400.com" || o.Port != 23 || !o.TLS || o.OnData == nil || o.OnClosed == nil {
		t.Fatalf("dial options %+v", o)
	}
	r.a.RunCommand("connect pub400.com")
	waitFor(t, "second dial", func() bool { return r.d.count() == 2 })
	o, _ = r.d.last()
	if o.Port != 23 || o.TLS {
		t.Fatalf("defaults: %+v", o)
	}
}

func TestUnknownCommandShowsMessage(t *testing.T) {
	r := newRig(t, nil)
	r.a.RunCommand("frobnicate now")
	if got := r.noticeText(); !strings.Contains(got, "unknown command: frobnicate") {
		t.Fatalf("notice %q", got)
	}
	l, _, alert := r.a.oiaSource()
	if !strings.Contains(l, "unknown command") || !alert {
		t.Fatalf("OIA %q alert=%v", l, alert)
	}
	// a key press clears the message
	r.a.handleKey(key(0, 0, 0))
	if r.noticeText() != "" {
		t.Error("message not cleared by a key press")
	}
}

func TestKeySendAndBareActions(t *testing.T) {
	r := newRig(t, nil)
	r.a.RunCommand("key pf3")
	if r.noticeText() != "not connected" {
		t.Fatalf("notice %q", r.noticeText())
	}
	r.connect(t, "pub400.com")
	e := r.engine()
	e.clear()
	r.a.RunCommand("key pf13")
	r.a.RunCommand("key FIELDEXIT")
	r.a.RunCommand("fieldexit")
	r.a.RunCommand("sysreq")
	r.a.RunCommand("send DSPLIBL")
	got := joinCalls(e.Calls())
	want := "press:pf13 press:fieldExit press:fieldExit press:sysreq type:DSPLIBL press:enter"
	if got != want {
		t.Fatalf("calls %q; want %q", got, want)
	}
	r.a.RunCommand("key bogus")
	if !strings.Contains(r.noticeText(), "unknown action") {
		t.Fatalf("notice %q", r.noticeText())
	}
}

func TestSizeAndCPApplyOnNextConnect(t *testing.T) {
	r := newRig(t, nil)
	r.connect(t, "pub400.com")
	r.a.RunCommand("size 24x80")
	r.a.RunCommand("cp 273")
	r.mu.Lock()
	if r.args[0] != "27x132/37/" {
		t.Errorf("first connect %q", r.args[0])
	}
	r.mu.Unlock()
	r.a.RunCommand("connect pub400.com")
	waitFor(t, "second engine", func() bool { return len(r.engines) == 2 })
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.args[1] != "24x80/273/" {
		t.Errorf("second connect %q", r.args[1])
	}
}

func TestDumpAndTrace(t *testing.T) {
	r := newRig(t, nil)
	dir := t.TempDir()
	r.a.RunCommand("dump " + filepath.Join(dir, "x.txt"))
	if !strings.Contains(r.noticeText(), "not connected") {
		t.Fatalf("notice %q", r.noticeText())
	}
	r.connect(t, "pub400.com")
	out := filepath.Join(dir, "screen.txt")
	r.a.RunCommand("dump " + out)
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(lines) != 24 || !strings.Contains(lines[1], "Sign On") || !strings.Contains(lines[5], "User  . . . .") {
		t.Fatalf("dump: %d lines, row1 %q", len(lines), lines[1])
	}
	tr := filepath.Join(dir, "trace.log")
	r.a.RunCommand("trace on " + tr)
	_, l := r.d.last()
	l.mu.Lock()
	has := l.trace != nil
	l.mu.Unlock()
	if !has {
		t.Fatal("trace writer not handed to the live link")
	}
	r.a.RunCommand("trace off")
	l.mu.Lock()
	has = l.trace != nil
	l.mu.Unlock()
	if has {
		t.Fatal("trace writer not removed")
	}
	if _, err := os.Stat(tr); err != nil {
		t.Fatal(err)
	}
}

func TestHelpText(t *testing.T) {
	h := HelpText()
	for _, want := range []string{"Shift+F1-F12", "connect [site|host[:port]]", "dump [file]", "Ctrl+K"} {
		if !strings.Contains(h, want) {
			t.Errorf("help lacks %q", want)
		}
	}
	r := newRig(t, nil)
	r.a.RunCommand("help")
	if r.a.topOverlay() != "help" {
		t.Fatalf("overlay %q", r.a.topOverlay())
	}
}
