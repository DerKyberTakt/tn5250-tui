package agent

import (
	"context"
	"encoding/hex"
	"errors"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/DerKyberTakt/tn5250-tui/internal/client"
	"github.com/DerKyberTakt/tn5250-tui/internal/ebcdic"
	"github.com/DerKyberTakt/tn5250-tui/internal/sites"
)

func ctxT(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func connectPlain(t *testing.T, m *Manager, host string, port int) ConnectOut {
	t.Helper()
	signon := false
	out, err := m.Connect(ctxT(t), ConnectArgs{Host: host, Port: port, ScreenSize: "27x132", Signon: &signon, TimeoutMs: 10000})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	return out
}

func TestManagerAgainstFakeHost(t *testing.T) {
	host, port := startFake(t)
	m, audit := newTestManager(t, Config{})
	ctx := ctxT(t)

	out := connectPlain(t, m, host, port)
	if out.Session != "s1" || out.Size == "" || out.SignedOn {
		t.Fatalf("connect: %+v", out)
	}
	if out.Screen.Settled == nil || !*out.Screen.Settled {
		t.Fatalf("the first screen must settle: %+v", out.Screen.Settled)
	}
	for _, want := range []string{"Welcome to PUB400.COM", "Your user name:", "Password (max. 128):"} {
		if !strings.Contains(out.Screen.Text, want) {
			t.Errorf("sign-on screen lacks %q:\n%s", want, out.Screen.Text)
		}
	}
	if out.Screen.OIA.Inhibited != "none" || !out.Screen.OIA.Connected {
		t.Errorf("oia: %+v", out.Screen.OIA)
	}
	if out.Screen.Cursor.Row < 1 || out.Screen.Cursor.Col < 1 {
		t.Errorf("cursor must be 1-based: %+v", out.Screen.Cursor)
	}

	// the field table: user field first (labelled), password second and hidden
	var input []FieldOut
	for _, f := range out.Screen.Fields {
		if !f.Bypass {
			input = append(input, f)
		}
	}
	if len(input) < 2 || !strings.Contains(input[0].Label, "Your user name") || !input[1].Nondisplay {
		t.Fatalf("input fields: %+v", input)
	}

	// find
	fr, err := m.Find(FindArgs{Session: "s1", Text: "your user name"})
	if err != nil || len(fr.Hits) != 1 || fr.Hits[0].Row < 1 {
		t.Errorf("find: %+v %v", fr, err)
	}

	// type into the user field (first input field), then press enter
	scr, err := m.Type(ctx, TypeArgs{Session: "s1", Text: "alex", Field: float64(input[0].Index)})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.ToUpper(scr.Text), "ALEX") {
		t.Errorf("typed text missing:\n%s", scr.Text)
	}
	if scr.Settled != nil {
		t.Errorf("typing alone does not wait for the host, settled=%v", *scr.Settled)
	}
	scr, err = m.Press(ctx, PressArgs{Session: "s1", Key: "Enter", TimeoutMs: 600})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(scr.Text, "FAKE HOST  Enter received") {
		t.Fatalf("no canned reply:\n%s", scr.Text)
	}
	if scr.Settled == nil {
		t.Fatal("enter must report settled")
	}
	t.Logf("after enter: settled=%v inhibited=%s", *scr.Settled, scr.OIA.Inhibited)

	// wait until text (already there) and a change that never comes
	w, err := m.Wait(ctx, WaitArgs{Session: "s1", Until: "text", Text: "enter RECEIVED", Rows: "1-2", TimeoutMs: 2000})
	if err != nil || !w.Matched {
		t.Errorf("wait text: %+v %v", w.Matched, err)
	}
	w, err = m.Wait(ctx, WaitArgs{Session: "s1", Until: "text", Text: "no such words", TimeoutMs: 150})
	if err != nil || w.Matched {
		t.Errorf("wait for absent text must time out unmatched: %v %v", w.Matched, err)
	}
	w, err = m.Wait(ctx, WaitArgs{Session: "s1", Until: "change", Seq: 1 << 40, TimeoutMs: 100})
	if err != nil || w.Matched {
		t.Errorf("wait change past the end: %v %v", w.Matched, err)
	}
	w, err = m.Wait(ctx, WaitArgs{Session: "s1", Until: "change", Seq: 1, TimeoutMs: 1000})
	if err != nil || !w.Matched {
		t.Errorf("wait change below the current seq: %v %v", w.Matched, err)
	}

	// local keys return at once and carry no settled flag
	scr, err = m.Press(ctx, PressArgs{Session: "s1", Key: "tab"})
	if err != nil || scr.Settled != nil {
		t.Errorf("tab: settled=%v err=%v", scr.Settled, err)
	}
	if _, err = m.Press(ctx, PressArgs{Session: "s1", Key: "tab", Times: 51}); err == nil {
		t.Error("times above 50 must be refused")
	}
	if _, err = m.Press(ctx, PressArgs{Session: "s1", Key: "nonsense"}); err == nil || !strings.Contains(err.Error(), "enter") {
		t.Errorf("an unknown key lists the accepted ones: %v", err)
	}

	// sessions
	ss := m.Sessions()
	if len(ss.Sessions) != 1 || ss.Sessions[0].Session != "s1" || !ss.Sessions[0].Connected || ss.Sessions[0].Host != host || ss.ReadOnly {
		t.Errorf("sessions: %+v", ss)
	}

	// trace and dump
	tr, err := m.Trace(TraceArgs{Session: "s1", Lines: 5})
	if err != nil || len(tr.Lines) == 0 || len(tr.Lines) > 5 {
		t.Errorf("trace: %v %v", tr.Lines, err)
	}
	d, err := m.Dump(DumpArgs{Session: "s1"})
	if err != nil || !strings.Contains(d.Dump, "--attr--") || d.Size != out.Size {
		t.Errorf("dump: %v", err)
	}

	// the audit log records what the agent did, one JSON line each
	log := audit.String()
	for _, want := range []string{`"tool":"connect"`, `"tool":"type"`, `"tool":"press"`, `"text":"alex"`} {
		if !strings.Contains(log, want) {
			t.Errorf("audit lacks %s:\n%s", want, log)
		}
	}

	// disconnect
	dc, err := m.Disconnect(ctx, DisconnectArgs{Session: "s1"})
	if err != nil || !dc.Closed || dc.SignedOff {
		t.Errorf("disconnect: %+v %v", dc, err)
	}
	if _, err := m.Screen(ScreenArgs{Session: "s1"}); err == nil {
		t.Error("the session must be gone after disconnect")
	}
	if len(m.Sessions().Sessions) != 0 {
		t.Error("sessions must be empty")
	}
}

func TestFillByLabelAndPasswordNeverLogged(t *testing.T) {
	host, port := startFake(t)
	m, audit := newTestManager(t, Config{})
	ctx := ctxT(t)
	connectPlain(t, m, host, port)

	const secret = "Zq9!TopSecret"
	scr, err := m.Fill(ctx, FillArgs{Session: "s1", Fields: []FillItem{
		{Label: "your user name", Text: "alex"},
		{Label: "Password", Text: secret},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(scr.Text, secret) {
		t.Fatal("a hidden field's text must not appear on the screen")
	}
	scr, err = m.Press(ctx, PressArgs{Session: "s1", Key: "enter", TimeoutMs: 600})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(scr.Text, "FAKE HOST") {
		t.Fatalf("the fill did not reach the host:\n%s", scr.Text)
	}
	// an ambiguous or unknown label is an error and types nothing
	if _, err := m.Fill(ctx, FillArgs{Session: "s1", Fields: []FillItem{{Label: "nothing like it", Text: "x"}}}); err == nil {
		t.Error("an unknown label must fail")
	}

	// a password typed through the type tool into a hidden field is logged by length only
	host2, port2 := startFake(t)
	connectPlain(t, m, host2, port2)
	if _, err := m.Type(ctx, TypeArgs{Session: "s2", Text: secret + "2", Field: "password"}); err != nil {
		t.Fatal(err)
	}

	tr, _ := m.Trace(TraceArgs{Session: "s1", Lines: TraceLines})
	tr2, _ := m.Trace(TraceArgs{Session: "s2", Lines: TraceLines})
	all := audit.String() + strings.Join(tr.Lines, "\n") + strings.Join(tr2.Lines, "\n")
	enc := ebcdic.MustGet(37).EncodeString(secret)
	for _, needle := range []string{secret, secret + "2", hex.EncodeToString(enc)} {
		if strings.Contains(all, needle) {
			t.Errorf("secret %q leaked into the audit log or the trace", needle)
		}
	}
	if !strings.Contains(audit.String(), "<hidden 13 chars>") {
		t.Errorf("hidden text should be logged by length:\n%s", audit.String())
	}
	// the trace does hold the masked frame: the user name went out, the password did not
	if !strings.Contains(strings.Join(tr.Lines, "\n"), "OUT") {
		t.Error("trace has no outgoing frame")
	}
}

func TestConnectWithSignon(t *testing.T) {
	host, port := startFake(t)
	m, audit := newTestManager(t, Config{User: "alex", Password: "pa55word!"})
	ctx := ctxT(t)

	out, err := m.Connect(ctx, ConnectArgs{Host: host, Port: port, ScreenSize: "27x132", TimeoutMs: 600})
	if err != nil {
		t.Fatal(err)
	}
	if !out.SignedOn || !strings.Contains(out.Screen.Text, "FAKE HOST  Enter received") {
		t.Fatalf("sign-on did not reach the host: signed_on=%v\n%s", out.SignedOn, out.Screen.Text)
	}
	if strings.Contains(audit.String(), "pa55word!") {
		t.Error("the password is in the audit log")
	}
	tr, _ := m.Trace(TraceArgs{Session: out.Session, Lines: TraceLines})
	if strings.Contains(strings.Join(tr.Lines, "\n"), hex.EncodeToString(ebcdic.MustGet(37).EncodeString("pa55word!"))) {
		t.Error("the password is in the trace")
	}

	// a second connect on the FAKE HOST screen: sign-on is refused (no password field), the
	// session stays open
	// on the canned screen there is no input field at all: signon must refuse with a clear error
	s, _ := m.get(out.Session)
	if err := s.typedSignon("alex", "x"); err == nil || !strings.Contains(err.Error(), "non-display") {
		t.Errorf("sign-on on a screen without a password field must be refused: %v", err)
	}
}

func TestReadOnlyGuard(t *testing.T) {
	host, port := startFake(t)
	m, _ := newTestManager(t, Config{ReadOnly: true})
	ctx := ctxT(t)
	connectPlain(t, m, host, port)

	for _, text := range []string{"DLTF X", "chglibl", "call pgm(x)", "ADDLIBLE Y"} {
		_, err := m.Type(ctx, TypeArgs{Session: "s1", Text: text, Field: float64(1)})
		if err == nil || !strings.Contains(err.Error(), "read-only mode:") || !strings.Contains(err.Error(), "ask the user to run it") {
			t.Errorf("%q: %v", text, err)
		}
		_, err = m.Fill(ctx, FillArgs{Session: "s1", Fields: []FillItem{{Index: 1, Text: text}}})
		if err == nil || !strings.Contains(err.Error(), "read-only mode:") {
			t.Errorf("fill %q: %v", text, err)
		}
	}
	// the screen must not have changed: nothing was typed
	scr, _ := m.Screen(ScreenArgs{Session: "s1"})
	if strings.Contains(scr.Text, "DLTF") {
		t.Error("blocked text reached the screen")
	}
	// ordinary and WRK text is fine
	if _, err := m.Type(ctx, TypeArgs{Session: "s1", Text: "WRKACTJOB", Field: float64(1)}); err != nil {
		t.Errorf("WRK must pass: %v", err)
	}
	ss := m.Sessions()
	if !ss.ReadOnly || !strings.Contains(ss.Note, "read-only") {
		t.Errorf("sessions must say read-only: %+v", ss)
	}
	// typing into a protected area names the field table
	scr, _ = m.Screen(ScreenArgs{Session: "s1"})
	for _, f := range scr.Fields {
		if f.Bypass {
			_, err := m.Type(ctx, TypeArgs{Session: "s1", Text: "x", Field: float64(f.Index)})
			if err == nil || !strings.Contains(err.Error(), "protected") || !strings.Contains(err.Error(), "input fields") {
				t.Errorf("protected field: %v", err)
			}
			break
		}
	}
}

func TestSessionLimitAndErrors(t *testing.T) {
	host, port := startFake(t)
	m, _ := newTestManager(t, Config{MaxSessions: 1})
	connectPlain(t, m, host, port)
	signon := false
	if _, err := m.Connect(ctxT(t), ConnectArgs{Host: host, Port: port, Signon: &signon}); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Errorf("limit: %v", err)
	}
	if _, err := m.Screen(ScreenArgs{Session: "s9"}); err == nil || !strings.Contains(err.Error(), "s1") {
		t.Errorf("an unknown session names the open ones: %v", err)
	}
	if _, err := m.Connect(ctxT(t), ConnectArgs{}); err == nil || !strings.Contains(err.Error(), "no host") {
		t.Errorf("no host: %v", err)
	}
	if _, err := m.Connect(ctxT(t), ConnectArgs{Site: "nosuch"}); err == nil || !strings.Contains(err.Error(), "no saved site") {
		t.Errorf("unknown site: %v", err)
	}
	// connection refused is reported, and no session is left behind
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	refused := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	m2, _ := newTestManager(t, Config{})
	if _, err := m2.Connect(ctxT(t), ConnectArgs{Host: "127.0.0.1", Port: refused, Signon: &signon}); err == nil {
		t.Error("a refused connection must fail")
	}
	if len(m2.Sessions().Sessions) != 0 {
		t.Error("a failed connect leaves no session")
	}
}

func TestResolveTarget(t *testing.T) {
	yes := true
	f := &sites.File{
		Sites: []sites.Site{
			{Name: "TESTSYS", Host: "pub400.com", Port: 992, TLS: true, User: "SITEUSER", Password: "sitepw", ScreenSize: "24x80", DeviceName: "KWDEV1"},
			{Name: "PLAIN", Host: "127.0.0.1"},
		},
		Defaults: &sites.Defaults{ScreenSize: "27x132", CodePage: 273},
	}
	// The same file after the VS Code extension enrolled another user into TESTSYS.
	reEnrolled := &sites.File{
		Sites:    []sites.Site{{Name: "TESTSYS", Host: "pub400.com", Port: 992, TLS: true, User: "NEWUSER", Password: "newpw", ScreenSize: "24x80", DeviceName: "KWDEV1"}},
		Defaults: f.Defaults,
	}
	tests := []struct {
		name string
		cfg  Config
		args ConnectArgs
		want target
		fail string
	}{
		{"site values", Config{Sites: f}, ConnectArgs{Site: "testsys"},
			target{site: "TESTSYS", host: "pub400.com", port: 992, tls: true, user: "SITEUSER", password: "sitepw", size: "24x80", cp: 273, device: "KWDEV1"}, ""},
		{"args override the site", Config{Sites: f}, ConnectArgs{Site: "TESTSYS", Host: "127.0.0.1", Port: 23, ScreenSize: "27x132", User: "ME", Password: "pw"},
			target{site: "TESTSYS", host: "127.0.0.1", port: 23, tls: true, user: "ME", password: "pw", size: "27x132", cp: 273, device: "KWDEV1"}, ""},
		{"site defaults fill the rest", Config{Sites: f}, ConnectArgs{Site: "PLAIN"},
			target{site: "PLAIN", host: "127.0.0.1", port: 23, size: "27x132", cp: 273}, ""},
		{"config default site", Config{Sites: f, Site: "PLAIN"}, ConnectArgs{},
			target{site: "PLAIN", host: "127.0.0.1", port: 23, size: "27x132", cp: 273}, ""},
		{"host arg skips the default site", Config{Sites: f, Site: "TESTSYS", User: "CFG", Password: "cfgpw"}, ConnectArgs{Host: "127.0.0.1"},
			target{host: "127.0.0.1", port: 23, user: "CFG", password: "cfgpw", size: "27x132", cp: 273}, ""},
		{"config fills host and tls", Config{Host: "127.0.0.1", TLS: true, TrustAny: true, ScreenSize: "24x80", CodePage: 37}, ConnectArgs{},
			target{host: "127.0.0.1", port: 992, tls: true, trustAny: true, size: "24x80", cp: 37}, ""},
		{"tls arg beats config", Config{Host: "127.0.0.1", TLS: true}, ConnectArgs{TLS: new(bool)},
			target{host: "127.0.0.1", port: 23, size: "27x132", cp: 37}, ""},
		{"tls arg", Config{Host: "127.0.0.1"}, ConnectArgs{TLS: &yes},
			target{host: "127.0.0.1", port: 992, tls: true, size: "27x132", cp: 37}, ""},
		{"device template is dropped", Config{Host: "127.0.0.1", DeviceName: "&USERN*"}, ConnectArgs{},
			target{host: "127.0.0.1", port: 23, size: "27x132", cp: 37}, ""},
		{"reload picks up a site re-enrolled while the server runs", Config{Sites: f, Reload: func() (*sites.File, error) { return reEnrolled, nil }}, ConnectArgs{Site: "TESTSYS"},
			target{site: "TESTSYS", host: "pub400.com", port: 992, tls: true, user: "NEWUSER", password: "newpw", size: "24x80", cp: 273, device: "KWDEV1"}, ""},
		{"a failed reload keeps the sites loaded so far", Config{Sites: f, Reload: func() (*sites.File, error) { return nil, errors.New("disk") }}, ConnectArgs{Site: "TESTSYS"},
			target{site: "TESTSYS", host: "pub400.com", port: 992, tls: true, user: "SITEUSER", password: "sitepw", size: "24x80", cp: 273, device: "KWDEV1"}, ""},
		{"bad size", Config{Host: "127.0.0.1"}, ConnectArgs{ScreenSize: "80x24"}, target{}, "screen_size"},
		{"no host", Config{}, ConnectArgs{}, target{}, "no host"},
		{"unknown site lists the saved ones", Config{Sites: f}, ConnectArgs{Site: "x"}, target{}, "TESTSYS, PLAIN"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := newTestManager(t, tc.cfg)
			got, err := m.resolveTarget(tc.args)
			if tc.fail != "" {
				if err == nil || !strings.Contains(err.Error(), tc.fail) {
					t.Fatalf("want error %q, got %v", tc.fail, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Errorf("got  %+v\nwant %+v", got, tc.want)
			}
		})
	}
}

// fakeDialer hands the manager a link that records writes; the test feeds data itself.
type fakeDialer struct {
	opts   client.Options
	writes [][]byte
}

type fakeLink struct{ d *fakeDialer }

func (l fakeLink) WriteTraced(b, _ []byte) error {
	l.d.writes = append(l.d.writes, append([]byte(nil), b...))
	return nil
}
func (l fakeLink) Close() error { return nil }

func (d *fakeDialer) Dial(_ context.Context, o client.Options) (Link, error) {
	d.opts = o
	return fakeLink{d}, nil
}

func TestConfigDialerIsUsed(t *testing.T) {
	d := &fakeDialer{}
	m, _ := newTestManager(t, Config{Dialer: d, Host: "127.0.0.1", Port: 4000})
	signon := false
	// no data ever arrives: connect times out, and the dialer saw the right options
	_, err := m.Connect(ctxT(t), ConnectArgs{Signon: &signon, TimeoutMs: 200})
	if err == nil || !strings.Contains(err.Error(), "no screen") {
		t.Errorf("want a no-screen error, got %v", err)
	}
	if d.opts.Host != "127.0.0.1" || d.opts.Port != 4000 || d.opts.OnData == nil || d.opts.OnClosed == nil || !d.opts.KeepAlive {
		t.Errorf("dial options: %+v", d.opts)
	}
	if len(m.Sessions().Sessions) != 0 {
		t.Error("a connect that got no screen leaves no session")
	}
}

func TestSubscribeAndViewerAPI(t *testing.T) {
	host, port := startFake(t)
	m, _ := newTestManager(t, Config{})
	connectPlain(t, m, host, port)
	ch, cancel, err := m.Subscribe("s1")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	go func() {
		_, _ = m.Press(context.Background(), PressArgs{Session: "s1", Key: "enter", TimeoutMs: 5000})
	}()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("a subscriber must be woken by an update")
	}
	snap, err := m.Snapshot("s1")
	if err != nil || snap.Rows == 0 {
		t.Errorf("snapshot: %v", err)
	}
	if _, err := m.OIA("s1"); err != nil {
		t.Error(err)
	}
	if hs := m.Handles(); len(hs) != 1 || hs[0] != "s1" {
		t.Errorf("handles: %v", hs)
	}
	if _, _, err := m.Subscribe("nope"); err == nil {
		t.Error("unknown session")
	}
}

func TestHostClosesConnection(t *testing.T) {
	host, port := startFake(t)
	m, _ := newTestManager(t, Config{})
	connectPlain(t, m, host, port)
	s, _ := m.get("s1")
	s.linkMu.Lock()
	_ = s.link.Close() // the socket goes away under the session
	s.linkMu.Unlock()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if ok, _ := s.isConnected(); !ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the session never noticed the closed socket")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := m.Press(ctxT(t), PressArgs{Session: "s1", Key: "enter"}); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Errorf("press on a closed session: %v", err)
	}
	scr, err := m.Screen(ScreenArgs{Session: "s1"})
	if err != nil || scr.Closed == "" || scr.OIA.Connected {
		t.Errorf("the last screen stays readable and says it is closed: %v %+v", err, scr.Closed)
	}
	if ss := m.Sessions(); ss.Sessions[0].Connected {
		t.Error("sessions must show it disconnected")
	}
}

// A saved or default credential belongs to the endpoint it was saved for: the agent cannot
// aim it at another host, port or plain-text transport with the connect arguments.
func TestCredentialsBoundToEndpoint(t *testing.T) {
	no := false
	f := &sites.File{Sites: []sites.Site{
		{Name: "LAB", Host: "127.0.0.1", Port: 992, TLS: true, TrustAnyCertificate: true, User: "AGENT1", Password: "Fake-Pass-12345"},
	}}
	tests := []struct {
		name     string
		cfg      Config
		args     ConnectArgs
		user, pw string
		trustAny bool
		fail     string
	}{
		{"same endpoint keeps them", Config{Sites: f}, ConnectArgs{Site: "LAB"}, "AGENT1", "Fake-Pass-12345", true, ""},
		{"same endpoint spelled again", Config{Sites: f}, ConnectArgs{Site: "LAB", Host: "127.0.0.1", Port: 992}, "AGENT1", "Fake-Pass-12345", true, ""},
		{"other host", Config{Sites: f}, ConnectArgs{Site: "LAB", Host: "localhost"}, "", "", false, ""},
		{"other port", Config{Sites: f}, ConnectArgs{Site: "LAB", Port: 12042}, "", "", false, ""},
		{"tls off on the saved endpoint", Config{Sites: f}, ConnectArgs{Site: "LAB", TLS: &no}, "", "", false, "clear text"},
		{"tls off on another host", Config{Sites: f}, ConnectArgs{Site: "LAB", Host: "localhost", TLS: &no}, "", "", false, ""},
		{"other host with the call's own credentials", Config{Sites: f}, ConnectArgs{Site: "LAB", Host: "localhost", User: "ME", Password: "mine"}, "ME", "mine", false, ""},
		{"other host, call names only a user", Config{Sites: f}, ConnectArgs{Site: "LAB", Host: "localhost", User: "ME"}, "ME", "", false, ""},
		{"default credentials, same host", Config{Host: "127.0.0.1", User: "D", Password: "dpw"}, ConnectArgs{}, "D", "dpw", false, ""},
		{"default credentials, other host", Config{Host: "127.0.0.1", User: "D", Password: "dpw"}, ConnectArgs{Host: "localhost"}, "", "", false, ""},
		{"default credentials, other port", Config{Host: "127.0.0.1", User: "D", Password: "dpw"}, ConnectArgs{Port: 12042}, "", "", false, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := newTestManager(t, tc.cfg)
			got, err := m.resolveTarget(tc.args)
			if tc.fail != "" {
				if err == nil || !strings.Contains(err.Error(), tc.fail) {
					t.Fatalf("want error %q, got %v", tc.fail, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.user != tc.user || got.password != tc.pw || got.trustAny != tc.trustAny {
				t.Errorf("got user=%q password=%q trustAny=%v, want %q %q %v", got.user, got.password, got.trustAny, tc.user, tc.pw, tc.trustAny)
			}
		})
	}
}

// End to end: the password of a saved site never reaches a different host, and the audit
// line of the connect names the endpoint that was really used.
func TestSiteCredentialsNotSentElsewhere(t *testing.T) {
	host, port := startFake(t)
	f := &sites.File{Sites: []sites.Site{{Name: "LAB", Host: "127.0.0.2", Port: 1, User: "AGENT1", Password: "Fake-Pass-12345"}}}
	m, audit := newTestManager(t, Config{Sites: f})
	out, err := m.Connect(ctxT(t), ConnectArgs{Site: "LAB", Host: host, Port: port, TimeoutMs: 600})
	if err != nil {
		t.Fatal(err)
	}
	if out.SignedOn {
		t.Fatal("the saved password was typed into a sign-on on another host")
	}
	if !strings.Contains(audit.String(), `"port":`+strconv.Itoa(port)) || !strings.Contains(audit.String(), `"tls":false`) {
		t.Errorf("audit line lacks the resolved port and tls: %s", audit.String())
	}
}
