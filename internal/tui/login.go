package tui

import (
	"errors"
	"strconv"
	"strings"

	"github.com/rivo/tview"

	"github.com/DerKyberTakt/tn5250-tui/internal/sites"
)

// loginInput is the raw text of the login form.
type loginInput struct {
	Host, Port, User, Password, SaveAs string
	TLS                                bool
}

// toSite validates the login form. A site name is only needed to save it.
func (in loginInput) toSite() (sites.Site, error) {
	host, port, err := ParseTarget(in.Host)
	if err != nil {
		return sites.Site{}, errors.New("Host is required")
	}
	s := sites.Site{
		Name: strings.TrimSpace(in.SaveAs), Host: host, Port: port, TLS: in.TLS,
		User: strings.TrimSpace(in.User), Password: in.Password,
	}
	if p := strings.TrimSpace(in.Port); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			return s, errors.New("Port must be 1-65535")
		}
		s.Port = n
	}
	s.AutoSignon = s.User != "" && s.Password != ""
	if s.Port == 0 { // size, code page and device name come from the defaults when connecting
		s.Port = 23
		if s.TLS {
			s.Port = 992
		}
	}
	return s, nil
}

func readLoginForm(f *tview.Form) loginInput {
	return loginInput{
		Host: formText(f, "Host"), Port: formText(f, "Port"), User: formText(f, "User"),
		Password: formText(f, "Password"), SaveAs: formText(f, "Save as site"),
		TLS: formChecked(f, "TLS"),
	}
}

// newLoginForm builds the login form.
func newLoginForm(onConnect func(site sites.Site, save bool), onCancel func(), onSettings func(), onError func(string)) *tview.Form {
	f := tview.NewForm()
	f.AddInputField("Host", "", 40, nil, nil)
	f.AddInputField("Port", "", 8, tview.InputFieldInteger, nil)
	f.AddCheckbox("TLS", false, nil)
	f.AddInputField("User", "", 20, nil, nil)
	addPasswordField(f, "")
	f.AddInputField("Save as site", "", 30, nil, nil)
	f.AddButton("Connect", func() {
		in := readLoginForm(f)
		site, err := in.toSite()
		if err != nil {
			onError(err.Error())
			return
		}
		onConnect(site, site.Name != "")
	})
	f.AddButton("Settings", onSettings)
	f.AddButton("Cancel", onCancel)
	f.SetCancelFunc(onCancel)
	prepForm(f, nil)
	return f
}

// showLogin opens the login form (shown when there are no sites, or by
// `connect` with nothing to connect to).
func (a *App) showLogin() {
	if a.topOverlay() == "login" {
		return
	}
	var form *tview.Form
	form = newLoginForm(
		func(site sites.Site, save bool) {
			a.closeNamed("login")
			if save {
				a.opts.Sites.Upsert(site)
				if err := a.opts.Sites.Save(); err != nil {
					a.setNotice("save: " + err.Error())
				}
				a.connectTo(site, site.Name)
				return
			}
			a.connectTo(site, "")
		},
		func() { a.closeNamed("login") },
		a.showSettings,
		func(msg string) { form.SetTitle(" " + msg + " ") },
	)
	form.SetBorder(true).SetTitle(" Connect ")
	a.showOverlay("login", form, 60, 19)
}
