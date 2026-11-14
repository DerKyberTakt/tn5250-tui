// Command tn5250-tui is a lightweight 5250 terminal for the console.
//
//	tn5250-tui                      the sites list (or the login form when there are none)
//	tn5250-tui host[:port] [--tls]  connect straight away
//	tn5250-tui --site NAME          connect to a saved site
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/DerKyberTakt/tn5250-tui/internal/sites"
	"github.com/DerKyberTakt/tn5250-tui/internal/tn5250"
	"github.com/DerKyberTakt/tn5250-tui/internal/tui"
)

// version is set by the release build (-ldflags "-X main.version=v0.1.0").
var version = "dev"

func main() {
	fs := flag.NewFlagSet("tn5250-tui", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	site := fs.String("site", "", "connect to this saved site")
	useTLS := fs.Bool("tls", false, "telnet over TLS (host target only; a site keeps its own setting)")
	user := fs.String("user", "", "sign on as this user profile (with --password)")
	password := fs.String("password", "", "password for --user (plain text, as the sites file)")
	traceFile := fs.String("trace", "", "write a hex trace of the connection to this file")
	size := fs.String("size", "", "screen size for a host target: 24x80 or 27x132")
	codePage := fs.Int("cp", 0, "EBCDIC code page for a host target (default 37)")
	device := fs.String("device", "", "workstation (device) name to request")
	showVersion := fs.Bool("version", false, "print the version and exit")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "tn5250-tui %s - a lightweight 5250 terminal for the console\n\n", version)
		fmt.Fprintln(os.Stderr, "usage: tn5250-tui [flags] [host[:port]]")
		fs.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nsites file: %s\n", sites.Path())
	}
	if err := fs.Parse(os.Args[1:]); err != nil {
		if err == flag.ErrHelp {
			os.Exit(0)
		}
		os.Exit(2)
	}
	if *showVersion {
		fmt.Println("tn5250-tui", version)
		return
	}
	target := *site
	if rest := fs.Args(); len(rest) > 0 {
		if target != "" {
			fmt.Fprintln(os.Stderr, "tn5250-tui: give either --site or a host, not both")
			os.Exit(2)
		}
		target = strings.TrimSpace(rest[0])
	}

	f, err := sites.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "tn5250-tui: sites file %s: %v\n", sites.Path(), err)
		os.Exit(1)
	}

	app := tui.New(tui.AppOptions{
		EngineFactory: newEngine,
		Sites:         f,
		Target:        target,
		TLS:           *useTLS,
		User:          *user,
		Password:      *password,
		TraceFile:     *traceFile,
		ScreenSize:    *size,
		CodePage:      *codePage,
		DeviceName:    *device,
	})
	if err := app.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "tn5250-tui: %v\n", err)
		os.Exit(1)
	}
}

// newEngine builds a tn5250 session for one connection, wrapped for the TUI.
func newEngine(size string, cp int, dev string, send func(b, forTrace []byte), update func()) (tui.Engine, error) {
	s, err := tn5250.New(tn5250.Options{ScreenSize: size, CodePage: cp, DeviceName: dev, TypeAhead: true}, tn5250.Events{
		Send:   send,
		Update: update,
	})
	if err != nil {
		return nil, err
	}
	return &engine{s}, nil
}

// engine adapts a tn5250.Session to the TUI's Engine interface: the types are the same
// shape, the methods differ only in the Action type.
type engine struct {
	s *tn5250.Session
}

func (e *engine) Feed(b []byte)          { e.s.Feed(b) }
func (e *engine) Press(action string)    { e.s.Press(tn5250.Action(action)) }
func (e *engine) Type(text string)       { e.s.Type(text) }
func (e *engine) SetCursor(row, col int) { e.s.SetCursor(row, col) }
func (e *engine) Reset()                 { e.s.Reset() }
func (e *engine) OIA(connected bool) tui.OIA {
	o := e.s.OIA(connected)
	return tui.OIA{
		Connected:      o.Connected,
		InputInhibited: o.InputInhibited,
		Insert:         o.Insert,
		MessageWaiting: o.MessageWaiting,
		DeviceName:     o.DeviceName,
		Message:        o.Message,
		QueuedKeys:     o.QueuedKeys,
	}
}

func (e *engine) Snapshot() tui.Snapshot {
	s := e.s.Snapshot()
	out := tui.Snapshot{Rows: s.Rows, Cols: s.Cols, Seq: s.Seq}
	out.Cursor.Row, out.Cursor.Col = s.Cursor.Row, s.Cursor.Col
	out.Cells = make([]tui.Cell, len(s.Cells))
	for i, c := range s.Cells {
		out.Cells[i] = tui.Cell{Ch: c.Ch, Attr: c.Attr}
	}
	out.Fields = make([]tui.FieldInfo, len(s.Fields))
	for i, f := range s.Fields {
		out.Fields[i] = tui.FieldInfo{Start: f.Start, Length: f.Length, Bypass: f.Bypass, Nondisplay: f.Nondisplay, MDT: f.MDT}
	}
	return out
}
