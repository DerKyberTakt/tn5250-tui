package tui

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/DerKyberTakt/tn5250-tui/internal/sites"
)

var screenSizes = []string{"27x132", "24x80"}

// siteInput holds the raw text of a site form.
type siteInput struct {
	Name, Host, Port, User, Password, ScreenSize, CodePage, DeviceName string
	TLS, Trust, Auto, Demo                                             bool
	DemoLabel                                                          string // kept from the site being edited
}

// toSite validates the input and builds the Site (defaults applied).
func (in siteInput) toSite() (sites.Site, error) {
	s := sites.Site{
		Name:                strings.TrimSpace(in.Name),
		Host:                strings.TrimSpace(in.Host),
		TLS:                 in.TLS,
		TrustAnyCertificate: in.Trust,
		User:                strings.TrimSpace(in.User),
		Password:            in.Password,
		ScreenSize:          in.ScreenSize,
		DeviceName:          strings.TrimSpace(in.DeviceName),
		AutoSignon:          in.Auto,
		Demo:                in.Demo,
		DemoLabel:           in.DemoLabel,
	}
	if s.Name == "" {
		return s, errors.New("Name is required")
	}
	if s.Host == "" {
		return s, errors.New("Host is required")
	}
	if p := strings.TrimSpace(in.Port); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			return s, errors.New("Port must be 1-65535")
		}
		s.Port = n
	}
	if c := strings.TrimSpace(in.CodePage); c != "" {
		n, err := strconv.Atoi(c)
		if err != nil || n < 1 {
			return s, errors.New("Code page must be a CCSID number")
		}
		s.CodePage = n
	}
	return s.WithDefaults(), nil
}

func sizeIndex(size string) int {
	for i, s := range screenSizes {
		if s == size {
			return i
		}
	}
	return 0
}

// addPasswordField adds a Password input (masked, like the sign-on screen) plus a
// "Show password" checkbox.
func addPasswordField(f *tview.Form, value string) {
	f.AddInputField("Password", value, 30, nil, nil)
	if it, ok := f.GetFormItemByLabel("Password").(*tview.InputField); ok {
		it.SetMaskCharacter('*')
	}
	f.AddCheckbox("Show password", false, func(on bool) {
		if it, ok := f.GetFormItemByLabel("Password").(*tview.InputField); ok {
			if on {
				it.SetMaskCharacter(0)
			} else {
				it.SetMaskCharacter('*')
			}
		}
	})
}

func formText(f *tview.Form, label string) string {
	if it, ok := f.GetFormItemByLabel(label).(*tview.InputField); ok {
		return it.GetText()
	}
	return ""
}

func formChecked(f *tview.Form, label string) bool {
	if it, ok := f.GetFormItemByLabel(label).(*tview.Checkbox); ok {
		return it.IsChecked()
	}
	return false
}

// readSiteForm collects the site form's fields.
func readSiteForm(f *tview.Form) siteInput {
	in := siteInput{
		Name: formText(f, "Name"), Host: formText(f, "Host"), Port: formText(f, "Port"),
		User: formText(f, "User"), Password: formText(f, "Password"),
		CodePage: formText(f, "Code page"), DeviceName: formText(f, "Device name"),
		TLS: formChecked(f, "TLS"), Trust: formChecked(f, "Trust any certificate"),
		Auto: formChecked(f, "Auto sign-on"), Demo: formChecked(f, "Demo system"),
	}
	if dd, ok := f.GetFormItemByLabel("Screen size").(*tview.DropDown); ok {
		_, in.ScreenSize = dd.GetCurrentOption()
	}
	return in
}

// newSiteForm builds the add/edit form for s.
func newSiteForm(s sites.Site, onSave func(sites.Site), onCancel func(), onError func(string)) *tview.Form {
	s = s.WithDefaults()
	f := tview.NewForm()
	f.AddInputField("Name", s.Name, 30, nil, nil)
	f.AddInputField("Host", s.Host, 40, nil, nil)
	f.AddInputField("Port", strconv.Itoa(s.Port), 8, tview.InputFieldInteger, nil)
	f.AddCheckbox("TLS", s.TLS, nil)
	f.AddCheckbox("Trust any certificate", s.TrustAnyCertificate, nil)
	f.AddInputField("User", s.User, 20, nil, nil)
	addPasswordField(f, s.Password)
	f.AddDropDown("Screen size", screenSizes, sizeIndex(s.ScreenSize), nil)
	f.AddInputField("Code page", strconv.Itoa(s.CodePage), 8, tview.InputFieldInteger, nil)
	f.AddInputField("Device name", s.DeviceName, 12, nil, nil)
	f.AddCheckbox("Auto sign-on", s.AutoSignon, nil)
	f.AddCheckbox("Demo system", s.Demo, nil)
	f.AddButton("Save", func() {
		in := readSiteForm(f)
		in.DemoLabel = s.DemoLabel
		site, err := in.toSite()
		if err != nil {
			onError(err.Error())
			return
		}
		onSave(site)
	})
	f.AddButton("Cancel", onCancel)
	f.SetCancelFunc(onCancel)
	return f
}

// ---- site manager ----------------------------------------------------------

type siteManager struct {
	a    *App
	list *tview.List
	hint *tview.TextView
	root *tview.Flex
}

func (m *siteManager) refresh() {
	cur := m.list.GetCurrentItem()
	m.list.Clear()
	for _, s := range m.a.opts.Sites.Sites {
		d := m.a.opts.Sites.Complete(s)
		tls := ""
		if d.TLS {
			tls = " tls"
		}
		m.list.AddItem(s.Name, fmt.Sprintf("%s:%d%s  %s  %s", d.Host, d.Port, tls, d.ScreenSize, s.User), 0, nil)
	}
	if cur >= m.list.GetItemCount() {
		cur = m.list.GetItemCount() - 1
	}
	if cur >= 0 {
		m.list.SetCurrentItem(cur)
	}
	if m.list.GetItemCount() == 0 {
		m.hint.SetText("No sites yet: press a to add one.")
	} else {
		m.hint.SetText("Enter connect   a add   e edit   d delete   s settings   Esc close")
	}
}

func (m *siteManager) selected() (sites.Site, bool) {
	i := m.list.GetCurrentItem()
	if i < 0 || i >= len(m.a.opts.Sites.Sites) {
		return sites.Site{}, false
	}
	return m.a.opts.Sites.Sites[i], true
}

func (a *App) showSites() {
	if a.topOverlay() == "sites" {
		return
	}
	m := &siteManager{a: a, list: tview.NewList(), hint: tview.NewTextView()}
	m.list.ShowSecondaryText(true).SetSelectedFocusOnly(true)
	m.list.SetBorder(true).SetTitle(" Sites ")
	m.hint.SetTextColor(oiaAlert)
	m.root = tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(m.list, 0, 1, true).
		AddItem(m.hint, 1, 0, false)
	m.refresh()
	m.list.SetSelectedFunc(func(i int, _, _ string, _ rune) {
		if s, ok := m.selected(); ok {
			a.closeNamed("sites")
			a.connectTo(s, s.Name)
		}
	})
	m.root.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		switch ev.Key() {
		case tcell.KeyEscape:
			a.closeNamed("sites")
			return nil
		case tcell.KeyRune:
			switch ev.Rune() {
			case 'a':
				a.showSiteForm(m, sites.Site{}, "")
				return nil
			case 'e':
				if s, ok := m.selected(); ok {
					a.showSiteForm(m, s, s.Name)
				}
				return nil
			case 'd':
				if s, ok := m.selected(); ok {
					a.confirmDelete(m, s.Name)
				}
				return nil
			case 's':
				a.showSettings()
				return nil
			}
		}
		return ev
	})
	a.showOverlay("sites", m.root, 78, 20)
	a.app.SetFocus(m.list)
	a.overlays[len(a.overlays)-1].focus = m.list
}

func (a *App) confirmDelete(m *siteManager, name string) {
	md := tview.NewModal().SetText("Delete site " + name + "?").AddButtons([]string{"Delete", "Cancel"})
	md.SetDoneFunc(func(_ int, label string) {
		a.closeNamed("delete")
		if label == "Delete" {
			a.opts.Sites.Delete(name)
			_ = a.opts.Sites.Save()
			m.refresh()
		}
	})
	a.showOverlay("delete", md, 0, 0)
}

// showSiteForm opens the add/edit form; oldName is "" for a new site.
func (a *App) showSiteForm(m *siteManager, s sites.Site, oldName string) {
	if oldName == "" { // a new site starts from the defaults
		d := a.opts.Sites.EffectiveDefaults()
		s.ScreenSize, s.CodePage, s.DeviceName, s.AutoSignon = d.ScreenSize, d.CodePage, d.DeviceName, d.AutoSignon
	}
	var form *tview.Form
	form = newSiteForm(s,
		func(site sites.Site) {
			if oldName != "" && !strings.EqualFold(oldName, site.Name) {
				a.opts.Sites.Delete(oldName)
			}
			a.opts.Sites.Upsert(site)
			if err := a.opts.Sites.Save(); err != nil {
				a.setNotice("save: " + err.Error())
			}
			a.closeNamed("siteform")
			m.refresh()
		},
		func() { a.closeNamed("siteform") },
		func(msg string) { form.SetTitle(" " + msg + " ") },
	)
	title := " Add site "
	if oldName != "" {
		title = " Edit site "
	}
	form.SetBorder(true).SetTitle(title)
	a.showOverlay("siteform", form, 60, 24)
}
