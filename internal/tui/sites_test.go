package tui

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/DerKyberTakt/tn5250-tui/internal/sites"
)

func TestSiteInputValidation(t *testing.T) {
	good := siteInput{Name: " pub ", Host: "PUB400.COM", Port: "992", TLS: true, Trust: true, User: "U", Password: " p w ", ScreenSize: "27x132", CodePage: "273", DeviceName: "DEV1", Auto: true}
	s, err := good.toSite()
	if err != nil {
		t.Fatal(err)
	}
	want := sites.Site{Name: "pub", Host: "PUB400.COM", Port: 992, TLS: true, TrustAnyCertificate: true, User: "U", Password: " p w ", ScreenSize: "27x132", CodePage: 273, DeviceName: "DEV1", AutoSignon: true}
	if s != want {
		t.Fatalf("got %+v\nwant %+v", s, want)
	}
	d, err := siteInput{Name: "x", Host: "h"}.toSite()
	if err != nil || d.Port != 23 || d.CodePage != 37 || d.ScreenSize != "27x132" {
		t.Fatalf("defaults %+v %v", d, err)
	}
	for _, bad := range []siteInput{
		{Host: "h"}, {Name: "n"}, {Name: "n", Host: "h", Port: "0"}, {Name: "n", Host: "h", Port: "x"},
		{Name: "n", Host: "h", CodePage: "abc"},
	} {
		if _, err := bad.toSite(); err == nil {
			t.Errorf("%+v should fail", bad)
		}
	}
}

func TestSiteFormRoundTrip(t *testing.T) {
	var saved *sites.Site
	var errMsg string
	in := sites.Site{Name: "pub", Host: "PUB400.COM", Port: 992, TLS: true, User: "U", Password: "pw", ScreenSize: "27x132", CodePage: 273, DeviceName: "DEV", AutoSignon: true}
	f := newSiteForm(in, func(s sites.Site) { saved = &s }, func() {}, func(m string) { errMsg = m })
	// the form shows the plain password unless the mask box is ticked
	pw := f.GetFormItemByLabel("Password").(*tview.InputField)
	if pw.GetText() != "pw" {
		t.Fatalf("password field %q", pw.GetText())
	}
	f.GetFormItemByLabel("Mask password").(*tview.Checkbox).SetChecked(true)
	f.GetFormItemByLabel("Port").(*tview.InputField).SetText("23")
	pressButton(f, "Save")
	if saved == nil || errMsg != "" {
		t.Fatalf("saved=%v err=%q", saved, errMsg)
	}
	want := in
	want.Port = 23
	if *saved != want {
		t.Fatalf("got %+v\nwant %+v", *saved, want)
	}
	f.GetFormItemByLabel("Host").(*tview.InputField).SetText("")
	saved = nil
	pressButton(f, "Save")
	if saved != nil || !strings.Contains(errMsg, "Host") {
		t.Fatalf("saved=%v err=%q", saved, errMsg)
	}
}

func pressButton(f *tview.Form, label string) {
	b := f.GetButton(f.GetButtonIndex(label))
	b.InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), func(tview.Primitive) {})
}

func TestLoginInput(t *testing.T) {
	s, err := loginInput{Host: "pub400.com:992", TLS: true, User: "U", Password: "P", SaveAs: "pub"}.toSite()
	if err != nil || s.Host != "pub400.com" || s.Port != 992 || !s.TLS || !s.AutoSignon || s.Name != "pub" {
		t.Fatalf("%+v %v", s, err)
	}
	s, err = loginInput{Host: "h", Port: "2323"}.toSite()
	if err != nil || s.Port != 2323 || s.AutoSignon || s.Name != "" {
		t.Fatalf("%+v %v", s, err)
	}
	if _, err = (loginInput{}).toSite(); err == nil {
		t.Fatal("empty host must fail")
	}
}

func TestSiteManagerListsAndConnects(t *testing.T) {
	r := newRig(t, func(o *AppOptions) {
		o.Sites = &sites.File{}
		o.Sites.Upsert(sites.Site{Name: "alpha", Host: "PUB400.COM", Port: 23})
		o.Sites.Upsert(sites.Site{Name: "beta", Host: "PUB400.COM", Port: 992, TLS: true})
	})
	r.a.RunCommand("sites")
	if r.a.topOverlay() != "sites" {
		t.Fatalf("overlay %q", r.a.topOverlay())
	}
	r.a.closeNamed("sites")
	if r.a.topOverlay() != "" {
		t.Fatal("sites not closed")
	}
}

func TestLoginFormConnectAndSave(t *testing.T) {
	r := newRig(t, nil)
	r.a.RunCommand("connect") // nothing to connect to: login form
	if r.a.topOverlay() != "login" {
		t.Fatalf("overlay %q", r.a.topOverlay())
	}
	form := r.a.overlays[len(r.a.overlays)-1].focus.(*tview.Form)
	form.GetFormItemByLabel("Host").(*tview.InputField).SetText("PUB400.COM")
	form.GetFormItemByLabel("User").(*tview.InputField).SetText("USER1")
	form.GetFormItemByLabel("Password").(*tview.InputField).SetText("PW")
	form.GetFormItemByLabel("Save as site").(*tview.InputField).SetText("pub")
	pressButton(form, "Connect")
	waitFor(t, "connected", r.isConnected)
	if _, ok := r.a.opts.Sites.Find("pub"); !ok {
		t.Fatal("site not saved")
	}
	if r.a.topOverlay() != "" {
		t.Fatal("login form still open")
	}
	loaded, err := sites.Load()
	if err != nil || len(loaded.Sites) != 1 || loaded.Sites[0].Password != "PW" {
		t.Fatalf("file: %+v %v", loaded, err)
	}
}
