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
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: tn5250-tui attach [--pid N | --addr A [--token-file F | --token T]] [--list] [session]")
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
		if len(entries) == 0 {
			fmt.Fprintln(os.Stderr, "tn5250-tui attach: no running tn5250-tui mcp server (start one, or give --addr with a token)")
			return 1
		}
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
	if err := v.Run(fs.Arg(0)); err != nil {
		fmt.Fprintln(os.Stderr, "tn5250-tui attach:", viewer.Sanitize(err.Error()))
		return 1
	}
	return 0
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
