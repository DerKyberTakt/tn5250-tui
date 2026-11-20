package main

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/DerKyberTakt/tn5250-tui/internal/viewer"
)

// runAttach is `tn5250-tui attach [session]`: watch a session of a running
// `tn5250-tui mcp` server (read-only). With several servers the newest is taken unless
// --pid says which; --list prints them.
func runAttach(args []string) int {
	fs := flag.NewFlagSet("tn5250-tui attach", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	pid := fs.Int("pid", 0, "the server process to attach to (default: the newest)")
	addr := fs.String("addr", "", "server address (with --token; skips the token files)")
	token := fs.String("token", "", "server token for --addr (visible to other local users in the process list; prefer TN5250_VIEWER_TOKEN or --token-file)")
	tokenFile := fs.String("token-file", "", "file holding the server token for --addr (read once; keep it mode 0600)")
	list := fs.Bool("list", false, "list the running servers and their sessions, then exit")
	wait := fs.Bool("wait", false, "wait for an agent session to exist (on any server), then watch it: start the viewer before the agent connects")
	follow := fs.Bool("follow", false, "with --wait: when the watched session ends, wait for the next one instead of leaving")
	exitOnClose := fs.Int("exit-on-close", 0, "leave this many seconds after the watched session ended (0: stay open on its last screen)")
	end := fs.String("end", "", "sign off and close a session (a handle such as s1, or all), then exit; no viewer")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: tn5250-tui attach [--pid N | --addr A [--token-file F | --token T]] [--list | --end S|all | --wait [--follow]] [--exit-on-close N] [session]")
		fs.PrintDefaults()
		fmt.Fprintln(os.Stderr, "\nthe token for --addr comes from --token-file, else the TN5250_VIEWER_TOKEN environment variable, else --token (which other local users can read from the process list)")
		fmt.Fprintf(os.Stderr, "\ntoken files: %s\n", viewer.TokenDir())
	}
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	var entries []viewer.TokenFile
	if *addr != "" {
		tok, err := resolveViewerToken(*token, *tokenFile, os.Getenv)
		if err != nil {
			fmt.Fprintln(os.Stderr, "tn5250-tui attach:", err)
			return 2
		}
		entries = []viewer.TokenFile{{Addr: *addr, Token: tok, Label: "--addr"}}
	} else {
		var err error
		entries, err = viewer.ListTokens(true)
		if err != nil {
			fmt.Fprintln(os.Stderr, "tn5250-tui attach:", err)
			return 1
		}
		if len(entries) == 0 && !*wait {
			fmt.Fprintln(os.Stderr, "tn5250-tui attach: no running tn5250-tui mcp server (start one, give --addr with a token, or use --wait)")
			return 1
		}
	}
	if *end != "" {
		return endSessions(entries, *pid, *end)
	}
	if *wait {
		return waitAndWatch(*addr != "", entries, *pid, fs.Arg(0), *follow, time.Duration(*exitOnClose)*time.Second)
	}
	if *list {
		for _, e := range entries {
			fmt.Println(viewer.Sanitize(viewer.Describe(e)))
			cl, err := viewer.Dial(e.Addr, e.Token, 2*time.Second)
			if err != nil {
				fmt.Println("   ", viewer.Sanitize(err.Error()))
				continue
			}
			ss, _ := cl.Sessions()
			cl.Close()
			for _, s := range ss {
				state := "disconnected"
				if s.Connected {
					state = "connected"
				}
				fmt.Printf("    %-4s %-30s %-12s %s seq %d %s\n", viewer.Sanitize(s.Session), viewer.Sanitize(s.Host), viewer.Sanitize(s.Device), state, s.Seq, viewer.Sanitize(s.Inhibited))
			}
		}
		return 0
	}
	entry := entries[0]
	if *pid != 0 {
		found := false
		for _, e := range entries {
			if e.PID == *pid {
				entry, found = e, true
			}
		}
		if !found {
			fmt.Fprintln(os.Stderr, "tn5250-tui attach: no server with pid", *pid)
			return 1
		}
	}
	cl, err := viewer.Dial(entry.Addr, entry.Token, 3*time.Second)
	if err != nil {
		fmt.Fprintln(os.Stderr, "tn5250-tui attach:", viewer.Sanitize(err.Error()))
		return 1
	}
	defer cl.Close()
	label := entry.Label
	if label == "" {
		label = "pid " + strconv.Itoa(entry.PID)
	}
	v := viewer.New(cl, label)
	v.ExitOnClose = time.Duration(*exitOnClose) * time.Second
	if err := v.Run(fs.Arg(0)); err != nil {
		fmt.Fprintln(os.Stderr, "tn5250-tui attach:", viewer.Sanitize(err.Error()))
		return 1
	}
	return 0
}

// endSessions is `attach --end`: asks the servers (one with --pid) to sign off and close the
// named session, or every connected one for "all".
func endSessions(entries []viewer.TokenFile, pid int, which string) int {
	n, failed := 0, 0
	for _, e := range entries {
		if pid != 0 && e.PID != pid {
			continue
		}
		cl, err := viewer.Dial(e.Addr, e.Token, 2*time.Second)
		if err != nil {
			continue
		}
		ss, _ := cl.Sessions()
		for _, s := range ss {
			if !strings.EqualFold(which, "all") && !strings.EqualFold(which, s.Session) {
				continue
			}
			n++
			signedOff, err := cl.End(s.Session, true)
			switch {
			case err != nil:
				failed++
				fmt.Printf("%s on %s (pid %d): %s\n", viewer.Sanitize(s.Session), viewer.Sanitize(s.Host), e.PID, viewer.Sanitize(err.Error()))
			case signedOff:
				fmt.Printf("%s on %s (pid %d): signed off and closed\n", viewer.Sanitize(s.Session), viewer.Sanitize(s.Host), e.PID)
			default:
				fmt.Printf("%s on %s (pid %d): closed (no command line on its screen to sign off at)\n", viewer.Sanitize(s.Session), viewer.Sanitize(s.Host), e.PID)
			}
		}
		cl.Close()
	}
	if n == 0 {
		fmt.Fprintln(os.Stderr, "tn5250-tui attach: no such session on a running server")
		return 1
	}
	if failed > 0 {
		return 1
	}
	return 0
}

// findSession looks through the servers for a connected session (the named one when session
// is set) and returns an open client on its server.
func findSession(entries []viewer.TokenFile, pid int, session string) (*viewer.Client, viewer.TokenFile, string, bool) {
	for _, e := range entries {
		if pid != 0 && e.PID != pid {
			continue
		}
		cl, err := viewer.Dial(e.Addr, e.Token, 2*time.Second)
		if err != nil {
			continue
		}
		ss, _ := cl.Sessions()
		for _, s := range ss {
			if s.Connected && (session == "" || strings.EqualFold(session, s.Session)) {
				return cl, e, s.Session, true
			}
		}
		cl.Close()
	}
	return nil, viewer.TokenFile{}, "", false
}

// waitAndWatch is `attach --wait`: polls the servers until an agent session is connected,
// watches it, and with follow goes back to waiting when that session ends.
func waitAndWatch(fixed bool, entries []viewer.TokenFile, pid int, session string, follow bool, exitOnClose time.Duration) int {
	if follow && exitOnClose <= 0 {
		exitOnClose = 3 * time.Second
	}
	for {
		said := false
		var cl *viewer.Client
		var entry viewer.TokenFile
		var name string
		for {
			if !fixed {
				if list, err := viewer.ListTokens(true); err == nil {
					entries = list
				}
			}
			var ok bool
			if cl, entry, name, ok = findSession(entries, pid, session); ok {
				break
			}
			if !said {
				fmt.Fprintln(os.Stderr, "tn5250-tui attach: waiting for an agent session (Ctrl+C stops) ...")
				said = true
			}
			time.Sleep(300 * time.Millisecond)
		}
		label := entry.Label
		if label == "" {
			label = "pid " + strconv.Itoa(entry.PID)
		}
		v := viewer.New(cl, label)
		v.ExitOnClose = exitOnClose
		err := v.Run(name)
		cl.Close()
		if err != nil {
			fmt.Fprintln(os.Stderr, "tn5250-tui attach:", viewer.Sanitize(err.Error()))
			return 1
		}
		if !follow || !v.Ended() {
			return 0
		}
	}
}

// EnvViewerToken names the environment variable `attach --addr` reads the server token from,
// so the token need not appear in the process list.
const EnvViewerToken = "TN5250_VIEWER_TOKEN"

// resolveViewerToken picks the token for `attach --addr`: the token file (read once, first
// line), else the environment variable, else the --token flag (kept for compatibility).
func resolveViewerToken(flagToken, file string, getenv func(string) string) (string, error) {
	if file != "" {
		data, err := os.ReadFile(file)
		if err != nil {
			return "", fmt.Errorf("token file: %w", err)
		}
		tok := strings.TrimSpace(strings.SplitN(string(data), "\n", 2)[0])
		if tok == "" {
			return "", fmt.Errorf("token file %s is empty", file)
		}
		return tok, nil
	}
	if v := strings.TrimSpace(getenv(EnvViewerToken)); v != "" {
		return v, nil
	}
	return flagToken, nil
}
