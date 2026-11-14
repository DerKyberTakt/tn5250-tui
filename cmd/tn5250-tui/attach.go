package main

import (
	"flag"
	"fmt"
	"os"
	"strconv"
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
	token := fs.String("token", "", "server token for --addr")
	list := fs.Bool("list", false, "list the running servers and their sessions, then exit")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: tn5250-tui attach [--pid N | --addr A --token T] [--list] [session]")
		fs.PrintDefaults()
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
		entries = []viewer.TokenFile{{Addr: *addr, Token: *token, Label: "--addr"}}
	} else {
		var err error
		entries, err = viewer.ListTokens(true)
		if err != nil {
			fmt.Fprintln(os.Stderr, "tn5250-tui attach:", err)
			return 1
		}
		if len(entries) == 0 {
			fmt.Fprintln(os.Stderr, "tn5250-tui attach: no running tn5250-tui mcp server (start one, or give --addr/--token)")
			return 1
		}
	}
	if *list {
		for _, e := range entries {
			fmt.Println(viewer.Describe(e))
			cl, err := viewer.Dial(e.Addr, e.Token, 2*time.Second)
			if err != nil {
				fmt.Println("   ", err)
				continue
			}
			ss, _ := cl.Sessions()
			cl.Close()
			for _, s := range ss {
				state := "disconnected"
				if s.Connected {
					state = "connected"
				}
				fmt.Printf("    %-4s %-30s %-12s %s seq %d %s\n", s.Session, s.Host, s.Device, state, s.Seq, s.Inhibited)
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
		fmt.Fprintln(os.Stderr, "tn5250-tui attach:", err)
		return 1
	}
	defer cl.Close()
	label := entry.Label
	if label == "" {
		label = "pid " + strconv.Itoa(entry.PID)
	}
	v := viewer.New(cl, label)
	if err := v.Run(fs.Arg(0)); err != nil {
		fmt.Fprintln(os.Stderr, "tn5250-tui attach:", err)
		return 1
	}
	return 0
}
