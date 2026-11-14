package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/DerKyberTakt/tn5250-tui/internal/fakehost"
	"github.com/DerKyberTakt/tn5250-tui/internal/viewer"
)

// TestMCPAttachEndToEnd builds the binary, starts `tn5250-tui mcp` on pipes, speaks MCP to it
// by hand (initialize, connect to the fake host, screen), and checks that `tn5250-tui attach
// --list` sees the server through its token file and that a viewer client gets the frame.
func TestMCPAttachEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary")
	}
	dir := t.TempDir()
	exe := filepath.Join(dir, "tn5250-tui")
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	build := exec.Command("go", "build", "-o", exe, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	t.Setenv("TN5250_TUI_CONFIG", filepath.Join(dir, "sites.json"))

	capture, err := fakehost.LoadCapture("../../testdata/pub400-signon-3477fc.json")
	if err != nil {
		t.Fatal(err)
	}
	host, err := fakehost.StartWith("127.0.0.1:0", capture, fakehost.Options{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close()
	hostName, hostPort, _ := parseHostPort(host.Addr())

	srv := exec.Command(exe, "mcp", "--label", "e2e")
	srv.Env = append(os.Environ(), "TN5250_TUI_CONFIG="+filepath.Join(dir, "sites.json"))
	stdin, _ := srv.StdinPipe()
	stdout, _ := srv.StdoutPipe()
	srv.Stderr = os.Stderr
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stdin.Close(); _ = srv.Process.Kill(); _ = srv.Wait() }()
	rd := bufio.NewReader(stdout)
	id := 0
	call := func(method string, params any) map[string]any {
		id++
		req := map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}
		b, _ := json.Marshal(req)
		if _, err := stdin.Write(append(b, '\n')); err != nil {
			t.Fatalf("write %s: %v", method, err)
		}
		for {
			line, err := rd.ReadBytes('\n')
			if err != nil {
				t.Fatalf("read after %s: %v", method, err)
			}
			var m map[string]any
			if json.Unmarshal(line, &m) != nil || m["id"] == nil {
				continue // a notification
			}
			if e, ok := m["error"]; ok {
				t.Fatalf("%s: %v", method, e)
			}
			r, _ := m["result"].(map[string]any)
			return r
		}
	}
	notify := func(method string) {
		b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": method})
		_, _ = stdin.Write(append(b, '\n'))
	}
	init := call("initialize", map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "e2e", "version": "0"}})
	if si, _ := init["serverInfo"].(map[string]any); si["name"] != "tn5250-tui" {
		t.Fatalf("initialize: %v", init)
	}
	notify("notifications/initialized")
	tools := call("tools/list", map[string]any{})
	if list, _ := tools["tools"].([]any); len(list) != 11 {
		t.Fatalf("tools: %d", len(list))
	}
	res := call("tools/call", map[string]any{"name": "connect", "arguments": map[string]any{"host": hostName, "port": hostPort, "screen_size": "27x132", "signon": false}})
	text := fmt.Sprint(res["content"])
	if res["isError"] == true || !strings.Contains(text, "s1") {
		t.Fatalf("connect: %v", res)
	}
	res = call("tools/call", map[string]any{"name": "screen", "arguments": map[string]any{"session": "s1", "rows": "1-3"}})
	if sc, _ := res["structuredContent"].(map[string]any); !strings.Contains(fmt.Sprint(sc["text"]), "PUB400") {
		t.Fatalf("screen: %v", res)
	}

	// The token file points a viewer at the server.
	entries, err := viewer.ListTokens(true)
	if err != nil || len(entries) != 1 || entries[0].Label != "e2e" || entries[0].PID != srv.Process.Pid {
		t.Fatalf("tokens: %v %+v", err, entries)
	}
	cl, err := viewer.Dial(entries[0].Addr, entries[0].Token, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	frames, stop, _ := cl.Watch("s1")
	fr := <-frames
	stop()
	cl.Close()
	if fr.Rows != 24 || !strings.Contains(strings.Join(fr.Text, "\n"), "PUB400") {
		t.Fatalf("frame: rows %d", fr.Rows)
	}
	list := exec.Command(exe, "attach", "--list")
	list.Env = srv.Env
	out, err := list.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "e2e") || !strings.Contains(string(out), "s1") {
		t.Fatalf("attach --list: %v\n%s", err, out)
	}
	call("tools/call", map[string]any{"name": "disconnect", "arguments": map[string]any{"session": "s1"}})
	_ = stdin.Close()
	_ = srv.Wait()
	if rest, _ := viewer.ListTokens(false); len(rest) != 0 {
		t.Errorf("token file not removed on exit: %+v", rest)
	}
}
