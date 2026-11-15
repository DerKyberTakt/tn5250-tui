package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/DerKyberTakt/tn5250-tui/internal/agent"
	"github.com/DerKyberTakt/tn5250-tui/internal/sites"
	"github.com/DerKyberTakt/tn5250-tui/internal/viewer"
)

// runMCP is `tn5250-tui mcp`: an MCP server on stdin/stdout that hosts 5250 sessions for an
// AI agent, plus a loopback listener (random port, token file) through which
// `tn5250-tui attach` shows those sessions live.
func runMCP(args []string) int {
	fs := flag.NewFlagSet("tn5250-tui mcp", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	site := fs.String("site", "", "saved site the agent connects to when a call names none")
	host := fs.String("host", "", "host[:port] for the default target (instead of --site)")
	useTLS := fs.Bool("tls", false, "telnet over TLS for --host")
	trustAny := fs.Bool("trust-any-certificate", false, "skip the TLS certificate check for --host")
	user := fs.String("user", "", "user profile for the default target (env TN5250_USER)")
	password := fs.String("password", "", "password for --user (env TN5250_PASSWORD; prefer the sites file)")
	size := fs.String("size", "", "screen size 27x132 (default) or 24x80")
	codePage := fs.Int("cp", 0, "EBCDIC code page (default 37)")
	device := fs.String("device", "", "workstation (device) name to request")
	readOnly := fs.Bool("read-only", false, "refuse typed text that starts a command that changes things (DLT, CHG, CRT, SBMJOB, CALL, ...)")
	audit := fs.String("audit", "", "append one JSON line per agent action to this file (stderr always)")
	maxSessions := fs.Int("max-sessions", agent.DefaultMaxSessions, "open sessions at most")
	noListen := fs.Bool("no-listen", false, "no loopback listener for tn5250-tui attach")
	label := fs.String("label", "", "name shown by tn5250-tui attach --list (default: the site or host)")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: tn5250-tui mcp [--site NAME | --host H[:P]] [--read-only] [--audit FILE] ...")
		fs.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nsites file: %s\ntoken files: %s\n", sites.Path(), viewer.TokenDir())
	}
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if *user == "" {
		*user = os.Getenv("TN5250_USER")
	}
	if *password == "" {
		*password = os.Getenv("TN5250_PASSWORD")
	}
	f, err := sites.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "tn5250-tui mcp: sites file %s: %v\n", sites.Path(), err)
		return 1
	}
	for _, w := range f.Warnings {
		fmt.Fprintln(os.Stderr, "tn5250-tui mcp:", w)
	}
	cfg := agent.Config{Sites: f, Site: *site, TLS: *useTLS, TrustAny: *trustAny, User: *user, Password: *password,
		ScreenSize: *size, CodePage: *codePage, DeviceName: *device, AuditFile: *audit, ReadOnly: *readOnly, MaxSessions: *maxSessions}
	if *host != "" {
		h, p, err := parseHostPort(*host)
		if err != nil {
			fmt.Fprintln(os.Stderr, "tn5250-tui mcp:", err)
			return 2
		}
		cfg.Host, cfg.Port = h, p
	}
	if *site != "" {
		if _, ok := f.Find(*site); !ok {
			fmt.Fprintf(os.Stderr, "tn5250-tui mcp: no site %q in %s\n", *site, sites.Path())
			return 1
		}
	}
	agent.Version = version
	m, err := agent.NewManager(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "tn5250-tui mcp:", err)
		return 1
	}
	defer m.Close()

	if !*noListen {
		token := viewer.NewToken()
		srv, err := viewer.Listen(token, managerSource{m})
		if err != nil {
			fmt.Fprintln(os.Stderr, "tn5250-tui mcp: viewer listener:", err, "(continuing without)")
		} else {
			defer srv.Close()
			lbl := *label
			if lbl == "" {
				lbl = *site
				if lbl == "" {
					lbl = *host
				}
			}
			tf := &viewer.TokenFile{PID: os.Getpid(), Addr: srv.Addr(), Token: token, Started: time.Now(), Label: lbl}
			if p, err := tf.Write(); err != nil {
				fmt.Fprintln(os.Stderr, "tn5250-tui mcp: token file:", err)
			} else {
				defer tf.Remove()
				fmt.Fprintf(os.Stderr, "tn5250-tui mcp: viewers attach with `tn5250-tui attach` (%s)\n", p)
			}
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := agent.NewServer(m).Run(ctx, &mcp.StdioTransport{}); err != nil && ctx.Err() == nil {
		fmt.Fprintln(os.Stderr, "tn5250-tui mcp:", err)
		return 1
	}
	return 0
}

// parseHostPort splits host[:port]; port 0 means "the site default".
func parseHostPort(s string) (string, int, error) {
	s = strings.TrimSpace(s)
	i := strings.LastIndex(s, ":")
	if i < 0 {
		return s, 0, nil
	}
	var p int
	if _, err := fmt.Sscanf(s[i+1:], "%d", &p); err != nil || p < 1 || p > 65535 {
		return "", 0, fmt.Errorf("bad port in %q", s)
	}
	return s[:i], p, nil
}

// managerSource adapts the agent's session manager to the viewer server.
type managerSource struct{ m *agent.Manager }

func (s managerSource) Sessions() []viewer.SessionInfo {
	var out []viewer.SessionInfo
	for _, si := range s.m.Sessions().Sessions {
		out = append(out, viewer.SessionInfo{Session: si.Session, Host: si.Host, Device: si.Device, Connected: si.Connected,
			Since: si.Since, Seq: si.Seq, Inhibited: si.Inhibited})
	}
	return out
}

func (s managerSource) Frame(session string) (viewer.Frame, bool) {
	snap, err := s.m.Snapshot(session)
	if err != nil {
		return viewer.Frame{}, false
	}
	oia, _ := s.m.OIA(session)
	return viewer.EncodeFrame(session, snap, oia), true
}

func (s managerSource) Subscribe(session string) (<-chan struct{}, func(), bool) {
	ch, cancel, err := s.m.Subscribe(session)
	if err != nil {
		return nil, nil, false
	}
	return ch, cancel, true
}
