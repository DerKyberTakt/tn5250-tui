package tui

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/DerKyberTakt/tn5250-tui/internal/sites"
)

// settingsForm returns the settings form on top of the overlays.
func settingsForm(t *testing.T, a *App) *tview.Form {
	t.Helper()
	if a.topOverlay() != "settings" {
		t.Fatalf("top overlay %q, want settings", a.topOverlay())
	}
	f, ok := a.overlays[len(a.overlays)-1].focus.(*tview.Form)
	if !ok {
		t.Fatalf("settings overlay is not a form")
	}
	return f
}

func setText(f *tview.Form, label, text string) {
	f.GetFormItemByLabel(label).(*tview.InputField).SetText(text)
}

func TestSettingsCommandOpensForm(t *testing.T) {
	r := newRig(t, nil)
	r.a.RunCommand("settings")
	f := settingsForm(t, r.a)
	for _, l := range []string{"Screen size", "Code page", "Device name template", "Column separators",
		"SEU syntax colouring", "Auto sign-on by default", "Trace file", "Keep-alive seconds"} {
		if f.GetFormItemByLabel(l) == nil {
			t.Errorf("missing field %q", l)
		}
	}
	// no block in the file: the built-in values show
	in := readSettingsForm(f)
	if in.ScreenSize != "27x132" || in.CodePage != "37" || !in.SourceHighlight || in.Separators || in.AutoSignon || in.KeepAlive != "" {
		t.Fatalf("form %+v", in)
	}
	if f.GetButtonIndex("Save") < 0 || f.GetButtonIndex("Cancel") < 0 {
		t.Fatal("buttons missing")
	}
	// a second :settings does not stack another form
	r.a.RunCommand("settings")
	if len(r.a.overlays) != 1 {
		t.Fatalf("overlays %d", len(r.a.overlays))
	}
}

func TestSettingsSavePersistsAndApplies(t *testing.T) {
	r := newRig(t, nil)
	r.a.RunCommand("size 24x80") // typed overrides give way to the saved settings
	r.a.RunCommand("settings")
	f := settingsForm(t, r.a)
	f.GetFormItemByLabel("Screen size").(*tview.DropDown).SetCurrentOption(1) // 24x80
	setText(f, "Code page", "273")
	setText(f, "Device name template", "KWTUI")
	f.GetFormItemByLabel("Column separators").(*tview.Checkbox).SetChecked(true)
	f.GetFormItemByLabel("SEU syntax colouring").(*tview.Checkbox).SetChecked(false)
	f.GetFormItemByLabel("Auto sign-on by default").(*tview.Checkbox).SetChecked(true)
	setText(f, "Trace file", "t.log")
	setText(f, "Keep-alive seconds", "45")
	pressButton(f, "Save")

	if r.a.topOverlay() != "" {
		t.Fatalf("form still open: %q", r.a.topOverlay())
	}
	if got := r.noticeText(); got != "Settings saved" {
		t.Fatalf("notice %q", got)
	}
	if !r.a.screen.Separators {
		t.Error("separators not applied live")
	}
	want := sites.Defaults{ScreenSize: "24x80", CodePage: 273, DeviceName: "KWTUI", Separators: true,
		SourceHighlight: false, AutoSignon: true, TraceFile: "t.log", KeepAliveSeconds: 45}
	g, err := sites.Load() // the rig points TN5250_TUI_CONFIG at a temp file
	if err != nil || g.Defaults == nil || !reflect.DeepEqual(*g.Defaults, want) {
		t.Fatalf("file defaults %+v err %v; want %+v", g.Defaults, err, want)
	}
	if b, _ := os.ReadFile(sites.Path()); !strings.Contains(string(b), `"defaults"`) {
		t.Fatalf("file: %s", b)
	}
}

func TestSavedDefaultsDriveNextConnect(t *testing.T) {
	r := newRig(t, nil)
	r.connect(t, "pub400.com")
	r.mu.Lock()
	if r.args[0] != "27x132/37/" {
		t.Errorf("first connect %q", r.args[0])
	}
	r.mu.Unlock()

	r.a.RunCommand("size 27x132") // an old override must not win over the saved settings
	r.a.RunCommand("settings")
	f := settingsForm(t, r.a)
	f.GetFormItemByLabel("Screen size").(*tview.DropDown).SetCurrentOption(1)
	setText(f, "Code page", "273")
	setText(f, "Device name template", "KWTUI")
	setText(f, "Keep-alive seconds", "30")
	pressButton(f, "Save")

	r.a.RunCommand("connect pub400.com")
	waitFor(t, "second engine", func() bool { r.mu.Lock(); defer r.mu.Unlock(); return len(r.engines) == 2 })
	r.mu.Lock()
	got := r.args[1]
	r.mu.Unlock()
	if got != "24x80/273/KWTUI" {
		t.Errorf("second connect %q", got)
	}
	waitFor(t, "second dial", func() bool { return r.d.count() == 2 })
	if o, _ := r.d.last(); o.KeepAliveEvery.Seconds() != 30 {
		t.Errorf("keep-alive %v", o.KeepAliveEvery)
	}
	// a saved site's own values still win
	r.a.opts.Sites.Upsert(sites.Site{Name: "wide", Host: "PUB400.COM", ScreenSize: "27x132", CodePage: 37})
	r.a.RunCommand("connect wide")
	waitFor(t, "third engine", func() bool { r.mu.Lock(); defer r.mu.Unlock(); return len(r.engines) == 3 })
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.args[2] != "27x132/37/KWTUI" {
		t.Errorf("site connect %q", r.args[2])
	}
}

func TestSettingsCancelChangesNothing(t *testing.T) {
	for _, how := range []string{"button", "esc"} {
		r := newRig(t, nil)
		r.a.RunCommand("settings")
		f := settingsForm(t, r.a)
		f.GetFormItemByLabel("Screen size").(*tview.DropDown).SetCurrentOption(1)
		setText(f, "Code page", "273")
		f.GetFormItemByLabel("Column separators").(*tview.Checkbox).SetChecked(true)
		if how == "button" {
			pressButton(f, "Cancel")
		} else {
			f.InputHandler()(tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone), func(tview.Primitive) {})
		}
		if r.a.topOverlay() != "" {
			t.Fatalf("%s: form still open", how)
		}
		if r.a.opts.Sites.Defaults != nil || r.a.screen.Separators {
			t.Errorf("%s: state changed: %+v", how, r.a.opts.Sites.Defaults)
		}
		if _, err := os.Stat(sites.Path()); err == nil {
			t.Errorf("%s: file written", how)
		}
		r.connect(t, "pub400.com")
		if r.args[0] != "27x132/37/" {
			t.Errorf("%s: connect %q", how, r.args[0])
		}
	}
}

func TestSettingsValidationShownInForm(t *testing.T) {
	r := newRig(t, nil)
	r.a.RunCommand("settings")
	f := settingsForm(t, r.a)
	setText(f, "Code page", "abc")
	pressButton(f, "Save")
	if r.a.topOverlay() != "settings" || !strings.Contains(f.GetTitle(), "Code page") {
		t.Fatalf("overlay %q title %q", r.a.topOverlay(), f.GetTitle())
	}
	setText(f, "Code page", "37")
	setText(f, "Keep-alive seconds", "-3")
	pressButton(f, "Save")
	if !strings.Contains(f.GetTitle(), "Keep-alive") || r.a.opts.Sites.Defaults != nil {
		t.Fatalf("title %q defaults %+v", f.GetTitle(), r.a.opts.Sites.Defaults)
	}
	if _, err := os.Stat(sites.Path()); err == nil {
		t.Fatal("file written after a validation error")
	}
	if _, err := (settingsInput{ScreenSize: "80x24"}).toDefaults(); err == nil {
		t.Error("an unknown screen size must fail")
	}
}

func TestSettingsReachableWithoutSession(t *testing.T) {
	// no sites: the login form has a Settings button; Cancel returns to the login form
	r := newRig(t, nil)
	r.a.startup()
	if r.a.topOverlay() != "login" {
		t.Fatalf("overlay %q", r.a.topOverlay())
	}
	login := r.a.overlays[0].focus.(*tview.Form)
	pressButton(login, "Settings")
	pressButton(settingsForm(t, r.a), "Cancel")
	if r.a.topOverlay() != "login" {
		t.Fatalf("after cancel: %q", r.a.topOverlay())
	}

	// sites: the site list opens it with s
	r2 := newRig(t, func(o *AppOptions) { o.Sites = siteWithLogin(false) })
	r2.a.startup()
	if r2.a.topOverlay() != "sites" {
		t.Fatalf("overlay %q", r2.a.topOverlay())
	}
	// the manager's capture lives on its root flex; reach it through the page
	name, item := r2.a.pages.GetFrontPage()
	if name != "sites" {
		t.Fatalf("front page %q", name)
	}
	root := findCapture(item)
	if root == nil {
		t.Fatal("no input capture in the site manager")
	}
	if root(runeKey('s')) != nil {
		t.Error("s should be consumed")
	}
	pressButton(settingsForm(t, r2.a), "Save")
	if r2.a.topOverlay() != "sites" {
		t.Fatalf("after save: %q", r2.a.topOverlay())
	}
	if r2.a.opts.Sites.Defaults == nil || len(r2.a.opts.Sites.Sites) != 1 {
		t.Fatalf("file %+v", r2.a.opts.Sites)
	}
}

// findCapture returns the first input capture found in p or its flex children.
func findCapture(p tview.Primitive) func(*tcell.EventKey) *tcell.EventKey {
	type capturer interface {
		GetInputCapture() func(*tcell.EventKey) *tcell.EventKey
	}
	if c, ok := p.(capturer); ok {
		if h := c.GetInputCapture(); h != nil {
			return h
		}
	}
	if fl, ok := p.(*tview.Flex); ok {
		for i := 0; i < fl.GetItemCount(); i++ {
			if h := findCapture(fl.GetItem(i)); h != nil {
				return h
			}
		}
	}
	return nil
}

func TestSettingsInCommandHelp(t *testing.T) {
	found := false
	for _, c := range CommandHelp() {
		if c[0] == "settings" {
			found = true
		}
	}
	if !found {
		t.Fatal("settings missing from CommandHelp")
	}
	if c, err := ParseCommand(":settings"); err != nil || c.Name != "settings" {
		t.Fatalf("%+v %v", c, err)
	}
}

func TestNewSiteFormStartsFromDefaults(t *testing.T) {
	r := newRig(t, func(o *AppOptions) {
		o.Sites = &sites.File{Defaults: &sites.Defaults{ScreenSize: "24x80", CodePage: 273, DeviceName: "KW", AutoSignon: true}}
	})
	r.a.showSites()
	m := &siteManager{a: r.a, list: tview.NewList(), hint: tview.NewTextView()}
	r.a.showSiteForm(m, sites.Site{}, "")
	f := r.a.overlays[len(r.a.overlays)-1].focus.(*tview.Form)
	in := readSiteForm(f)
	if in.ScreenSize != "24x80" || in.CodePage != "273" || in.DeviceName != "KW" || !in.Auto {
		t.Fatalf("%+v", in)
	}
}
