package tui

import (
	"errors"
	"strconv"
	"strings"

	"github.com/rivo/tview"

	"github.com/DerKyberTakt/tn5250-tui/internal/sites"
	"github.com/DerKyberTakt/tn5250-tui/internal/vscode"
)

// settingsInput is the raw text of the settings form.
type settingsInput struct {
	ScreenSize, CodePage, DeviceName, TraceFile, KeepAlive string
	Separators, SourceHighlight, AutoSignon                bool
}

// toDefaults validates the settings form.
func (in settingsInput) toDefaults() (sites.Defaults, error) {
	d := sites.Defaults{
		ScreenSize:      in.ScreenSize,
		DeviceName:      strings.TrimSpace(in.DeviceName),
		Separators:      in.Separators,
		SourceHighlight: in.SourceHighlight,
		AutoSignon:      in.AutoSignon,
		TraceFile:       strings.TrimSpace(in.TraceFile),
	}
	if d.ScreenSize != "24x80" && d.ScreenSize != "27x132" {
		return d, errors.New("Screen size must be 24x80 or 27x132")
	}
	if c := strings.TrimSpace(in.CodePage); c != "" {
		n, err := strconv.Atoi(c)
		if err != nil || n < 1 {
			return d, errors.New("Code page must be a CCSID number")
		}
		d.CodePage = n
	}
	if k := strings.TrimSpace(in.KeepAlive); k != "" {
		n, err := strconv.Atoi(k)
		if err != nil || n < 0 {
			return d, errors.New("Keep-alive must be a number of seconds (0 = 15)")
		}
		d.KeepAliveSeconds = n
	}
	return d, nil
}

func readSettingsForm(f *tview.Form) settingsInput {
	in := settingsInput{
		CodePage: formText(f, "Code page"), DeviceName: formText(f, "Device name template"),
		TraceFile: formText(f, "Trace file"), KeepAlive: formText(f, "Keep-alive seconds"),
		Separators:      formChecked(f, "Column separators"),
		SourceHighlight: formChecked(f, "SEU syntax colouring"),
		AutoSignon:      formChecked(f, "Auto sign-on by default"),
	}
	if dd, ok := f.GetFormItemByLabel("Screen size").(*tview.DropDown); ok {
		_, in.ScreenSize = dd.GetCurrentOption()
	}
	return in
}

// newSettingsForm builds the settings form, filled from d.
func newSettingsForm(d sites.Defaults, onSave func(sites.Defaults), onCancel func(), onError func(string), onImport func()) *tview.Form {
	f := tview.NewForm()
	f.AddDropDown("Screen size", screenSizes, sizeIndex(d.ScreenSize), nil)
	f.AddInputField("Code page", strconv.Itoa(d.CodePage), 8, nil, nil)
	f.AddInputField("Device name template", d.DeviceName, 20, nil, nil)
	f.AddCheckbox("Column separators", d.Separators, nil)
	f.AddCheckbox("SEU syntax colouring", d.SourceHighlight, nil)
	f.AddCheckbox("Auto sign-on by default", d.AutoSignon, nil)
	f.AddInputField("Trace file", d.TraceFile, 30, nil, nil)
	keep := ""
	if d.KeepAliveSeconds > 0 {
		keep = strconv.Itoa(d.KeepAliveSeconds)
	}
	f.AddInputField("Keep-alive seconds", keep, 8, nil, nil)
	f.AddTextView("Overrides", DescribeOverrides(d), 40, 1, false, false)
	save := func() {
		nd, err := readSettingsForm(f).toDefaults()
		if err != nil {
			onError(err.Error())
			return
		}
		onSave(nd)
	}
	f.AddButton("Save", save)
	f.AddButton("Cancel", onCancel)
	if onImport != nil {
		f.AddButton("Import VS Code", onImport)
	}
	f.SetCancelFunc(onCancel)
	prepForm(f, save)
	return f
}

// showSettings opens the settings page (`:settings`, the site list's s key and the login
// form's Settings button). Save writes the defaults block of the sites file and applies what
// can apply at once: the column separators, and the screen size and code page of the next
// connect (a :size or :cp typed earlier is dropped). The other values are read when they are
// needed: the device name, auto sign-on and keep-alive by the next connect.
func (a *App) showSettings() {
	if a.topOverlay() == "settings" {
		return
	}
	var form *tview.Form
	form = newSettingsForm(a.opts.Sites.EffectiveDefaults(),
		func(d sites.Defaults) {
			// The page edits the plain values; the imported overrides stay as they are.
			cur := a.opts.Sites.EffectiveDefaults()
			d.Colors, d.Keys, d.Watermark = cur.Colors, cur.Keys, cur.Watermark
			if err := a.saveDefaults(d); err != nil {
				form.SetTitle(" save: " + err.Error() + " ")
				return
			}
			a.closeNamed("settings")
			a.mu.Lock()
			a.flashLocked("Settings saved")
			a.mu.Unlock()
		},
		func() { a.closeNamed("settings") },
		func(msg string) { form.SetTitle(" " + msg + " ") },
		func() {
			msg := a.importVSCode("")
			a.closeNamed("settings")
			a.mu.Lock()
			a.flashLocked(msg)
			a.mu.Unlock()
		},
	)
	form.SetBorder(true).SetTitle(" Settings   Ctrl+S save   Esc cancel ")
	a.showOverlay("settings", form, 64, 23)
}

// importVSCode runs the VS Code extension import (`:import [path]`, the settings page's
// button): the sessions become sites (passwords stay in VS Code: type them once), the
// defaults, colours, key overrides and watermark replace the defaults block. The result
// is applied at once.
func (a *App) importVSCode(path string) string {
	r, from, err := vscode.ImportFile(path)
	if err != nil {
		return err.Error()
	}
	old := a.opts.Sites.Defaults
	oldSites := append([]sites.Site(nil), a.opts.Sites.Sites...)
	added, updated, cleared := vscode.Apply(a.opts.Sites, r)
	if err := a.opts.Sites.Save(); err != nil {
		a.opts.Sites.Defaults, a.opts.Sites.Sites = old, oldSites
		return "save: " + err.Error()
	}
	d := a.opts.Sites.EffectiveDefaults()
	a.eachScreen(func(s *Screen) { s.Separators = d.Separators })
	a.applyDefaults(d)
	a.mu.Lock()
	a.sizeOv, a.cpOv = "", 0
	a.mu.Unlock()
	msg := vscode.Summary(r, added, updated) + " from " + from
	if len(cleared) > 0 {
		msg += "; " + strings.Join(cleared, "; ")
	}
	return msg
}

// saveDefaults stores d in the sites file and applies it. The file is only changed in memory
// when it was written.
func (a *App) saveDefaults(d sites.Defaults) error {
	old := a.opts.Sites.Defaults
	a.opts.Sites.Defaults = &d
	if err := a.opts.Sites.Save(); err != nil {
		a.opts.Sites.Defaults = old
		return err
	}
	a.eachScreen(func(s *Screen) { s.Separators = d.Separators })
	a.eachScreen(func(s *Screen) { s.Highlight = d.SourceHighlight })
	a.applyDefaults(d)
	a.mu.Lock()
	a.sizeOv, a.cpOv = "", 0
	a.mu.Unlock()
	return nil
}
