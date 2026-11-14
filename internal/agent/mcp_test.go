package agent

import (
	"context"
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// startMCP serves a manager on one end of an in-memory transport and returns a connected client.
func startMCP(t *testing.T, cfg Config) (*mcp.ClientSession, *Manager) {
	t.Helper()
	m, _ := newTestManager(t, cfg)
	srv := NewServer(m)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	st, ct := mcp.NewInMemoryTransports()
	ss, err := srv.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ss.Close() })
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0"}, nil)
	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs, m
}

func call(t *testing.T, cs *mcp.ClientSession, name string, args any) *mcp.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return res
}

// decode reads a tool's structured result into v.
func decode(t *testing.T, res *mcp.CallToolResult, v any) {
	t.Helper()
	if res.IsError {
		t.Fatalf("tool error: %s", contentText(res))
	}
	b, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatalf("%v in %s", err, b)
	}
}

func contentText(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

func TestToolsList(t *testing.T) {
	cs, _ := startMCP(t, Config{})
	list := func() []*mcp.Tool {
		res, err := cs.ListTools(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		return res.Tools
	}
	tools := list()
	var names []string
	byName := map[string]*mcp.Tool{}
	for _, tl := range tools {
		names = append(names, tl.Name)
		byName[tl.Name] = tl
	}
	want := []string{"connect", "screen", "press", "type", "fill", "wait", "find", "sessions", "disconnect", "trace", "dump"}
	if len(names) != len(want) {
		t.Fatalf("want %d tools, got %v", len(want), names)
	}
	sorted := slices.Clone(want)
	slices.Sort(sorted)
	got := slices.Clone(names)
	slices.Sort(got)
	if !slices.Equal(got, sorted) {
		t.Fatalf("tools: %v", names)
	}
	if !slices.Equal(toolNames, want) {
		t.Errorf("toolNames out of date: %v", toolNames)
	}
	// the order is deterministic from call to call
	for i := 0; i < 3; i++ {
		var again []string
		for _, tl := range list() {
			again = append(again, tl.Name)
		}
		if !slices.Equal(again, names) {
			t.Fatalf("tools/list order changed: %v vs %v", again, names)
		}
	}
	for _, n := range []string{"screen", "wait", "find", "sessions", "trace", "dump"} {
		if a := byName[n].Annotations; a == nil || !a.ReadOnlyHint {
			t.Errorf("%s must be annotated read-only", n)
		}
	}
	for _, n := range []string{"connect", "press", "type", "fill", "disconnect"} {
		a := byName[n].Annotations
		if a == nil || a.ReadOnlyHint || a.DestructiveHint == nil || !*a.DestructiveHint {
			t.Errorf("%s must be annotated destructive", n)
		}
	}
	for _, tl := range tools {
		if tl.Description == "" || tl.InputSchema == nil || tl.OutputSchema == nil {
			t.Errorf("%s: description and schemas are required", tl.Name)
		}
	}
	// the type tool takes a number or a string for field
	schema, _ := json.Marshal(byName["type"].InputSchema)
	if !strings.Contains(string(schema), `"field"`) {
		t.Errorf("type schema: %s", schema)
	}
}

func TestMCPConnectAndScreen(t *testing.T) {
	host, port := startFake(t)
	cs, _ := startMCP(t, Config{})

	var conn ConnectOut
	decode(t, call(t, cs, "connect", map[string]any{"host": host, "port": port, "screen_size": "27x132", "signon": false, "timeout_ms": 10000}), &conn)
	if conn.Session != "s1" || !strings.Contains(conn.Screen.Text, "Welcome to PUB400.COM") {
		t.Fatalf("connect: %+v", conn)
	}

	var scr ScreenOut
	decode(t, call(t, cs, "screen", map[string]any{"session": "s1"}), &scr)
	if !strings.Contains(scr.Text, "Your user name:") || scr.Signature == "" || scr.Cursor.Row < 1 {
		t.Fatalf("screen: %+v", scr)
	}
	var fo ScreenOut
	decode(t, call(t, cs, "screen", map[string]any{"session": "s1", "rows": "1-2", "fields_only": true}), &fo)
	scr = fo
	if scr.Text != "" || len(scr.Fields) != 0 {
		t.Errorf("fields_only with rows 1-2: %+v", scr)
	}

	// field as a number, then a label, through the protocol
	decode(t, call(t, cs, "type", map[string]any{"session": "s1", "text": "alex", "field": 1}), &scr)
	if !strings.Contains(strings.ToUpper(scr.Text), "ALEX") {
		t.Errorf("type by number:\n%s", scr.Text)
	}
	decode(t, call(t, cs, "type", map[string]any{"session": "s1", "text": "bob", "field": "your user name"}), &scr)
	if !strings.Contains(strings.ToUpper(scr.Text), "BOB") || strings.Contains(strings.ToUpper(scr.Text), "ALEX") {
		t.Errorf("type by label replaces the field:\n%s", scr.Text)
	}
	var fl ScreenOut
	decode(t, call(t, cs, "fill", map[string]any{"session": "s1", "fields": []map[string]any{{"label": "user", "text": "carol"}}, "then": "enter", "timeout_ms": 600}), &fl)
	if !strings.Contains(fl.Text, "FAKE HOST  Enter received") || fl.Settled == nil {
		t.Errorf("fill then enter:\n%s", fl.Text)
	}

	var ss SessionsOut
	decode(t, call(t, cs, "sessions", map[string]any{}), &ss)
	if len(ss.Sessions) != 1 || ss.ReadOnly {
		t.Errorf("sessions: %+v", ss)
	}
	var fr FindOut
	decode(t, call(t, cs, "find", map[string]any{"session": "s1", "text": "enter received"}), &fr)
	if len(fr.Hits) != 1 || fr.Hits[0].Row != 1 || fr.Hits[0].Col != 12 {
		t.Errorf("find: %+v", fr)
	}
	var tr TraceOut
	decode(t, call(t, cs, "trace", map[string]any{"session": "s1", "lines": 3}), &tr)
	if len(tr.Lines) == 0 || len(tr.Lines) > 3 {
		t.Errorf("trace: %+v", tr)
	}
	var wo WaitOut
	decode(t, call(t, cs, "wait", map[string]any{"session": "s1", "until": "text", "text": "fake host", "timeout_ms": 1000}), &wo)
	if !wo.Matched {
		t.Error("wait text")
	}
	var du DumpOut
	decode(t, call(t, cs, "dump", map[string]any{"session": "s1"}), &du)
	if !strings.Contains(du.Dump, "FAKE HOST") {
		t.Error("dump")
	}
	var dc DisconnectOut
	decode(t, call(t, cs, "disconnect", map[string]any{"session": "s1"}), &dc)
	if !dc.Closed {
		t.Error("disconnect")
	}
}

func TestMCPErrorsAreToolErrors(t *testing.T) {
	cs, _ := startMCP(t, Config{ReadOnly: true})
	res := call(t, cs, "screen", map[string]any{"session": "s7"})
	if !res.IsError || !strings.Contains(contentText(res), "no session") {
		t.Errorf("unknown session must be a tool error: %v %q", res.IsError, contentText(res))
	}
	res = call(t, cs, "press", map[string]any{"session": "s7", "key": "enter"})
	if !res.IsError {
		t.Error("press on an unknown session must be a tool error")
	}
	// unknown arguments are rejected by the schema
	if _, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "screen", Arguments: map[string]any{"session": "s1", "bogus": 1}}); err == nil {
		t.Log("unknown argument accepted as a tool error")
	}
	var ss SessionsOut
	decode(t, call(t, cs, "sessions", map[string]any{}), &ss)
	if !ss.ReadOnly || ss.Note == "" || ss.Sessions == nil {
		t.Errorf("sessions in read-only mode: %+v", ss)
	}
}

func TestMCPReadOnlyBlocksGuardedVerb(t *testing.T) {
	host, port := startFake(t)
	cs, _ := startMCP(t, Config{ReadOnly: true})
	var conn ConnectOut
	decode(t, call(t, cs, "connect", map[string]any{"host": host, "port": port, "signon": false, "timeout_ms": 10000}), &conn)
	res := call(t, cs, "type", map[string]any{"session": conn.Session, "text": "DLTF X", "field": 1})
	if !res.IsError || !strings.Contains(contentText(res), "read-only mode: DLT is blocked; ask the user to run it") {
		t.Errorf("guarded verb: %v %q", res.IsError, contentText(res))
	}
}

// TestRunStdio runs RunStdio on pipes standing in for stdin and stdout: the client sees only
// protocol, and the server ends when the client goes away.
func TestRunStdio(t *testing.T) {
	inR, inW, _ := os.Pipe()
	outR, outW, _ := os.Pipe()
	oldIn, oldOut := os.Stdin, os.Stdout
	os.Stdin, os.Stdout = inR, outW
	defer func() { os.Stdin, os.Stdout = oldIn, oldOut }()

	var audit lockedBuf
	done := make(chan error, 1)
	go func() { done <- RunStdio(context.Background(), Config{AuditWriter: &audit}) }()

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0"}, nil)
	cs, err := client.Connect(context.Background(), &mcp.IOTransport{Reader: outR, Writer: inW}, nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil || len(res.Tools) != 11 {
		t.Fatalf("tools over stdio: %v %v", res, err)
	}
	_ = cs.Close()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("RunStdio did not end when the client closed")
	}
}
