package tui

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"unicode"

	"github.com/gdamore/tcell/v2"
)

// Command is a parsed command-bar line.
type Command struct {
	Name string   // lower case, aliases resolved (q/exit -> quit)
	Args []string // for send: one element, the rest of the line verbatim
	TLS  bool     // --tls was given
}

// ParseCommand parses a command-bar line (a leading ':' is ignored). It is a
// pure function: nothing is executed.
func ParseCommand(line string) (Command, error) {
	line = strings.TrimSpace(line)
	line = strings.TrimSpace(strings.TrimPrefix(line, ":"))
	if line == "" {
		return Command{}, errors.New("empty command")
	}
	name, rest := line, ""
	if i := strings.IndexFunc(line, unicode.IsSpace); i >= 0 {
		name, rest = line[:i], strings.TrimSpace(line[i:])
	}
	c := Command{Name: strings.ToLower(name)}
	switch c.Name {
	case "q", "exit":
		c.Name = "quit"
	case "send":
		if rest == "" {
			return c, errors.New("send: text missing")
		}
		c.Args = []string{rest}
		return c, nil
	}
	args, err := splitArgs(rest)
	if err != nil {
		return c, err
	}
	for _, a := range args {
		if a == "--tls" {
			c.TLS = true
			continue
		}
		c.Args = append(c.Args, a)
	}
	return c, nil
}

// splitArgs splits on blanks; double quotes group words.
func splitArgs(s string) ([]string, error) {
	var out []string
	var cur strings.Builder
	in, has := false, false
	for _, r := range s {
		switch {
		case r == '"':
			in = !in
			has = true
		case unicode.IsSpace(r) && !in:
			if has {
				out = append(out, cur.String())
				cur.Reset()
				has = false
			}
		default:
			cur.WriteRune(r)
			has = true
		}
	}
	if in {
		return nil, errors.New("unbalanced quote")
	}
	if has {
		out = append(out, cur.String())
	}
	return out, nil
}

// ParseTarget splits "host", "host:port", "[v6]:port" or a bare IPv6 literal.
// port is 0 when absent.
func ParseTarget(s string) (host string, port int, err error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", 0, errors.New("no host")
	}
	switch {
	case strings.HasPrefix(s, "["):
		if strings.HasSuffix(s, "]") {
			return s[1 : len(s)-1], 0, nil
		}
		h, p, e := net.SplitHostPort(s)
		if e != nil {
			return "", 0, fmt.Errorf("bad address %q", s)
		}
		host, s = h, p
	case strings.Count(s, ":") == 1:
		i := strings.LastIndex(s, ":")
		host, s = s[:i], s[i+1:]
	case strings.Count(s, ":") > 1:
		return s, 0, nil // IPv6 literal without brackets
	default:
		return s, 0, nil
	}
	if host == "" {
		return "", 0, errors.New("no host")
	}
	n, e := strconv.Atoi(s)
	if e != nil || n < 1 || n > 65535 {
		return "", 0, fmt.Errorf("bad port %q", s)
	}
	return host, n, nil
}

// CommandHelp lists the commands for the help modal.
func CommandHelp() [][2]string {
	return [][2]string{
		{"connect [site|host[:port]] [--tls]", "connect (no argument: reconnect, or the login form)"},
		{"disconnect", "close the connection"},
		{"sites", "site manager: add, edit, delete, connect"},
		{"signon", "typed sign-on with the site's user and password"},
		{"key <action>", "press any action (pf13, fieldExit, sysreq, ...); :<action> works too"},
		{"send <text>", "type text, then Enter"},
		{"trace on [file] | off", "hex trace of the traffic (default tn5250-trace.log)"},
		{"dump [file]", "write the screen text (default tn5250-screen.txt)"},
		{"size 24x80|27x132", "screen size for the next connect"},
		{"cp <ccsid>", "code page for the next connect"},
		{"help", "this list"},
		{"q, quit", "quit"},
	}
}

// RunCommand parses and executes a command-bar line. Problems are shown as a
// one-line message on the OIA line.
func (a *App) RunCommand(line string) {
	a.setNotice("")
	if msg := a.execute(line); msg != "" {
		a.setNotice(msg)
	}
}

// execute runs the command and returns the message to show ("" = none).
func (a *App) execute(line string) string {
	c, err := ParseCommand(line)
	if err != nil {
		return err.Error()
	}
	switch c.Name {
	case "connect":
		return a.cmdConnect(c)
	case "disconnect":
		a.Disconnect()
		a.mu.Lock()
		a.cur.Host = "" // an explicit disconnect forgets the reconnect target
		a.mu.Unlock()
		return "Disconnected"
	case "sites":
		a.showSites()
		return ""
	case "signon":
		if err := a.signon(); err != nil {
			return err.Error()
		}
		return ""
	case "key":
		if len(c.Args) != 1 {
			return "usage: key <action>"
		}
		return a.pressNamed(c.Args[0])
	case "send":
		return a.cmdSend(c.Args[0])
	case "trace":
		return a.cmdTrace(c.Args)
	case "dump":
		return a.cmdDump(c.Args)
	case "size":
		if len(c.Args) != 1 || (c.Args[0] != "24x80" && c.Args[0] != "27x132") {
			return "usage: size 24x80|27x132"
		}
		a.mu.Lock()
		a.sizeOv = c.Args[0]
		a.mu.Unlock()
		return "Screen size " + c.Args[0] + " applies on the next connect"
	case "cp":
		n := 0
		if len(c.Args) == 1 {
			n, _ = strconv.Atoi(c.Args[0])
		}
		if n <= 0 {
			return "usage: cp <ccsid>"
		}
		a.mu.Lock()
		a.cpOv = n
		a.mu.Unlock()
		return "Code page " + strconv.Itoa(n) + " applies on the next connect"
	case "help":
		a.showHelp()
		return ""
	case "quit":
		a.showQuit()
		return ""
	}
	if _, ok := LookupAction(c.Name); ok && len(c.Args) == 0 {
		return a.pressNamed(c.Name)
	}
	return "unknown command: " + c.Name + "  (:help)"
}

func (a *App) cmdConnect(c Command) string {
	if len(c.Args) > 1 {
		return "usage: connect [site|host[:port]] [--tls]"
	}
	if len(c.Args) == 0 {
		a.mu.Lock()
		have := a.cur.Host != ""
		a.mu.Unlock()
		if !have {
			a.showLogin()
			return ""
		}
		a.reconnect()
		return ""
	}
	site, saved, err := a.resolveTarget(c.Args[0], c.TLS)
	if err != nil {
		return err.Error()
	}
	a.connectTo(site, saved)
	return ""
}

func (a *App) pressNamed(name string) string {
	act, ok := LookupAction(name)
	if !ok {
		return "unknown action: " + name
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.engine == nil || !a.connected {
		return "not connected"
	}
	a.engine.Press(act)
	return ""
}

func (a *App) cmdSend(text string) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.engine == nil || !a.connected {
		return "not connected"
	}
	a.engine.Type(text)
	a.engine.Press("enter")
	return ""
}

func (a *App) cmdTrace(args []string) string {
	if len(args) == 0 {
		return "usage: trace on [file] | off"
	}
	switch strings.ToLower(args[0]) {
	case "on":
		file := "tn5250-trace.log"
		if len(args) > 1 {
			file = args[1]
		}
		if err := a.traceOn(file); err != nil {
			return "trace: " + err.Error()
		}
		return "Tracing to " + file
	case "off":
		a.traceOff()
		return "Trace off"
	}
	return "usage: trace on [file] | off"
}

func (a *App) cmdDump(args []string) string {
	file := "tn5250-screen.txt"
	if len(args) > 0 {
		file = args[0]
	}
	snap, ok := a.screenSource()
	if !ok {
		return "nothing to dump: not connected"
	}
	if err := os.WriteFile(file, []byte(snap.Text()+"\n"), 0o644); err != nil {
		return "dump: " + err.Error()
	}
	return "Screen written to " + file
}

// ---- the command bar widget ------------------------------------------------

func (a *App) openCommandBar() {
	if a.barOpen || len(a.overlays) > 0 {
		return
	}
	a.barOpen = true
	a.histPos = len(a.history)
	a.bar.SetText("")
	a.flex.ResizeItem(a.bar, 1, 0)
	a.app.SetFocus(a.bar)
}

func (a *App) closeCommandBar() {
	if !a.barOpen {
		return
	}
	a.barOpen = false
	a.flex.ResizeItem(a.bar, 0, 0)
	a.app.SetFocus(a.screen)
}

func (a *App) barDone(key tcell.Key) {
	switch key {
	case tcell.KeyEnter:
		text := strings.TrimSpace(a.bar.GetText())
		a.closeCommandBar()
		if text != "" {
			a.history = append(a.history, text)
			a.RunCommand(text)
		}
	case tcell.KeyEscape:
		a.closeCommandBar()
	}
}

func (a *App) barKey(ev *tcell.EventKey) *tcell.EventKey {
	switch ev.Key() {
	case tcell.KeyCtrlK:
		a.closeCommandBar()
		return nil
	case tcell.KeyUp:
		if a.histPos > 0 {
			a.histPos--
			a.bar.SetText(a.history[a.histPos])
		}
		return nil
	case tcell.KeyDown:
		if a.histPos < len(a.history) {
			a.histPos++
			if a.histPos == len(a.history) {
				a.bar.SetText("")
			} else {
				a.bar.SetText(a.history[a.histPos])
			}
		}
		return nil
	}
	return ev
}
