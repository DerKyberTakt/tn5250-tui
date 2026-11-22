package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/DerKyberTakt/tn5250-tui/internal/client"
	"github.com/DerKyberTakt/tn5250-tui/internal/sites"
)

// redrawInterval is the shortest time between two redraws caused by the host.
const redrawInterval = 16 * time.Millisecond

// Link is an open connection as the App uses it (client.Conn implements it).
type Link interface {
	Write(b []byte) error
	WriteTraced(b, forTrace []byte) error
	SetTrace(w io.Writer)
	Close() error
}

// Dialer opens connections; tests use a fake.
type Dialer interface {
	Dial(ctx context.Context, o client.Options) (Link, error)
}

// NetDialer is the real Dialer, backed by the client package.
type NetDialer struct{}

// Dial implements Dialer.
func (NetDialer) Dial(ctx context.Context, o client.Options) (Link, error) {
	c, err := client.Dial(ctx, o)
	if err != nil {
		return nil, err
	}
	return c, nil
}

// EngineFactory builds an engine for one connection. send must be called with
// every frame the engine wants to put on the socket (forTrace is the same
// frame with secrets blanked, or nil); update must be called whenever the
// screen or the OIA changed.
type EngineFactory func(size string, cp int, dev string, send func(b, forTrace []byte), update func()) (Engine, error)

// AppOptions configures New.
type AppOptions struct {
	EngineFactory EngineFactory
	Sites         *sites.File // required (may be empty)
	Dialer        Dialer      // nil = NetDialer
	// Target is a site name or host[:port] to connect to at start; "" shows
	// the site list (or the login form when there are no sites).
	Target string
	// TLS forces TLS for a host[:port] target (a site keeps its own setting).
	TLS bool
	// User and Password are used for the start-up target instead of the
	// site's; both set turns auto sign-on on for that connection.
	User, Password string
	// TraceFile, when set, receives the hex trace of every connection.
	TraceFile string
	// ScreenSize, CodePage and DeviceName apply to host[:port] targets
	// (defaults 27x132, 37, engine's own).
	ScreenSize string
	CodePage   int
	DeviceName string
}

type overlay struct {
	name  string
	focus tview.Primitive
}

// conn is one tab: a connection (or the empty tab before and after one) and everything
// the terminal knows about it. Every field is guarded by App.mu, and so is every engine
// call. The App embeds the active tab, so a.engine, a.connected, ... are the active
// tab's; the other tabs keep their engines fed in the background.
type conn struct {
	engine      Engine
	sess        *session
	connected   bool
	connecting  bool
	cur         sites.Site // current connection target (with overrides)
	curSaved    string     // name of the saved site, "" for ad-hoc
	autoPending bool
	notice      string
	noticeFlash bool // notice is a one-off message; the next key press clears it

	pcAnswered    bool           // the STRPCCMD screen on show was already answered
	pendingSignon *pendingSignon // a typed sign-on waiting for the host's answer
	hintShown     bool           // the code page hint was shown on this connection
	demoLabel     string         // the demo watermark of this tab's site ("" = none)
	tool          *sqlTool       // a tool tab (SQL runner) instead of a 5250 session
}

// App is the terminal application.
type App struct {
	opts   AppOptions
	dialer Dialer

	app     *tview.Application
	pages   *tview.Pages
	flex    *tview.Flex
	tabbar  *TabBar
	panes   *tview.Flex // the screen, or two screens with a divider (split view)
	screen  *Screen     // left pane
	screen2 *Screen     // right pane (split view)
	divider *tview.Box
	local   *localPane // `:!` output under the screen
	oia     *OIABar
	bar     *tview.InputField

	mu          sync.Mutex // guards the fields below, every tab and every engine call
	*conn                  // the active tab
	tabs        []*conn    // every tab, in bar order (at least one)
	other       *conn      // split view: the tab in the pane without the keyboard (nil = not split)
	focusPane   int        // split view: the pane (0 left, 1 right) the active tab is drawn in
	gen         uint64
	sizeOv      string
	cpOv        int
	traceW      io.Writer
	traceCloser io.Closer

	now func() time.Time // time.Now; tests replace it

	overlays   []overlay
	barOpen    bool
	splitShown bool // the second pane is in the layout
	history    []string
	histPos    int

	drawPending atomic.Bool
	stopped     atomic.Bool
	post        func(func()) // runs f on the UI goroutine and redraws
}

// New builds the application. Nothing is shown or connected until Run.
func New(opts AppOptions) *App {
	if opts.Sites == nil {
		opts.Sites = &sites.File{}
	}
	first := &conn{}
	a := &App{opts: opts, dialer: opts.Dialer, now: time.Now, conn: first, tabs: []*conn{first}}
	if a.dialer == nil {
		a.dialer = NetDialer{}
	}
	a.app = tview.NewApplication()
	a.app.SetAfterDrawFunc(a.afterDraw)
	a.post = func(f func()) {
		if a.stopped.Load() {
			return
		}
		a.app.QueueUpdateDraw(f)
	}

	a.screen = NewScreen(a.paneSource(0))
	a.screen2 = NewScreen(a.paneSource(1))
	a.divider = tview.NewBox().SetBackgroundColor(oiaBg)
	a.eachScreen(func(s *Screen) {
		s.Separators = opts.Sites.EffectiveDefaults().Separators
		s.Highlight = opts.Sites.EffectiveDefaults().SourceHighlight
	})
	a.panes = tview.NewFlex().SetDirection(tview.FlexColumn).AddItem(a.screen, 0, 1, true)
	a.local = newLocalPane()
	a.applyDefaults(opts.Sites.EffectiveDefaults())
	a.tabbar = NewTabBar(a.tabInfos)
	a.oia = NewOIABar(a.oiaSource)
	a.bar = tview.NewInputField().SetLabel(":").SetFieldWidth(0)
	a.bar.SetBackgroundColor(oiaBg)
	a.bar.SetFieldBackgroundColor(oiaBg)
	a.bar.SetFieldTextColor(tcell.ColorWhite)
	a.bar.SetLabelColor(oiaAlert)
	a.bar.SetDoneFunc(a.barDone)
	a.bar.SetInputCapture(a.barKey)

	a.flex = tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(a.tabbar, 0, 0, false). // one row once there is more than one tab
		AddItem(a.panes, 0, 1, true).
		AddItem(a.local.view, 0, 0, false). // `:!` output, 8 rows when shown
		AddItem(a.bar, 0, 0, false).
		AddItem(a.oia, 1, 0, false)
	a.pages = tview.NewPages().AddPage("main", a.flex, true, true)
	a.app.SetRoot(a.pages, true).SetFocus(a.screen)
	a.app.SetInputCapture(a.handleKey)
	return a
}

// SetScreen installs a tcell screen (tests use a simulation screen). It must
// be called before Run.
func (a *App) SetScreen(s tcell.Screen) { a.app.SetScreen(s) }

// Run starts the application and blocks until it quits.
func (a *App) Run() error {
	trace := a.opts.TraceFile
	if trace == "" {
		trace = a.opts.Sites.EffectiveDefaults().TraceFile
	}
	if trace != "" {
		if err := a.traceOn(trace); err != nil {
			return err
		}
	}
	a.startup()
	err := a.app.Run()
	a.stopped.Store(true)
	a.disconnectAll()
	a.closeTools()
	a.mu.Lock()
	a.closeTraceLocked()
	a.mu.Unlock()
	return err
}

// startup performs the initial action: connect to the target, else show the
// site list, else the login form.
func (a *App) startup() {
	switch {
	case a.opts.Target != "":
		site, saved, err := a.resolveTarget(a.opts.Target, a.opts.TLS)
		if err != nil {
			a.setNotice(err.Error())
			return
		}
		if a.opts.User != "" {
			site.User = a.opts.User
			site.Password = a.opts.Password
			site.AutoSignon = a.opts.Password != ""
		}
		a.connectTo(site, saved)
	case len(a.opts.Sites.Sites) == 0:
		a.showLogin()
	default:
		a.showSites()
	}
}

// resolveTarget turns a command or flag argument into a Site: a saved site by
// name, else host[:port]. saved is the site's name when it is a saved one.
func (a *App) resolveTarget(target string, tlsFlag bool) (sites.Site, string, error) {
	if s, ok := a.opts.Sites.Find(target); ok {
		if tlsFlag {
			s.TLS = true
		}
		return s, s.Name, nil
	}
	host, port, err := ParseTarget(target)
	if err != nil {
		return sites.Site{}, "", err
	}
	s := sites.Site{
		Host: host, Port: port, TLS: tlsFlag,
		ScreenSize: a.opts.ScreenSize, CodePage: a.opts.CodePage, DeviceName: a.opts.DeviceName,
	}
	return s, "", nil
}

// Quit stops the application.
func (a *App) Quit() {
	a.stopped.Store(true)
	a.disconnectAll()
	a.closeTools()
	a.app.Stop()
}

// ---- shared state helpers --------------------------------------------------

func (a *App) setNotice(msg string) {
	a.mu.Lock()
	a.notice = msg
	a.noticeFlash = false
	a.mu.Unlock()
}

func (a *App) screenSource() (Snapshot, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.engine == nil {
		return Snapshot{}, false
	}
	return a.engine.Snapshot(), true
}

func (a *App) oiaSource() (string, string, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.tool != nil {
		// A tool tab (SQL) has no 5250 session: its line names the tool, not a disconnected
		// screen that asks for Enter.
		return "SQL  " + a.tool.name + "  database host server", "", false
	}
	var o OIA
	row, col := 0, 0
	if a.engine != nil {
		o = a.engine.OIA(a.connected)
		s := a.engine.Snapshot()
		row, col = s.Cursor.Row, s.Cursor.Col
	}
	o.Connected = a.connected
	if a.cur.Demo && a.cur.Host != "" {
		o.DeviceName = strings.TrimSpace(demoLabel(a.cur) + "  " + o.DeviceName)
	}
	notice := a.notice
	if notice == "" && !a.connected && !a.connecting && a.cur.Host != "" {
		notice = "Press Enter to reconnect, or :connect"
	}
	left, right := FormatOIA(o, row, col, notice)
	if a.connecting {
		left = strings.Replace(left, "Disconnected", "Connecting", 1)
	}
	alert := !a.connected || notice != "" || o.Message != "" || (o.InputInhibited != "none" && o.InputInhibited != "")
	return left, right, alert
}

// demoLabel is the word shown for a demo site (DEMO unless the site names another).
func demoLabel(s sites.Site) string {
	if l := strings.TrimSpace(s.DemoLabel); l != "" {
		return l
	}
	return "DEMO"
}

// requestRedraw is the engine's update callback: it coalesces to at most one
// redraw per redrawInterval and never draws from the calling goroutine.
func (a *App) requestRedraw() {
	if !a.drawPending.CompareAndSwap(false, true) {
		return
	}
	time.AfterFunc(redrawInterval, func() {
		a.drawPending.Store(false)
		a.post(a.afterUpdate)
	})
}

// afterUpdate runs on the UI goroutine after an engine changed. Every tab is looked at:
// a sign-on in a background tab goes on while another tab is in front.
func (a *App) afterUpdate() {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, t := range a.tabs {
		t.checkSignonLocked() // before the auto sign-on: its own attempt is judged on a later update
		t.pcCommandLocked()
		t.autoSignonLocked()
	}
}

// flashLocked shows msg on the OIA line until the next key press. The caller holds App.mu.
func (c *conn) flashLocked(msg string) {
	c.notice = msg
	c.noticeFlash = true
}

// clearFlashLocked removes a one-off notice (the user pressed a key). The caller holds App.mu.
func (c *conn) clearFlashLocked() {
	if c.noticeFlash {
		c.notice = ""
		c.noticeFlash = false
	}
}

// pressLocked presses an engine key. Enter first notes what a sign-on attempt holds, so a
// refusal can point at the code page. The caller holds App.mu and has checked c.engine.
func (c *conn) pressLocked(action string) {
	if action == "enter" {
		c.armSignonLocked()
	}
	c.engine.Press(action)
}

// ---- connections -----------------------------------------------------------

// session is one connection attempt/connection; chunks that arrive before the
// dial returned are queued and replayed in order once the link is installed.
type session struct {
	app    *App
	tab    *conn // the tab this connection belongs to
	gen    uint64
	cancel context.CancelFunc

	mu      sync.Mutex // guards ready, pending and feeding order
	ready   bool
	pending [][]byte

	failure atomic.Value // string: why the engine refused the host's data, when it did

	dead bool // the socket ended (guarded by App.mu)

	// Workstation name retries. template is the name as configured (variables expanded),
	// attempt the number of this try (0 = the first), deviceName the name requested now.
	template   string
	attempt    int
	deviceName string
	// connectedAt is when the dial returned (guarded by App.mu; zero until then).
	connectedAt time.Time
	// screens counts the engine updates the host caused; 0 means it never sent a screen.
	screens atomic.Int64

	linkMu sync.Mutex
	link   Link
}

func (s *session) getLink() Link {
	s.linkMu.Lock()
	defer s.linkMu.Unlock()
	return s.link
}

// send is the engine's Send callback.
func (s *session) send(b, forTrace []byte) {
	if l := s.getLink(); l != nil {
		_ = l.WriteTraced(b, forTrace)
	}
}

func (s *session) onData(b []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.ready {
		s.pending = append(s.pending, append([]byte(nil), b...))
		return
	}
	s.app.feed(s, b)
}

func (s *session) onClosed(err error) {
	if why, _ := s.failure.Load().(string); why != "" {
		err = errors.New(why)
	}
	s.app.dropped(s, err)
}

func (s *session) setLink(l Link) bool {
	a, t := s.app, s.tab
	a.mu.Lock()
	if t.sess != s {
		a.mu.Unlock()
		return false
	}
	if !s.dead { // the host may have closed before we got here
		t.connected = true
		t.connecting = false
		t.notice = ""
	}
	a.mu.Unlock()
	s.linkMu.Lock()
	s.link = l
	s.linkMu.Unlock()
	s.mu.Lock()
	for _, b := range s.pending {
		a.feed(s, b)
	}
	s.pending = nil
	s.ready = true
	s.mu.Unlock()
	a.requestRedraw()
	return true
}

// feed hands socket bytes to the session's engine (under the App mutex).
func (a *App) feed(s *session, b []byte) {
	a.mu.Lock()
	t := s.tab
	if t.sess != s || t.engine == nil {
		a.mu.Unlock()
		return
	}
	t.engine.Feed(b)
	var failed error
	if f, ok := t.engine.(interface{ Failed() error }); ok {
		failed = f.Failed()
	}
	a.mu.Unlock()
	if failed != nil {
		// The host broke a framing limit (a record that never ends, say): close the link;
		// dropped then shows why.
		s.failure.Store(failed.Error())
		if l := s.getLink(); l != nil {
			_ = l.Close()
		}
	}
}

// dropped is called when the socket ends (host closed it, or an error).
func (a *App) dropped(s *session, err error) {
	a.mu.Lock()
	t := s.tab
	if t.sess != s {
		a.mu.Unlock()
		return
	}
	s.dead = true
	t.connected = false
	t.connecting = false
	t.autoPending = false
	// Closed by the host right after the connect and before any screen: the workstation name
	// is most likely in use (by an ACS session, say). Try the next name.
	early := s.deviceName != "" && s.screens.Load() == 0 &&
		(s.connectedAt.IsZero() || a.now().Sub(s.connectedAt) < earlyDropWindow)
	retry := early && s.attempt < maxDeviceRetries && nextDeviceName(s.template, s.attempt+1) != ""
	cur, saved := t.cur, t.curSaved
	msg := "Connection closed. Press Enter to reconnect, or :connect"
	switch {
	case early:
		msg = fmt.Sprintf("Connection closed by the host (workstation %s in use?). Press Enter to reconnect, or :connect", s.deviceName)
	case err != nil:
		msg = "Connection lost: " + err.Error() + ". Press Enter to reconnect"
	}
	t.notice = msg
	t.noticeFlash = false
	a.mu.Unlock()
	s.linkMu.Lock()
	s.link = nil
	s.linkMu.Unlock()
	if retry {
		a.dial(t, cur, saved, s.attempt+1, s.deviceName)
		return
	}
	a.requestRedraw()
}

// earlyDropWindow is how soon after the connect a close by the host, with no screen sent,
// counts as "workstation name in use".
const earlyDropWindow = 8 * time.Second

// connectTo connects to site: in the active tab when it holds no connection, else in a
// new tab (the open sessions stay). saved is the saved site's name ("" for an ad-hoc host).
func (a *App) connectTo(site sites.Site, saved string) {
	a.dial(a.tabForConnect(), site, saved, 0, "")
}

// tabForConnect is the tab a new connection goes to: the active one when it is idle, else
// a new tab. It runs on the UI goroutine.
func (a *App) tabForConnect() *conn {
	a.mu.Lock()
	idle := !a.connected && !a.connecting && a.tool == nil
	a.mu.Unlock()
	if idle {
		return a.conn
	}
	return a.newTab()
}

// dial connects tab t to site, for connection attempt number attempt. The site's device
// name is a template (see nextDeviceName); attempt 0 is the user's own connect (on the UI
// goroutine), later ones retry after the host refused the name.
func (a *App) dial(t *conn, site sites.Site, saved string, attempt int, refused string) {
	site = a.opts.Sites.Complete(site)
	a.mu.Lock()
	if a.sizeOv != "" {
		site.ScreenSize = a.sizeOv
	}
	if a.cpOv != 0 {
		site.CodePage = a.cpOv
	}
	a.mu.Unlock()
	a.disconnectTab(t)
	if attempt == 0 {
		label := ""
		if site.Demo {
			label = demoLabel(site)
		}
		a.mu.Lock()
		t.demoLabel = label
		active := t == a.conn
		a.mu.Unlock()
		if active {
			a.syncScreens()
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	s := &session{app: a, tab: t, cancel: cancel, attempt: attempt}
	if tpl := expandDeviceVars(strings.TrimSpace(site.DeviceName)); tpl != "" {
		s.template = tpl
		s.deviceName = nextDeviceName(tpl, attempt)
	}
	if a.opts.EngineFactory == nil {
		cancel()
		a.setNotice("no engine configured")
		return
	}
	update := func() {
		s.screens.Add(1)
		a.requestRedraw()
	}
	eng, err := a.opts.EngineFactory(site.ScreenSize, site.CodePage, s.deviceName, s.send, update)
	if err != nil {
		cancel()
		a.setNotice("engine: " + err.Error())
		return
	}
	a.mu.Lock()
	a.gen++
	s.gen = a.gen
	t.engine = eng
	t.sess = s
	t.cur = site
	t.curSaved = saved
	t.connected = false
	t.connecting = true
	t.autoPending = site.AutoSignon && site.User != "" && site.Password != ""
	t.pcAnswered, t.pendingSignon, t.hintShown = false, nil, false
	t.noticeFlash = false
	t.notice = fmt.Sprintf("Connecting to %s:%d ...", site.Host, site.Port)
	if refused != "" {
		t.notice = fmt.Sprintf("Workstation %s refused by the host, trying %s ...", refused, s.deviceName)
	}
	trace := a.traceW
	a.mu.Unlock()
	keepEvery := time.Duration(a.opts.Sites.EffectiveDefaults().KeepAliveSeconds) * time.Second

	if saved != "" && attempt == 0 {
		a.opts.Sites.LastUsed = saved
		_ = a.opts.Sites.Save()
	}

	go func() {
		link, err := a.dialer.Dial(ctx, client.Options{
			Host: site.Host, Port: site.Port, TLS: site.TLS, TrustAnyCertificate: site.TrustAnyCertificate,
			TimeoutMs: 15000, KeepAlive: true, KeepAliveEvery: keepEvery, Trace: trace,
			OnData: s.onData, OnClosed: s.onClosed,
		})
		if err != nil {
			a.mu.Lock()
			if t.sess == s {
				t.connecting = false
				t.autoPending = false
				t.notice = "Connect failed: " + err.Error() + "  (Enter or :connect to retry)"
			}
			a.mu.Unlock()
			a.requestRedraw()
			return
		}
		a.mu.Lock()
		s.connectedAt = a.now()
		a.mu.Unlock()
		if !s.setLink(link) {
			_ = link.Close()
		}
	}()
}

// Disconnect closes the active tab's connection (if any) and resets its engine.
func (a *App) Disconnect() {
	a.mu.Lock()
	t := a.conn
	a.mu.Unlock()
	a.disconnectTab(t)
}

// disconnectTab closes tab t's connection (if any) and resets its engine; the tab stays.
func (a *App) disconnectTab(t *conn) {
	a.mu.Lock()
	s := t.sess
	eng := t.engine
	t.sess = nil
	t.engine = nil
	t.connected = false
	t.connecting = false
	t.autoPending = false
	a.mu.Unlock()
	if s == nil {
		return
	}
	s.cancel()
	if l := s.getLink(); l != nil {
		_ = l.Close()
	}
	if eng != nil {
		a.mu.Lock()
		eng.Reset()
		a.mu.Unlock()
	}
}

// disconnectAll closes every tab's connection (quit).
func (a *App) disconnectAll() {
	a.mu.Lock()
	tabs := append([]*conn(nil), a.tabs...)
	a.mu.Unlock()
	for _, t := range tabs {
		a.disconnectTab(t)
	}
}

// reconnect connects again to the active tab's target.
func (a *App) reconnect() {
	a.mu.Lock()
	cur, saved := a.cur, a.curSaved
	a.mu.Unlock()
	if cur.Host == "" {
		a.showLogin()
		return
	}
	a.connectTo(cur, saved)
}

// ---- trace -----------------------------------------------------------------

func (a *App) traceOn(file string) error {
	f, err := os.OpenFile(file, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.closeTraceLocked()
	a.traceW, a.traceCloser = f, f
	a.setLinkTraceLocked(f)
	return nil
}

func (a *App) traceOff() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.setLinkTraceLocked(nil)
	a.closeTraceLocked()
}

// setLinkTraceLocked points every open connection's trace at w. The caller holds a.mu.
func (a *App) setLinkTraceLocked(w io.Writer) {
	for _, t := range a.tabs {
		if t.sess != nil {
			if l := t.sess.getLink(); l != nil {
				l.SetTrace(w)
			}
		}
	}
}

func (a *App) closeTraceLocked() {
	if a.traceCloser != nil {
		_ = a.traceCloser.Close()
	}
	a.traceW, a.traceCloser = nil, nil
}

// ---- sign-on ---------------------------------------------------------------

var errNoSignon = errors.New("sign-on refused: the screen has no user and password fields")

// typedSignon fills the first two input fields with user and password and
// presses Enter. It refuses unless the second input field is non-display.
// The caller holds a.mu.
func typedSignon(e Engine, snap Snapshot, user, password string) error {
	if err := fillSignon(e, snap, user, password); err != nil {
		return err
	}
	e.Press("enter")
	return nil
}

// fillSignon types user and password into the first two input fields (no Enter).
func fillSignon(e Engine, snap Snapshot, user, password string) error {
	in := snap.InputFields()
	if len(in) < 2 || !in[1].Nondisplay {
		return errNoSignon
	}
	if snap.Cols <= 0 {
		return errNoSignon
	}
	for i, text := range []string{user, password} {
		e.SetCursor(in[i].Start/snap.Cols, in[i].Start%snap.Cols)
		e.Press("eraseEof")
		e.Type(text)
	}
	return nil
}

// signonLocked is typedSignon through the tab: Enter goes through pressLocked, so the code
// page hint watches the attempt. The caller holds App.mu.
func (c *conn) signonLocked(snap Snapshot, user, password string) error {
	if err := fillSignon(c.engine, snap, user, password); err != nil {
		return err
	}
	c.pressLocked("enter")
	return nil
}

// autoSignonLocked runs the typed sign-on once, on the first unlocked screen
// with content after connecting. The caller holds App.mu.
func (c *conn) autoSignonLocked() {
	if !c.autoPending || !c.connected || c.engine == nil {
		return
	}
	if o := c.engine.OIA(true); o.InputInhibited != "none" && o.InputInhibited != "" {
		return
	}
	snap := c.engine.Snapshot()
	if strings.TrimSpace(snap.Text()) == "" {
		return
	}
	c.autoPending = false // the first screen decides, once
	_ = c.signonLocked(snap, c.cur.User, c.cur.Password)
}

// signon is the `signon` command (active tab).
func (a *App) signon() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.engine == nil || !a.connected {
		return errors.New("not connected")
	}
	if a.cur.User == "" || a.cur.Password == "" {
		return errors.New("no user and password for this connection (save them in the site, or use --user/--password)")
	}
	return a.signonLocked(a.engine.Snapshot(), a.cur.User, a.cur.Password)
}

// ---- code page hint after a refused sign-on --------------------------------

// signonAnswerScreens is how many locked updates the host may take to answer a sign-on
// attempt before the hint gives up.
const signonAnswerScreens = 3

// pendingSignon is a sign-on attempt (Enter on a sign-on screen) whose password held
// characters that differ between code pages. Only those characters are kept, never the
// password itself.
type pendingSignon struct {
	chars   []string
	screens int
}

// armSignonLocked runs when Enter is pressed: if the screen is a sign-on screen and its
// password field holds variant characters, remember them so a refusal can name them.
// The caller holds App.mu.
func (c *conn) armSignonLocked() {
	c.pendingSignon = nil
	p, ok := c.engine.(SignonProber)
	if !ok {
		return
	}
	probe := p.SignonProbe()
	if !probe.Screen || len(probe.VariantChars) == 0 {
		return
	}
	c.pendingSignon = &pendingSignon{chars: probe.VariantChars}
}

// checkSignonLocked judges a pending sign-on attempt: when the host unlocked the keyboard
// and shows a sign-on screen again, the attempt failed, and the notice names the variant
// characters and the code page in use. One notice per connection. The caller holds App.mu.
func (c *conn) checkSignonLocked() {
	ps := c.pendingSignon
	if ps == nil || c.engine == nil {
		return
	}
	if o := c.engine.OIA(true); o.InputInhibited != "none" && o.InputInhibited != "" {
		if ps.screens++; ps.screens > signonAnswerScreens {
			c.pendingSignon = nil
		}
		return
	}
	c.pendingSignon = nil
	p, ok := c.engine.(SignonProber)
	if !ok {
		return
	}
	probe := p.SignonProbe()
	if !probe.Screen || c.hintShown {
		return
	}
	c.hintShown = true
	c.flashLocked(fmt.Sprintf("Sign-on failed; the password has characters that differ between code pages (%s). Code page in use: %d; if the user and password are right, set :cp to the host's CCSID and reconnect",
		strings.Join(ps.chars, " "), probe.CodePage))
}

// ---- STRPCCMD --------------------------------------------------------------

// pcCommandLocked answers a STRPCCMD (PC Organizer) screen with Enter at once so the host's
// program resumes, and says on the OIA line that the command was not run: this terminal never
// runs a command the host sends. The caller holds App.mu.
func (c *conn) pcCommandLocked() {
	if c.engine == nil || !c.connected {
		return
	}
	src, ok := c.engine.(PcCommandSource)
	if !ok {
		return
	}
	cmd, found := src.PcCommand()
	if !found {
		c.pcAnswered = false
		return
	}
	if c.pcAnswered {
		return
	}
	c.pcAnswered = true
	c.flashLocked("PC command not run here: " + cmd.Command)
	c.engine.Press("enter")
}
