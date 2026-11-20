package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/DerKyberTakt/tn5250-tui/internal/client"
	"github.com/DerKyberTakt/tn5250-tui/internal/sites"
	"github.com/DerKyberTakt/tn5250-tui/internal/tn5250"
)

// Manager holds the sessions an agent drives. All methods are safe for concurrent use; a
// session serialises its own engine calls.
type Manager struct {
	cfg    Config
	dialer Dialer
	audit  *auditor
	file   *os.File

	mu       sync.Mutex
	sessions map[string]*session
	counter  int
	pending  int // connects in progress: they hold a slot before they have a session
	// refused remembers a sign-on the host turned down, by endpoint and user (see signonGate).
	refused map[string]refusal
}

// reapAfter is how long a session whose connection ended stays listed (so the agent can still
// read its last screen) before the manager forgets it.
var reapAfter = 60 * time.Second

// NewManager builds a manager. It opens Config.AuditFile when set.
func NewManager(cfg Config) (*Manager, error) {
	if cfg.MaxSessions <= 0 {
		cfg.MaxSessions = DefaultMaxSessions
	}
	m := &Manager{cfg: cfg, dialer: cfg.Dialer, sessions: map[string]*session{}, audit: &auditor{}}
	if m.dialer == nil {
		m.dialer = netDialer{}
	}
	var first io.Writer = os.Stderr
	if cfg.AuditWriter != nil {
		first = cfg.AuditWriter
	}
	m.audit.outs = append(m.audit.outs, first)
	if cfg.AuditFile != "" {
		f, err := os.OpenFile(cfg.AuditFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			return nil, fmt.Errorf("audit file: %w", err)
		}
		m.file = f
		m.audit.file = f
		m.audit.outs = append(m.audit.outs, f)
	}
	return m, nil
}

// Close ends every session and closes the audit file.
func (m *Manager) Close() {
	m.mu.Lock()
	all := make([]*session, 0, len(m.sessions))
	for _, s := range m.sessions {
		all = append(all, s)
	}
	m.sessions = map[string]*session{}
	m.mu.Unlock()
	// The server is going away (the agent's client closed): sign off where a command line is on
	// the screen, so the host does not keep the job and the user's device session.
	var typed []*session
	for _, s := range all {
		if alive, _ := s.isConnected(); alive && s.typeSignoff() {
			typed = append(typed, s)
			m.record(s, "disconnect", map[string]any{"signoff": true, "signed_off": true, "reason": "server exit"}, 0, nil)
		}
	}
	if len(typed) > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		for _, s := range typed {
			_, _ = settle(ctx, s, settleOpts{Max: 2 * time.Second})
		}
		cancel()
	}
	for _, s := range all {
		s.close()
	}
	if m.file != nil {
		_ = m.file.Close()
	}
}

// ReadOnly reports whether guarded commands are refused.
func (m *Manager) ReadOnly() bool { return m.cfg.ReadOnly }

// session is one 5250 connection and its engine.
type session struct {
	handle string
	eng    *tn5250.Session
	notif  *notifier
	trace  *ring
	start  time.Time
	site   string
	host   string
	port   int

	mu sync.Mutex // serialises compound engine operations (type, fill, sign-on)

	linkMu    sync.Mutex
	link      Link
	linkReady chan struct{}

	stateMu   sync.Mutex
	connected bool
	closedErr string
	closedAt  time.Time
	closed    bool
}

func (s *session) isConnected() (bool, string) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	return s.connected, s.closedErr
}

func (s *session) markClosed(reason string) {
	s.stateMu.Lock()
	if s.connected {
		s.connected = false
		s.closedErr = reason
		s.closedAt = time.Now()
	}
	s.stateMu.Unlock()
	s.notif.Bump()
}

func (s *session) close() {
	s.linkMu.Lock()
	l := s.link
	s.linkMu.Unlock()
	s.markClosed("closed by the agent")
	if l != nil {
		_ = l.Close()
	}
}

// State implements settleSource.
func (s *session) State() (uint64, string, bool) {
	alive, _ := s.isConnected()
	return s.notif.Seq(), s.eng.OIA(alive).InputInhibited, alive
}

// Changed implements settleSource.
func (s *session) Changed() <-chan struct{} { return s.notif.Changed() }

func (s *session) snapshot() tn5250.Snapshot { return s.eng.Snapshot() }

func (s *session) oia() tn5250.OIA {
	alive, _ := s.isConnected()
	return s.eng.OIA(alive)
}

// ---- lookups ---------------------------------------------------------------------------

func (m *Manager) get(handle string) (*session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[strings.TrimSpace(handle)]
	if !ok {
		names := make([]string, 0, len(m.sessions))
		for h := range m.sessions {
			names = append(names, h)
		}
		sort.Strings(names)
		if len(names) == 0 {
			return nil, fmt.Errorf("no session %q: nothing is connected; call connect first", handle)
		}
		return nil, fmt.Errorf("no session %q (open: %s)", handle, strings.Join(names, ", "))
	}
	return s, nil
}

// live is get for a session that must still be connected.
func (m *Manager) live(handle string) (*session, error) {
	s, err := m.get(handle)
	if err != nil {
		return nil, err
	}
	if ok, why := s.isConnected(); !ok {
		return nil, fmt.Errorf("session %s is closed (%s); read it with screen or end it with disconnect", s.handle, why)
	}
	return s, nil
}

func timeoutOf(ms int) time.Duration {
	if ms <= 0 {
		return DefaultTimeout
	}
	d := time.Duration(ms) * time.Millisecond
	if d > MaxTimeout {
		d = MaxTimeout
	}
	return d
}

// screenOf formats the session's screen now.
func (s *session) screenOf(o ScreenOpts) (ScreenOut, error) {
	out, err := formatScreen(s.snapshot(), s.oia(), o)
	if err != nil {
		return out, err
	}
	if _, why := s.isConnected(); why != "" {
		out.Closed = why
	}
	return out, nil
}

// ---- audit -----------------------------------------------------------------------------

func (m *Manager) record(s *session, tool string, args map[string]any, before uint64, err error) {
	e := AuditEntry{Tool: tool, Args: args, SeqBefore: before}
	if s != nil {
		e.Session = s.handle
		e.SeqAfter = s.notif.Seq()
	}
	if err != nil {
		e.Error = err.Error()
	}
	m.audit.Log(e)
}

// ---- connect ---------------------------------------------------------------------------

// ConnectArgs is the input of connect.
type ConnectArgs struct {
	Site       string `json:"site,omitempty" jsonschema:"name of a saved site; its values apply, the other arguments override them"`
	Host       string `json:"host,omitempty" jsonschema:"host name or address when no site is used"`
	Port       int    `json:"port,omitempty" jsonschema:"TCP port (default 23, or 992 with tls)"`
	TLS        *bool  `json:"tls,omitempty" jsonschema:"telnet over TLS"`
	User       string `json:"user,omitempty" jsonschema:"user profile for the sign-on"`
	Password   string `json:"password,omitempty" jsonschema:"password for the sign-on; never echoed or logged"`
	ScreenSize string `json:"screen_size,omitempty" jsonschema:"24x80 or 27x132"`
	CodePage   int    `json:"code_page,omitempty" jsonschema:"EBCDIC code page (default 37)"`
	DeviceName string `json:"device_name,omitempty" jsonschema:"workstation name to request, up to 10 letters or digits"`
	Signon     *bool  `json:"signon,omitempty" jsonschema:"type the sign-on (default true when a user and password are known)"`
	TimeoutMs  int    `json:"timeout_ms,omitempty" jsonschema:"how long to wait for the host (default 15000)"`
}

// ConnectOut is the output of connect.
type ConnectOut struct {
	Session  string    `json:"session"`
	Device   string    `json:"device,omitempty"`
	Size     string    `json:"size"`
	SignedOn bool      `json:"signed_on" jsonschema:"true when connect typed the sign-on and the host accepted it"`
	Screen   ScreenOut `json:"screen"`
}

type target struct {
	site     string
	host     string
	port     int
	tls      bool
	trustAny bool
	user     string
	password string
	size     string
	cp       int
	device   string
}

var deviceRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]{0,9}$`)

// resolveTarget merges the saved site (when one is named), the call's arguments and the
// configuration defaults, in that order of precedence: arguments, then the site, then Config.
func (m *Manager) resolveTarget(a ConnectArgs) (target, error) {
	cfg := m.cfg
	siteName := strings.TrimSpace(a.Site)
	if siteName == "" && a.Host == "" {
		siteName = cfg.Site
	}
	var t target
	haveSite := false
	var siteHost string
	var sitePort int
	var siteTLS bool
	if siteName != "" {
		if cfg.Reload != nil {
			if f, err := cfg.Reload(); err == nil && f != nil {
				m.mu.Lock()
				m.cfg.Sites = f
				m.mu.Unlock()
				cfg.Sites = f
			}
		}
		var st sites.Site
		ok := false
		if cfg.Sites != nil {
			st, ok = cfg.Sites.Find(siteName)
		}
		if !ok {
			var names []string
			if cfg.Sites != nil {
				for _, s := range cfg.Sites.Sites {
					names = append(names, s.Name)
				}
			}
			if len(names) == 0 {
				return t, fmt.Errorf("no saved site %q (there are no saved sites; pass host)", siteName)
			}
			return t, fmt.Errorf("no saved site %q (saved: %s)", siteName, strings.Join(names, ", "))
		}
		haveSite = true
		siteHost, siteTLS = st.Host, st.TLS
		sitePort = cfg.Sites.Complete(sites.Site{Port: st.Port, TLS: st.TLS}).Port
		t = target{site: st.Name, host: st.Host, port: st.Port, tls: st.TLS, trustAny: st.TrustAnyCertificate,
			user: st.User, password: st.Password, size: st.ScreenSize, cp: st.CodePage, device: st.DeviceName}
	}
	if a.Host != "" {
		t.host = a.Host
	}
	if a.Port != 0 {
		t.port = a.Port
	}
	if a.TLS != nil {
		t.tls = *a.TLS
	}
	if a.User != "" {
		t.user = a.User
	}
	if a.Password != "" {
		t.password = a.Password
	}
	if a.ScreenSize != "" {
		t.size = a.ScreenSize
	}
	if a.CodePage != 0 {
		t.cp = a.CodePage
	}
	if a.DeviceName != "" {
		t.device = a.DeviceName
	}
	// Config defaults fill what is still empty. The yes/no settings only apply when no site
	// was named: a site's own TLS setting wins over the process default.
	if t.host == "" {
		t.host = cfg.Host
	}
	if t.port == 0 {
		t.port = cfg.Port
	}
	if !haveSite {
		if a.TLS == nil {
			t.tls = t.tls || cfg.TLS
		}
		t.trustAny = t.trustAny || cfg.TrustAny
	}
	defaultCreds := false
	if t.user == "" {
		t.user = cfg.User
		defaultCreds = defaultCreds || cfg.User != ""
	}
	if t.password == "" {
		t.password = cfg.Password
		defaultCreds = defaultCreds || cfg.Password != ""
	}
	if t.size == "" {
		t.size = cfg.ScreenSize
	}
	if t.cp == 0 {
		t.cp = cfg.CodePage
	}
	if t.device == "" {
		t.device = cfg.DeviceName
	}
	if t.host == "" {
		return t, errors.New("no host: pass site or host, or start the server with a default site or host")
	}
	done := cfg.Sites.Complete(sites.Site{Port: t.port, TLS: t.tls, ScreenSize: t.size, CodePage: t.cp, DeviceName: t.device})
	t.port, t.size, t.cp, t.device = done.Port, done.ScreenSize, done.CodePage, done.DeviceName
	// A stored credential belongs to the endpoint it was saved for: host, port and TLS. A call
	// that points the connection elsewhere does not carry the saved user, password or
	// trust-any-certificate setting along (the sign-on is then skipped unless the call brings its
	// own user and password). Dropping TLS on the saved endpoint is refused outright.
	if haveSite {
		sameEndpoint := strings.EqualFold(t.host, siteHost) && t.port == sitePort
		if sameEndpoint && siteTLS && !t.tls {
			return t, fmt.Errorf("site %q uses TLS; tls=false would send its credentials in clear text (refused)", t.site)
		}
		if !sameEndpoint || t.tls != siteTLS {
			t.trustAny = cfg.TrustAny
			// The call's own password is the agent's choice; anything else (the saved one, a
			// default) stays home, also when the call only names a different user.
			if a.Password == "" {
				t.password = ""
				if a.User == "" {
					t.user = ""
				}
			}
		}
	}
	if defaultCreds && cfg.Host != "" {
		def := cfg.Sites.Complete(sites.Site{Port: cfg.Port, TLS: cfg.TLS})
		if !strings.EqualFold(t.host, cfg.Host) || t.port != def.Port || t.tls != cfg.TLS {
			if a.User == "" && t.user == cfg.User {
				t.user = ""
			}
			if a.Password == "" && t.password == cfg.Password {
				t.password = ""
			}
		}
	}
	if t.size != "24x80" && t.size != "27x132" {
		return t, fmt.Errorf("screen_size %q is not supported (use 24x80 or 27x132)", t.size)
	}
	if t.device != "" && !deviceRe.MatchString(t.device) {
		t.device = "" // a template or invalid name: let the host assign one
	}
	return t, nil
}

func (m *Manager) liveCount() int {
	n := 0
	for _, s := range m.sessions {
		if ok, _ := s.isConnected(); ok {
			n++
		}
	}
	return n
}

// Connect opens a session, waits for the first unlocked screen and, when asked, signs on.
func (m *Manager) Connect(ctx context.Context, a ConnectArgs) (out ConnectOut, err error) {
	if err := m.audit.check(); err != nil {
		return out, err
	}
	var s *session
	var tlsUsed bool
	defer func() {
		args := map[string]any{"site": a.Site, "host": a.Host, "user": a.User}
		if a.Port != 0 {
			args["port"] = a.Port
		}
		if a.TLS != nil {
			args["tls"] = *a.TLS
		}
		if s != nil {
			args["site"], args["host"], args["port"] = s.site, s.host, s.port
			args["tls"] = tlsUsed
		}
		m.record(s, "connect", args, 0, err)
	}()
	t, err := m.resolveTarget(a)
	if err != nil {
		return out, err
	}
	tlsUsed = t.tls
	signon := t.user != "" && t.password != ""
	if a.Signon != nil {
		signon = *a.Signon
		if signon && (t.user == "" || t.password == "") {
			return out, errors.New("signon needs a user and a password (pass them, or save them in the site)")
		}
	}
	timeout := timeoutOf(a.TimeoutMs)
	if signon {
		if err := m.signonGate(t); err != nil {
			return out, err
		}
	}

	m.mu.Lock()
	if m.liveCount()+m.pending >= m.cfg.MaxSessions {
		m.mu.Unlock()
		return out, fmt.Errorf("session limit reached (%d); disconnect one first", m.cfg.MaxSessions)
	}
	m.counter++
	m.pending++
	handle := fmt.Sprintf("s%d", m.counter)
	m.mu.Unlock()
	registered := false
	defer func() {
		if !registered {
			m.mu.Lock()
			m.pending--
			m.mu.Unlock()
		}
	}()

	s = &session{handle: handle, notif: newNotifier(), trace: newRing(TraceLines), start: time.Now(),
		site: t.site, host: t.host, port: t.port, linkReady: make(chan struct{}), connected: true}
	sess := s // the callbacks keep this one: s itself is cleared on the failure paths
	eng, err := tn5250.New(tn5250.Options{ScreenSize: t.size, CodePage: t.cp, DeviceName: t.device}, tn5250.Events{
		Send: sess.send,
		Update: func() {
			sess.notif.Bump()
		},
		Trace: func(line string) { sess.trace.Add("ENG " + line) },
		Error: func(e error) {
			sess.trace.Add("ERR " + e.Error())
			sess.close()
		},
	})
	if err != nil {
		s = nil
		return out, err
	}
	s.eng = eng

	link, err := m.dialer.Dial(ctx, client.Options{
		Host: t.host, Port: t.port, TLS: t.tls, TrustAnyCertificate: t.trustAny,
		TimeoutMs: int(timeout / time.Millisecond), KeepAlive: true,
		OnData: func(b []byte) {
			sess.trace.Add(traceBytes("IN ", b))
			sess.eng.Feed(b)
		},
		OnClosed: func(e error) {
			why := "the host closed the connection"
			if e != nil {
				why = "connection lost: " + e.Error()
			}
			sess.trace.Add("EOF " + why)
			sess.markClosed(why)
		},
	})
	if err != nil {
		s.linkMu.Lock()
		close(s.linkReady)
		s.linkMu.Unlock()
		s = nil
		return out, fmt.Errorf("connect %s:%d: %w", t.host, t.port, err)
	}
	s.linkMu.Lock()
	s.link = link
	close(s.linkReady)
	s.linkMu.Unlock()

	m.mu.Lock()
	m.sessions[handle] = s
	m.pending--
	m.mu.Unlock()
	registered = true

	res, err := settle(ctx, s, settleOpts{Max: timeout, Ready: func() bool { return strings.TrimSpace(s.snapshot().Text()) != "" }})
	if err != nil {
		m.drop(s)
		return out, err
	}
	if res.Closed {
		_, why := s.isConnected()
		m.drop(s)
		return out, fmt.Errorf("the host closed the connection before the first screen (%s)", why)
	}
	if strings.TrimSpace(s.snapshot().Text()) == "" {
		m.drop(s)
		return out, fmt.Errorf("no screen from %s:%d within %s", t.host, t.port, timeout)
	}
	out.Session, out.Size = handle, fmt.Sprintf("%dx%d", s.snapshot().Rows, s.snapshot().Cols)
	settled := res.Settled
	if signon {
		if serr := s.typedSignon(t.user, t.password); serr != nil {
			err = fmt.Errorf("connected as %s but the sign-on was refused: %w; the session stays open (use screen, type, fill)", handle, serr)
			return out, err
		}
		res, err = settle(ctx, s, settleOpts{Max: timeout})
		if err != nil {
			return out, err
		}
		settled = res.Settled
		// The host answers a refused sign-on with the sign-on display and a message. Only a
		// settled sign-on display counts as a refusal: a host that is still working must not be
		// taken for one (signed_on is then false, the session stays open, nothing is remembered).
		s.mu.Lock()
		snap := s.snapshot()
		s.mu.Unlock()
		msg, refused, limit := signonRefusal(snap)
		out.SignedOn = !refused
		if settled {
			if refused {
				m.drop(s)
				s.close()
				user := strings.ToUpper(t.user)
				if limit {
					return out, fmt.Errorf("the host refused the sign-on: too many sessions for user %s (%s). Sign off or close one of that user's sessions (sessions lists the agent's own), then connect again; this connection was closed", user, msg)
				}
				m.noteRefused(t, msg)
				return out, fmt.Errorf("the host refused the sign-on for user %s (%s). This connection was closed and connect will not try this password again: every refused attempt counts toward disabling the profile. Tell the user; do not retry", user, msg)
			}
		}
	}
	scr, err := s.screenOf(ScreenOpts{})
	if err != nil {
		return out, err
	}
	scr.Settled = &settled
	out.Screen, out.Device = scr, scr.OIA.Device
	return out, nil
}

// drop removes a session that never became usable.
func (m *Manager) drop(s *session) {
	m.mu.Lock()
	delete(m.sessions, s.handle)
	m.mu.Unlock()
	s.close()
}

// send is the engine's Events.Send: trace the masked frame, write the real one.
func (s *session) send(b, forTrace []byte) {
	shown := forTrace
	if shown == nil {
		shown = b
	}
	s.trace.Add(traceBytes("OUT", shown))
	<-s.linkReady
	s.linkMu.Lock()
	l := s.link
	s.linkMu.Unlock()
	if l == nil {
		return
	}
	if err := l.WriteTraced(b, forTrace); err != nil {
		s.trace.Add("ERR write: " + err.Error())
	}
}

var errNoSignon = errors.New("the screen has no user and password fields (the second input field is not non-display)")

// typedSignon types user and password into the first two input fields and presses Enter. It
// refuses unless the second input field is non-display.
func (s *session) typedSignon(user, password string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap := s.snapshot()
	var in []tn5250.FieldInfo
	for _, f := range snap.Fields {
		if !f.Bypass {
			in = append(in, f)
		}
	}
	if len(in) < 2 || !in[1].Nondisplay || snap.Cols <= 0 {
		return errNoSignon
	}
	for i, text := range []string{user, password} {
		s.eng.SetCursor(in[i].Start/snap.Cols, in[i].Start%snap.Cols)
		s.eng.Press(tn5250.ActionEraseEOF)
		s.eng.Type(text)
	}
	s.eng.Press(tn5250.ActionEnter)
	return nil
}

// ---- reading ---------------------------------------------------------------------------

// ScreenArgs is the input of screen.
type ScreenArgs struct {
	Session    string `json:"session" jsonschema:"session handle from connect (s1, s2, ...)"`
	Rows       string `json:"rows,omitempty" jsonschema:"only these rows, 1-based: \"5\", \"4-20\", \"4-\" or \"-10\""`
	FieldsOnly bool   `json:"fields_only,omitempty" jsonschema:"return only the field table, not the text"`
}

// Screen returns the current screen without touching the host.
func (m *Manager) Screen(a ScreenArgs) (ScreenOut, error) {
	s, err := m.get(a.Session)
	if err != nil {
		return ScreenOut{}, err
	}
	return s.screenOf(ScreenOpts{Rows: a.Rows, FieldsOnly: a.FieldsOnly})
}

// Snapshot is the engine snapshot of a session (for a viewer).
func (m *Manager) Snapshot(handle string) (tn5250.Snapshot, error) {
	s, err := m.get(handle)
	if err != nil {
		return tn5250.Snapshot{}, err
	}
	return s.snapshot(), nil
}

// OIA is the status line of a session (for a viewer).
func (m *Manager) OIA(handle string) (tn5250.OIA, error) {
	s, err := m.get(handle)
	if err != nil {
		return tn5250.OIA{}, err
	}
	return s.oia(), nil
}

// Subscribe returns a channel that gets a signal after every screen or status change of the
// session, and a cancel function. A slow reader sees at most one pending signal; it should
// read Snapshot when woken.
func (m *Manager) Subscribe(handle string) (<-chan struct{}, func(), error) {
	s, err := m.get(handle)
	if err != nil {
		return nil, nil, err
	}
	c, cancel := s.notif.Subscribe()
	return c, cancel, nil
}

// Handles lists the open session handles in order of creation.
func (m *Manager) Handles() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	hs := make([]string, 0, len(m.sessions))
	for h := range m.sessions {
		hs = append(hs, h)
	}
	sort.Slice(hs, func(i, j int) bool { return handleNum(hs[i]) < handleNum(hs[j]) })
	return hs
}

func handleNum(h string) int {
	n := 0
	fmt.Sscanf(h, "s%d", &n)
	return n
}

// SessionInfo is one row of sessions.
type SessionInfo struct {
	Session   string `json:"session"`
	Site      string `json:"site,omitempty"`
	Host      string `json:"host"`
	Port      int    `json:"port"`
	Device    string `json:"device,omitempty"`
	Connected bool   `json:"connected"`
	Closed    string `json:"closed,omitempty"`
	Since     string `json:"since"`
	Seq       uint64 `json:"seq"`
	Inhibited string `json:"inhibited"`
}

// SessionsOut is the output of sessions.
type SessionsOut struct {
	Sessions []SessionInfo `json:"sessions"`
	ReadOnly bool          `json:"readOnly" jsonschema:"true when the server refuses typing that starts a guarded command"`
	Note     string        `json:"note,omitempty"`
}

// Sessions lists the open sessions.
func (m *Manager) Sessions() SessionsOut {
	out := SessionsOut{Sessions: []SessionInfo{}, ReadOnly: m.cfg.ReadOnly}
	if m.cfg.ReadOnly {
		out.Note = "read-only mode: typing that starts " + strings.Join(guardedVerbs, "/") + " is refused; ask the user to run those"
	}
	m.reap()
	for _, h := range m.Handles() {
		s, err := m.get(h)
		if err != nil {
			continue
		}
		alive, why := s.isConnected()
		o := s.eng.OIA(alive)
		out.Sessions = append(out.Sessions, SessionInfo{
			Session: s.handle, Site: s.site, Host: s.host, Port: s.port, Device: o.DeviceName,
			Connected: alive, Closed: why, Since: s.start.Format(time.RFC3339), Seq: s.notif.Seq(),
			Inhibited: inhibitedOr(o.InputInhibited),
		})
	}
	return out
}

// reap forgets sessions whose connection ended more than reapAfter ago, so a list (and a
// viewer) never shows a long-dead session beside a new one.
func (m *Manager) reap() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for h, s := range m.sessions {
		s.stateMu.Lock()
		dead := !s.connected && !s.closedAt.IsZero() && time.Since(s.closedAt) > reapAfter
		s.stateMu.Unlock()
		if dead {
			delete(m.sessions, h)
		}
	}
}

// End closes a session on behalf of a person watching it (the viewer's "end" request, the
// extension's Sign Off and Close): SIGNOFF is typed first when signoff is set and a command
// line is on the screen. It is recorded in the audit log like the agent's own disconnect.
func (m *Manager) End(handle string, signoff bool) (signedOff bool, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := m.Disconnect(ctx, DisconnectArgs{Session: handle, Signoff: signoff})
	return out.SignedOff, err
}

// typeSignoff types SIGNOFF and Enter when the screen has a command line; it reports whether
// it did.
func (s *session) typeSignoff() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap := s.snapshot()
	for _, v := range fieldViews(snap) {
		if !v.Bypass && isCommandField(v) {
			s.typeInto(snap, v, "SIGNOFF", true, true)
			s.eng.Press(tn5250.ActionEnter)
			return true
		}
	}
	return false
}

func inhibitedOr(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

// FindArgs is the input of find.
type FindArgs struct {
	Session       string `json:"session"`
	Text          string `json:"text" jsonschema:"the text to look for"`
	CaseSensitive bool   `json:"case_sensitive,omitempty"`
}

// FindOut is the output of find.
type FindOut struct {
	Hits []Hit `json:"hits"`
}

// Find searches the current screen for text.
func (m *Manager) Find(a FindArgs) (FindOut, error) {
	s, err := m.get(a.Session)
	if err != nil {
		return FindOut{}, err
	}
	if a.Text == "" {
		return FindOut{}, errors.New("find needs text")
	}
	return FindOut{Hits: findText(s.snapshot(), a.Text, a.CaseSensitive)}, nil
}

// TraceArgs is the input of trace.
type TraceArgs struct {
	Session   string `json:"session"`
	Lines     int    `json:"lines,omitempty" jsonschema:"how many lines (default 50, at most 200; from_start keeps at most 60)"`
	FromStart bool   `json:"from_start,omitempty" jsonschema:"the first lines of the session instead of the last: the telnet negotiation (terminal type, NEW-ENVIRON with KBDTYPE/CODEPAGE/CHARSET), which the ring has dropped by the first screen"`
}

// TraceOut is the output of trace.
type TraceOut struct {
	Lines []string `json:"lines"`
}

// Trace returns the last masked trace lines of a session.
func (m *Manager) Trace(a TraceArgs) (TraceOut, error) {
	s, err := m.get(a.Session)
	if err != nil {
		return TraceOut{}, err
	}
	n := a.Lines
	if n <= 0 {
		n = 50
	}
	if n > TraceLines {
		n = TraceLines
	}
	if a.FromStart {
		return TraceOut{Lines: s.trace.First(n)}, nil
	}
	return TraceOut{Lines: s.trace.Last(n)}, nil
}

// DumpArgs is the input of dump.
type DumpArgs struct {
	Session string `json:"session"`
}

// DumpOut is the output of dump.
type DumpOut struct {
	Dump   string `json:"dump" jsonschema:"the rows, then a line --attr--, then one line of attribute hex (2 digits per cell) per row"`
	Size   string `json:"size"`
	Cursor Pos    `json:"cursor"`
	Seq    uint64 `json:"seq"`
}

// Dump returns the whole screen with the attribute byte of every cell.
func (m *Manager) Dump(a DumpArgs) (DumpOut, error) {
	s, err := m.get(a.Session)
	if err != nil {
		return DumpOut{}, err
	}
	snap := s.snapshot()
	return DumpOut{Dump: dumpScreen(snap), Size: fmt.Sprintf("%dx%d", snap.Rows, snap.Cols),
		Cursor: Pos{Row: snap.Cursor.Row + 1, Col: snap.Cursor.Col + 1}, Seq: snap.Seq}, nil
}

// ---- keys and typing -------------------------------------------------------------------

// PressArgs is the input of press.
type PressArgs struct {
	Session   string `json:"session"`
	Key       string `json:"key" jsonschema:"enter, f1..f24, pageup, pagedown, tab, backtab, fieldexit, field+, field-, home, attn, sysreq, reset, clear, help, eraseeof, eraseinput, dup, fieldmark (case-insensitive)"`
	Times     int    `json:"times,omitempty" jsonschema:"press the key this many times, 1 to 50 (default 1); stops early when the host does not settle"`
	TimeoutMs int    `json:"timeout_ms,omitempty" jsonschema:"how long to wait for the host after a key that sends (default 15000)"`
}

// settleNow runs settle on the session and turns it into the settled flag.
func (m *Manager) settleNow(ctx context.Context, s *session, timeout time.Duration) (settleResult, error) {
	return settle(ctx, s, settleOpts{Max: timeout})
}

// finish builds the screen after an action; settled is nil when no wait took place.
func (m *Manager) finish(s *session, settled *bool) (ScreenOut, error) {
	out, err := s.screenOf(ScreenOpts{})
	if err != nil {
		return out, err
	}
	out.Settled = settled
	return out, nil
}

// Press presses a key (or several times) and, for a key that sends to the host, waits for the
// host to settle.
func (m *Manager) Press(ctx context.Context, a PressArgs) (out ScreenOut, err error) {
	if err := m.audit.check(); err != nil {
		return out, err
	}
	s, err := m.live(a.Session)
	if err != nil {
		return out, err
	}
	before := s.notif.Seq()
	defer func() {
		m.record(s, "press", map[string]any{"key": a.Key, "times": a.Times}, before, err)
	}()
	act, err := ResolveKey(a.Key)
	if err != nil {
		return out, err
	}
	times := a.Times
	if times == 0 {
		times = 1
	}
	if times < 1 || times > 50 {
		return out, fmt.Errorf("times must be 1 to 50, got %d", a.Times)
	}
	timeout := timeoutOf(a.TimeoutMs)
	var settled *bool
	wasLocked := s.lockedNow()
	for i := 0; i < times; i++ {
		s.mu.Lock()
		s.eng.Press(act)
		s.mu.Unlock()
		if postsToHost(act) {
			res, e := m.settleNow(ctx, s, timeout)
			if e != nil {
				return out, e
			}
			ok := res.Settled
			settled = &ok
			if !ok {
				break
			}
		}
	}
	settled, err = m.settleIfLocked(ctx, s, timeout, wasLocked, settled)
	if err != nil {
		return out, err
	}
	return m.finish(s, settled)
}

// lockedNow reports whether the host has the keyboard (X SYSTEM).
func (s *session) lockedNow() bool { return s.oia().InputInhibited == "system" }

// settleIfLocked waits when this action itself handed the keyboard to the host without a
// wait (typed text that filled an auto-enter field): the keyboard was open before (wasLocked
// false) and is locked now. A keyboard that was already locked stays the caller's business:
// keys then only queue as type-ahead, and the call returns at once.
func (m *Manager) settleIfLocked(ctx context.Context, s *session, timeout time.Duration, wasLocked bool, settled *bool) (*bool, error) {
	if settled != nil || wasLocked {
		return settled, nil
	}
	if alive, _ := s.isConnected(); !alive || !s.lockedNow() {
		return nil, nil
	}
	res, err := m.settleNow(ctx, s, timeout)
	if err != nil {
		return nil, err
	}
	ok := res.Settled
	return &ok, nil
}

// parseField reads the type tool's field argument: nil/""/"cursor" = the cursor; a number or
// numeric string = a field index; any other string = a label.
func parseField(v any) (ref fieldRef, atCursor bool, err error) {
	switch x := v.(type) {
	case nil:
		return ref, true, nil
	case float64:
		if x != float64(int(x)) || x < 1 {
			return ref, false, fmt.Errorf("field %v: use a whole number from 1, \"cursor\", or a label", x)
		}
		return fieldRef{Index: int(x)}, false, nil
	case int:
		return fieldRef{Index: x}, false, nil
	case string:
		t := strings.TrimSpace(x)
		if t == "" || strings.EqualFold(t, "cursor") {
			return ref, true, nil
		}
		n := 0
		if _, e := fmt.Sscanf(t, "%d", &n); e == nil && fmt.Sprint(n) == t {
			return fieldRef{Index: n}, false, nil
		}
		return fieldRef{Label: t}, false, nil
	}
	return ref, false, fmt.Errorf("field must be a number, \"cursor\" or a label")
}

// TypeArgs is the input of type.
type TypeArgs struct {
	Session   string `json:"session"`
	Text      string `json:"text" jsonschema:"the text to type"`
	Field     any    `json:"field,omitempty" jsonschema:"field index (1-based, from the field table), a label, or \"cursor\" (default) to type where the cursor is"`
	Clear     *bool  `json:"clear,omitempty" jsonschema:"erase the field first (default true when field is given)"`
	Then      string `json:"then,omitempty" jsonschema:"key to press afterwards: enter, tab, fieldexit or empty"`
	TimeoutMs int    `json:"timeout_ms,omitempty"`
}

// Type types text into a field (or at the cursor) and optionally presses a key.
func (m *Manager) Type(ctx context.Context, a TypeArgs) (out ScreenOut, err error) {
	if err := m.audit.check(); err != nil {
		return out, err
	}
	s, err := m.live(a.Session)
	if err != nil {
		return out, err
	}
	before := s.notif.Seq()
	args := map[string]any{"then": a.Then}
	defer func() {
		m.record(s, "type", args, before, err)
	}()
	then, err := optionalKey(a.Then)
	if err != nil {
		return out, err
	}
	ref, atCursor, err := parseField(a.Field)
	if err != nil {
		return out, err
	}
	timeout := timeoutOf(a.TimeoutMs)

	s.mu.Lock()
	wasLocked := s.lockedNow()
	snap := s.snapshot()
	views := fieldViews(snap)
	var target fieldView
	if atCursor {
		var ok bool
		target, ok = fieldAtCursor(snap, views)
		if !ok || target.Bypass {
			s.mu.Unlock()
			return out, fmt.Errorf("the cursor (row %d col %d) is not in an input field. %s", snap.Cursor.Row+1, snap.Cursor.Col+1, fieldTable(views))
		}
	} else {
		target, err = resolveField(views, ref)
		if err != nil {
			s.mu.Unlock()
			return out, err
		}
	}
	args["field"], args["text"] = target.Index, auditText(a.Text, target.Nondisplay)
	if m.cfg.ReadOnly {
		if err = checkReadOnly(a.Text); err != nil {
			s.mu.Unlock()
			return out, err
		}
	}
	doClear := !atCursor
	if a.Clear != nil {
		doClear = *a.Clear
	}
	s.typeInto(snap, target, a.Text, doClear, !atCursor)
	if then != "" {
		s.eng.Press(then)
	}
	s.mu.Unlock()

	var settled *bool
	if then != "" && postsToHost(then) {
		res, e := m.settleNow(ctx, s, timeout)
		if e != nil {
			return out, e
		}
		ok := res.Settled
		settled = &ok
	}
	if settled, err = m.settleIfLocked(ctx, s, timeout, wasLocked, settled); err != nil {
		return out, err
	}
	return m.finish(s, settled)
}

// typeInto moves to the field (when move), optionally erases it, and types. The caller holds
// s.mu.
func (s *session) typeInto(snap tn5250.Snapshot, f fieldView, text string, doClear, move bool) {
	if move && snap.Cols > 0 {
		s.eng.SetCursor(f.Start/snap.Cols, f.Start%snap.Cols)
	}
	if doClear {
		s.eng.Press(tn5250.ActionEraseEOF)
	}
	s.eng.Type(text)
}

// optionalKey resolves the "then" key; empty means none.
func optionalKey(name string) (tn5250.Action, error) {
	if strings.TrimSpace(name) == "" {
		return "", nil
	}
	return ResolveKey(name)
}

// FillItem is one field of fill.
type FillItem struct {
	Index int    `json:"index,omitempty" jsonschema:"1-based field number"`
	Row   int    `json:"row,omitempty" jsonschema:"row of the field (with col)"`
	Col   int    `json:"col,omitempty"`
	Label string `json:"label,omitempty" jsonschema:"the text just left of the field, e.g. \"User\" (case-insensitive, trailing colon or dots ignored)"`
	Text  string `json:"text" jsonschema:"the text for the field (empty clears it)"`
}

// FillArgs is the input of fill.
type FillArgs struct {
	Session   string     `json:"session"`
	Fields    []FillItem `json:"fields" jsonschema:"fields to fill, in order"`
	Then      string     `json:"then,omitempty" jsonschema:"key to press afterwards: enter, tab, fieldexit or empty"`
	TimeoutMs int        `json:"timeout_ms,omitempty"`
}

// Fill clears and fills several fields, then optionally presses a key. Every field is
// resolved and checked before anything is typed.
func (m *Manager) Fill(ctx context.Context, a FillArgs) (out ScreenOut, err error) {
	if err := m.audit.check(); err != nil {
		return out, err
	}
	s, err := m.live(a.Session)
	if err != nil {
		return out, err
	}
	before := s.notif.Seq()
	audit := []map[string]any{}
	defer func() {
		if len(audit) == 0 { // refused before typing: log what was asked for, lengths only
			for _, it := range a.Fields {
				audit = append(audit, map[string]any{"index": it.Index, "row": it.Row, "col": it.Col, "label": it.Label,
					"text": fmt.Sprintf("<%d chars>", len([]rune(it.Text)))})
			}
		}
		m.record(s, "fill", map[string]any{"fields": audit, "then": a.Then}, before, err)
	}()
	if len(a.Fields) == 0 {
		return out, errors.New("fill needs at least one field")
	}
	then, err := optionalKey(a.Then)
	if err != nil {
		return out, err
	}
	timeout := timeoutOf(a.TimeoutMs)

	s.mu.Lock()
	wasLocked := s.lockedNow()
	snap := s.snapshot()
	views := fieldViews(snap)
	targets := make([]fieldView, len(a.Fields))
	for i, it := range a.Fields {
		t, e := resolveField(views, fieldRef{Index: it.Index, Row: it.Row, Col: it.Col, Label: it.Label})
		if e != nil {
			s.mu.Unlock()
			return out, fmt.Errorf("fields[%d]: %w", i, e)
		}
		if m.cfg.ReadOnly {
			if e := checkReadOnly(it.Text); e != nil {
				s.mu.Unlock()
				return out, fmt.Errorf("fields[%d]: %w", i, e)
			}
		}
		targets[i] = t
	}
	for i, it := range a.Fields {
		audit = append(audit, map[string]any{"field": targets[i].Index, "text": auditText(it.Text, targets[i].Nondisplay)})
		s.typeInto(snap, targets[i], it.Text, true, true)
	}
	if then != "" {
		s.eng.Press(then)
	}
	s.mu.Unlock()

	var settled *bool
	if then != "" && postsToHost(then) {
		res, e := m.settleNow(ctx, s, timeout)
		if e != nil {
			return out, e
		}
		ok := res.Settled
		settled = &ok
	}
	if settled, err = m.settleIfLocked(ctx, s, timeout, wasLocked, settled); err != nil {
		return out, err
	}
	return m.finish(s, settled)
}

// ---- wait ------------------------------------------------------------------------------

// WaitArgs is the input of wait.
type WaitArgs struct {
	Session   string `json:"session"`
	Until     string `json:"until" jsonschema:"unlock (keyboard open and screen quiet), text (the text appears), change (the screen changes) or cursor (the cursor reaches row,col)"`
	Text      string `json:"text,omitempty" jsonschema:"substring for until=text, case-insensitive"`
	Rows      string `json:"rows,omitempty" jsonschema:"rows to search for until=text, as in screen (default all)"`
	Row       int    `json:"row,omitempty" jsonschema:"1-based row for until=cursor"`
	Col       int    `json:"col,omitempty" jsonschema:"1-based column for until=cursor"`
	Seq       uint64 `json:"seq,omitempty" jsonschema:"for until=change: wait until seq is above this (default: the seq now)"`
	TimeoutMs int    `json:"timeout_ms,omitempty" jsonschema:"how long to wait (default 15000)"`
}

// WaitOut is the output of wait.
type WaitOut struct {
	Matched bool      `json:"matched" jsonschema:"false when the time ran out or the connection ended"`
	Screen  ScreenOut `json:"screen"`
}

// Wait waits for a condition on a session's screen.
func (m *Manager) Wait(ctx context.Context, a WaitArgs) (WaitOut, error) {
	s, err := m.get(a.Session)
	if err != nil {
		return WaitOut{}, err
	}
	timeout := timeoutOf(a.TimeoutMs)
	var matched bool
	switch strings.ToLower(strings.TrimSpace(a.Until)) {
	case "unlock":
		res, e := settle(ctx, s, settleOpts{Max: timeout})
		if e != nil {
			return WaitOut{}, e
		}
		matched = res.Settled
	case "text":
		if a.Text == "" {
			return WaitOut{}, errors.New("until=text needs text")
		}
		if _, _, e := parseRows(a.Rows, s.snapshot().Rows); e != nil {
			return WaitOut{}, e
		}
		matched, err = waitFor(ctx, s, timeout, func() bool {
			snap := s.snapshot()
			lo, hi, _ := parseRows(a.Rows, snap.Rows)
			return containsFold(snap, a.Text, lo, hi)
		})
	case "change":
		base := a.Seq
		if base == 0 {
			base = s.snapshot().Seq
		}
		matched, err = waitFor(ctx, s, timeout, func() bool { return s.snapshot().Seq > base })
	case "cursor":
		if a.Row < 1 || a.Col < 1 {
			return WaitOut{}, errors.New("until=cursor needs row and col (1-based)")
		}
		matched, err = waitFor(ctx, s, timeout, func() bool {
			c := s.snapshot().Cursor
			return c.Row+1 == a.Row && c.Col+1 == a.Col
		})
	default:
		return WaitOut{}, fmt.Errorf("until %q is not one of unlock, text, change, cursor", a.Until)
	}
	if err != nil {
		return WaitOut{}, err
	}
	scr, err := s.screenOf(ScreenOpts{})
	return WaitOut{Matched: matched, Screen: scr}, err
}

// ---- disconnect ------------------------------------------------------------------------

// DisconnectArgs is the input of disconnect.
type DisconnectArgs struct {
	Session string `json:"session"`
	Signoff bool   `json:"signoff,omitempty" jsonschema:"first type SIGNOFF at a command line, if the screen has one (default false: just close)"`
}

// DisconnectOut is the output of disconnect.
type DisconnectOut struct {
	Closed    bool `json:"closed"`
	SignedOff bool `json:"signed_off,omitempty" jsonschema:"true when SIGNOFF was typed before closing"`
}

// Disconnect ends a session.
func (m *Manager) Disconnect(ctx context.Context, a DisconnectArgs) (out DisconnectOut, err error) {
	if a.Signoff { // typing SIGNOFF is an action; closing the connection is always allowed
		if err := m.audit.check(); err != nil {
			return out, err
		}
	}
	s, err := m.get(a.Session)
	if err != nil {
		return out, err
	}
	before := s.notif.Seq()
	defer func() {
		m.record(s, "disconnect", map[string]any{"signoff": a.Signoff, "signed_off": out.SignedOff}, before, err)
	}()
	if alive, _ := s.isConnected(); alive && a.Signoff {
		out.SignedOff = s.typeSignoff()
		if out.SignedOff {
			_, _ = settle(ctx, s, settleOpts{Max: 3 * time.Second})
		}
	}
	m.mu.Lock()
	delete(m.sessions, s.handle)
	m.mu.Unlock()
	s.close()
	out.Closed = true
	return out, nil
}
