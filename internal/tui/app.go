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
	// (defaults 24x80, 37, engine's own).
	ScreenSize string
	CodePage   int
	DeviceName string
}

type overlay struct {
	name  string
	focus tview.Primitive
}

// App is the terminal application.
type App struct {
	opts   AppOptions
	dialer Dialer

	app    *tview.Application
	pages  *tview.Pages
	flex   *tview.Flex
	screen *Screen
	oia    *OIABar
	bar    *tview.InputField

	mu          sync.Mutex // guards the fields below and every engine call
	engine      Engine
	sess        *session
	gen         uint64
	connected   bool
	connecting  bool
	cur         sites.Site // current connection target (with overrides)
	curSaved    string     // name of the saved site, "" for ad-hoc
	autoPending bool
	notice      string
	sizeOv      string
	cpOv        int
	traceW      io.Writer
	traceCloser io.Closer

	overlays []overlay
	barOpen  bool
	history  []string
	histPos  int

	drawPending atomic.Bool
	stopped     atomic.Bool
	post        func(func()) // runs f on the UI goroutine and redraws
}

// New builds the application. Nothing is shown or connected until Run.
func New(opts AppOptions) *App {
	if opts.Sites == nil {
		opts.Sites = &sites.File{}
	}
	a := &App{opts: opts, dialer: opts.Dialer}
	if a.dialer == nil {
		a.dialer = NetDialer{}
	}
	a.app = tview.NewApplication()
	a.post = func(f func()) {
		if a.stopped.Load() {
			return
		}
		a.app.QueueUpdateDraw(f)
	}

	a.screen = NewScreen(a.screenSource)
	a.oia = NewOIABar(a.oiaSource)
	a.bar = tview.NewInputField().SetLabel(":").SetFieldWidth(0)
	a.bar.SetBackgroundColor(oiaBg)
	a.bar.SetFieldBackgroundColor(oiaBg)
	a.bar.SetFieldTextColor(tcell.ColorWhite)
	a.bar.SetLabelColor(oiaAlert)
	a.bar.SetDoneFunc(a.barDone)
	a.bar.SetInputCapture(a.barKey)

	a.flex = tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(a.screen, 0, 1, true).
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
	if a.opts.TraceFile != "" {
		if err := a.traceOn(a.opts.TraceFile); err != nil {
			return err
		}
	}
	a.startup()
	err := a.app.Run()
	a.stopped.Store(true)
	a.Disconnect()
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
	a.Disconnect()
	a.app.Stop()
}

// ---- shared state helpers --------------------------------------------------

func (a *App) setNotice(msg string) {
	a.mu.Lock()
	a.notice = msg
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
	var o OIA
	row, col := 0, 0
	if a.engine != nil {
		o = a.engine.OIA(a.connected)
		s := a.engine.Snapshot()
		row, col = s.Cursor.Row, s.Cursor.Col
	}
	o.Connected = a.connected
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

// afterUpdate runs on the UI goroutine after the engine changed.
func (a *App) afterUpdate() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.autoSignonLocked()
}

// ---- connections -----------------------------------------------------------

// session is one connection attempt/connection; chunks that arrive before the
// dial returned are queued and replayed in order once the link is installed.
type session struct {
	app    *App
	gen    uint64
	cancel context.CancelFunc

	mu      sync.Mutex // guards ready, pending and feeding order
	ready   bool
	pending [][]byte

	dead bool // the socket ended (guarded by App.mu)

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
	s.app.dropped(s, err)
}

func (s *session) setLink(l Link) bool {
	a := s.app
	a.mu.Lock()
	if a.sess != s {
		a.mu.Unlock()
		return false
	}
	if !s.dead { // the host may have closed before we got here
		a.connected = true
		a.connecting = false
		a.notice = ""
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

// feed hands socket bytes to the engine (under the App mutex).
func (a *App) feed(s *session, b []byte) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.sess != s || a.engine == nil {
		return
	}
	a.engine.Feed(b)
}

// dropped is called when the socket ends (host closed it, or an error).
func (a *App) dropped(s *session, err error) {
	a.mu.Lock()
	if a.sess != s {
		a.mu.Unlock()
		return
	}
	s.dead = true
	a.connected = false
	a.connecting = false
	a.autoPending = false
	msg := "Connection closed. Press Enter to reconnect, or :connect"
	if err != nil {
		msg = "Connection lost: " + err.Error() + ". Press Enter to reconnect"
	}
	a.notice = msg
	a.mu.Unlock()
	s.linkMu.Lock()
	s.link = nil
	s.linkMu.Unlock()
	a.requestRedraw()
}

// connectTo drops any current connection and connects to site. saved is the
// saved site's name ("" for an ad-hoc host).
func (a *App) connectTo(site sites.Site, saved string) {
	site = site.WithDefaults()
	a.mu.Lock()
	if a.sizeOv != "" {
		site.ScreenSize = a.sizeOv
	}
	if a.cpOv != 0 {
		site.CodePage = a.cpOv
	}
	a.mu.Unlock()
	a.Disconnect()

	ctx, cancel := context.WithCancel(context.Background())
	s := &session{app: a, cancel: cancel}
	if a.opts.EngineFactory == nil {
		cancel()
		a.setNotice("no engine configured")
		return
	}
	eng, err := a.opts.EngineFactory(site.ScreenSize, site.CodePage, site.DeviceName, s.send, a.requestRedraw)
	if err != nil {
		cancel()
		a.setNotice("engine: " + err.Error())
		return
	}
	a.mu.Lock()
	a.gen++
	s.gen = a.gen
	a.engine = eng
	a.sess = s
	a.cur = site
	a.curSaved = saved
	a.connected = false
	a.connecting = true
	a.autoPending = site.AutoSignon && site.User != "" && site.Password != ""
	a.notice = fmt.Sprintf("Connecting to %s:%d ...", site.Host, site.Port)
	trace := a.traceW
	a.mu.Unlock()

	if saved != "" {
		a.opts.Sites.LastUsed = saved
		_ = a.opts.Sites.Save()
	}

	go func() {
		link, err := a.dialer.Dial(ctx, client.Options{
			Host: site.Host, Port: site.Port, TLS: site.TLS, TrustAnyCertificate: site.TrustAnyCertificate,
			TimeoutMs: 15000, KeepAlive: true, Trace: trace,
			OnData: s.onData, OnClosed: s.onClosed,
		})
		if err != nil {
			a.mu.Lock()
			if a.sess == s {
				a.connecting = false
				a.autoPending = false
				a.notice = "Connect failed: " + err.Error() + "  (Enter or :connect to retry)"
			}
			a.mu.Unlock()
			a.requestRedraw()
			return
		}
		if !s.setLink(link) {
			_ = link.Close()
		}
	}()
}

// Disconnect closes the connection (if any) and resets the engine.
func (a *App) Disconnect() {
	a.mu.Lock()
	s := a.sess
	eng := a.engine
	a.sess = nil
	a.engine = nil
	a.connected = false
	a.connecting = false
	a.autoPending = false
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

// reconnect connects again to the current target.
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
	if a.sess != nil {
		if l := a.sess.getLink(); l != nil {
			l.SetTrace(f)
		}
	}
	return nil
}

func (a *App) traceOff() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.sess != nil {
		if l := a.sess.getLink(); l != nil {
			l.SetTrace(nil)
		}
	}
	a.closeTraceLocked()
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
	e.Press("enter")
	return nil
}

// autoSignonLocked runs the typed sign-on once, on the first unlocked screen
// with content after connecting. The caller holds a.mu.
func (a *App) autoSignonLocked() {
	if !a.autoPending || !a.connected || a.engine == nil {
		return
	}
	if o := a.engine.OIA(true); o.InputInhibited != "none" && o.InputInhibited != "" {
		return
	}
	snap := a.engine.Snapshot()
	if strings.TrimSpace(snap.Text()) == "" {
		return
	}
	a.autoPending = false // the first screen decides, once
	_ = typedSignon(a.engine, snap, a.cur.User, a.cur.Password)
}

// signon is the `signon` command.
func (a *App) signon() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.engine == nil || !a.connected {
		return errors.New("not connected")
	}
	if a.cur.User == "" || a.cur.Password == "" {
		return errors.New("no user and password for this connection (save them in the site, or use --user/--password)")
	}
	return typedSignon(a.engine, a.engine.Snapshot(), a.cur.User, a.cur.Password)
}
