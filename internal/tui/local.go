package tui

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"

	"github.com/rivo/tview"
)

// Local command line. `:!<command>` runs a command on this machine (not the host) and shows
// its output in a pane at the bottom of the terminal, under the 5250 screen; `:sh` suspends
// the terminal and drops into your shell in the same console, back to the terminal when the
// shell exits. The pane is a plain text view: `:local` shows or hides it, `:local clear`
// empties it. Nothing here goes near the 5250 session.

// localRows is the height of the output pane when it is shown.
const localRows = 8

// shellCommand returns the interactive shell for `:sh`: $TN5250_TUI_SHELL, else $SHELL
// (Unix) or %COMSPEC% (Windows), else sh / cmd. Tests replace it.
var shellCommand = func() *exec.Cmd {
	if s := os.Getenv("TN5250_TUI_SHELL"); s != "" {
		return exec.Command(s)
	}
	if runtime.GOOS == "windows" {
		if s := os.Getenv("COMSPEC"); s != "" {
			return exec.Command(s)
		}
		return exec.Command("cmd")
	}
	if s := os.Getenv("SHELL"); s != "" {
		return exec.Command(s)
	}
	return exec.Command("sh")
}

// localCommand returns the command that runs one `:!` line through the shell.
func localCommand(line string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		shell := os.Getenv("COMSPEC")
		if shell == "" {
			shell = "cmd"
		}
		return exec.Command(shell, "/c", line)
	}
	return exec.Command("sh", "-c", line)
}

// localPane is the output view plus the state of the command running into it.
type localPane struct {
	view    *tview.TextView
	vmu     sync.Mutex // the view's text is written and read under it (the view itself is not safe for that)
	mu      sync.Mutex
	running *exec.Cmd
	shown   bool
}

func newLocalPane() *localPane {
	v := tview.NewTextView().SetScrollable(true).SetWrap(false)
	v.SetBackgroundColor(oiaBg)
	v.SetTextColor(oiaFg)
	v.SetBorder(false)
	scrollbar(v.Box, false, textRows(v, false))
	return &localPane{view: v}
}

// showLocal shows or hides the output pane (UI goroutine).
func (a *App) showLocal(on bool) {
	a.local.mu.Lock()
	a.local.shown = on
	a.local.mu.Unlock()
	h := 0
	if on {
		h = localRows
	}
	a.flex.ResizeItem(a.local.view, h, 0)
}

// localShown reports whether the output pane is on screen.
func (a *App) localShown() bool {
	a.local.mu.Lock()
	defer a.local.mu.Unlock()
	return a.local.shown
}

// localText is the pane's current text (tests and :dump).
func (a *App) localText() string {
	a.local.vmu.Lock()
	defer a.local.vmu.Unlock()
	return a.local.view.GetText(true)
}

// localAppend adds one line to the pane and scrolls to it.
func (a *App) localAppend(line string) {
	a.local.vmu.Lock()
	defer a.local.vmu.Unlock()
	fmt.Fprintln(a.local.view, line)
	a.local.view.ScrollToEnd()
}

// runLocal is `:!<line>`: the command runs in the background and its output streams into
// the pane; one command at a time.
func (a *App) runLocal(line string) string {
	line = strings.TrimSpace(line)
	if line == "" {
		return "usage: !<command>"
	}
	a.local.mu.Lock()
	if a.local.running != nil {
		a.local.mu.Unlock()
		return "a local command is still running"
	}
	cmd := localCommand(line)
	a.local.running = cmd
	a.local.mu.Unlock()
	a.showLocal(true)
	a.localAppend("$ " + line)
	out, err := cmd.StdoutPipe()
	if err != nil {
		a.localDone(err)
		return err.Error()
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		a.localDone(err)
		return err.Error()
	}
	go func() {
		a.copyLocal(out)
		a.localDone(cmd.Wait())
	}()
	return ""
}

// copyLocal streams r into the pane line by line (UI goroutine for the writes).
func (a *App) copyLocal(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		text := sc.Text()
		a.post(func() {
			a.localAppend(tview.Escape(text))
		})
	}
}

// localDone records the command's end in the pane.
func (a *App) localDone(err error) {
	a.local.mu.Lock()
	a.local.running = nil
	a.local.mu.Unlock()
	if err == nil {
		return
	}
	a.post(func() {
		a.localAppend(tview.Escape("[" + err.Error() + "]"))
	})
}

// localRunning reports whether a `:!` command is still going.
func (a *App) localRunning() bool {
	a.local.mu.Lock()
	defer a.local.mu.Unlock()
	return a.local.running != nil
}

// localCmd is `:local [on|off|clear]`.
func (a *App) localCmd(arg string) string {
	switch strings.ToLower(arg) {
	case "":
		a.showLocal(!a.localShown())
	case "on", "show":
		a.showLocal(true)
	case "off", "hide":
		a.showLocal(false)
	case "clear":
		a.local.vmu.Lock()
		a.local.view.Clear()
		a.local.vmu.Unlock()
	default:
		return "usage: local [on|off|clear]"
	}
	return ""
}

// shellOut is `:sh`: the terminal gives the console back, the shell runs in it, and the
// terminal redraws when the shell exits. The 5250 sessions stay connected meanwhile.
func (a *App) shellOut() string {
	cmd := shellCommand()
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	var runErr error
	ok := a.app.Suspend(func() {
		fmt.Fprintln(os.Stdout, "tn5250-tui: type exit to return to the terminal; the sessions stay connected")
		runErr = cmd.Run()
	})
	if !ok {
		return "cannot suspend the terminal right now"
	}
	if runErr != nil {
		return "shell: " + runErr.Error()
	}
	return ""
}
