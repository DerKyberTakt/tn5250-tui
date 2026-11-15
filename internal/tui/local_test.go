package tui

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestLocalCommandPane(t *testing.T) {
	r := newRig(t, nil)
	if r.a.localShown() {
		t.Fatal("pane shown at start")
	}
	if msg := r.a.execute("!"); msg != "usage: !<command>" {
		t.Fatalf("%q", msg)
	}
	if msg := r.a.execute(":!echo local-hello"); msg != "" {
		t.Fatalf("run: %q", msg)
	}
	if !r.a.localShown() {
		t.Fatal("pane not shown by :!")
	}
	waitFor(t, "command output", func() bool { return strings.Contains(r.a.localText(), "local-hello") && !r.a.localRunning() })
	if !strings.HasPrefix(r.a.localText(), "$ echo local-hello") {
		t.Fatalf("pane text %q", r.a.localText())
	}
	if msg := r.a.execute("local off"); msg != "" || r.a.localShown() {
		t.Fatalf("local off: %q shown=%v", msg, r.a.localShown())
	}
	r.a.execute("local")
	if !r.a.localShown() {
		t.Fatal("local toggles the pane back")
	}
	r.a.execute("local clear")
	if r.a.localText() != "" {
		t.Fatalf("after clear %q", r.a.localText())
	}
	if msg := r.a.execute("local x"); msg != "usage: local [on|off|clear]" {
		t.Fatalf("%q", msg)
	}
	// a failing command reports its exit in the pane, not as a notice
	r.a.execute("!exit 3")
	waitFor(t, "exit status", func() bool { return strings.Contains(r.a.localText(), "exit status 3") })
}

func TestShellOutRunsTheShellAndComesBack(t *testing.T) {
	old := shellCommand
	defer func() { shellCommand = old }()
	called := false
	shellCommand = func() *exec.Cmd {
		called = true
		return exec.Command(os.Args[0], "-test.run=TestNothingMatchesThisName")
	}
	r := newRig(t, func(o *AppOptions) { o.Target = "PUB400.COM" })
	sim := simScreen(t, 100, 30)
	r.a.post = func(f func()) { r.a.app.QueueUpdateDraw(f) }
	r.a.SetScreen(sim)
	done := make(chan error, 1)
	go func() { done <- r.a.Run() }()
	waitFor(t, "connected", r.isConnected)
	sim.SetSize(100, 30)
	waitFor(t, "drawn", func() bool { return simHas(sim, "Sign On") })
	var msg string
	finished := make(chan struct{})
	r.a.app.QueueUpdateDraw(func() { msg = r.a.execute("sh"); close(finished) })
	<-finished
	if !called || msg != "" {
		t.Fatalf("called=%v msg=%q", called, msg)
	}
	if !r.isConnected() {
		t.Fatal("the session dropped while the shell ran")
	}
	waitFor(t, "redrawn after the shell", func() bool { return simHas(sim, "Sign On") })
	r.a.app.QueueUpdateDraw(func() { r.a.Quit() })
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
