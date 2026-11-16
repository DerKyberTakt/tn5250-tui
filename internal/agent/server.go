package agent

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const instructions = `Drive IBM i 5250 terminal sessions. Call connect first (a saved site, or host plus user and password); it returns a session handle and the first screen. Then press, type and fill return the screen after the host has answered. Rows, columns and field numbers are 1-based; the line number in text is the row number. Prefer fill with labels over typing into field numbers, and read the "settled" and "oia" parts of every answer: oia.inhibited "error" means press reset. Disconnect when done.`

// toolNames is the order the tools are registered in.
var toolNames = []string{"connect", "screen", "press", "type", "fill", "wait", "find", "sessions", "disconnect", "trace", "dump"}

func ptr[T any](v T) *T { return &v }

// readOnlyTool and keyTool are the annotations of the two kinds of tool.
func readOnlyTool(title string) *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{Title: title, ReadOnlyHint: true, OpenWorldHint: ptr(false)}
}

func keyTool(title string) *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{Title: title, DestructiveHint: ptr(true), OpenWorldHint: ptr(true)}
}

// NewServer builds the MCP server with the eleven tools on top of the manager.
func NewServer(m *Manager) *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{Name: ServerName, Title: "5250 terminal for agents", Version: Version},
		&mcp.ServerOptions{
			Instructions: instructions,
			Logger:       slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
		})

	mcp.AddTool(srv, &mcp.Tool{
		Name: "connect",
		Description: "Open a 5250 session and wait for the first screen. Uses a saved site (site) or host/port/tls; " +
			"explicit arguments override the site. With a user and password (given or saved) it also types the sign-on " +
			"(refused unless the second input field is non-display) and returns the screen after it. The password is never echoed.",
		Annotations: keyTool("Connect"),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in ConnectArgs) (*mcp.CallToolResult, ConnectOut, error) {
		out, err := m.Connect(ctx, in)
		return nil, out, err
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name: "screen",
		Description: "Read the current screen: text (rows with trailing blanks trimmed; line n is row n), 1-based cursor, " +
			"the field table (index, row, col, length, label, bypass, nondisplay, mdt, text), the status line (oia), seq and a signature. " +
			"Does not touch the host. rows narrows the text; fields_only drops it.",
		Annotations: readOnlyTool("Read screen"),
	}, func(_ context.Context, _ *mcp.CallToolRequest, in ScreenArgs) (*mcp.CallToolResult, ScreenOut, error) {
		out, err := m.Screen(in)
		return nil, out, err
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name: "press",
		Description: "Press a key (" + KeyList() + "). Keys that send to the host (enter, f-keys, page keys, attn, sysreq, clear, help) " +
			"wait until the host answers and the keyboard unlocks; the result carries settled=true|false. " +
			"Tab, home, reset and other local keys return at once. times repeats the key.",
		Annotations: keyTool("Press key"),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in PressArgs) (*mcp.CallToolResult, ScreenOut, error) {
		out, err := m.Press(ctx, in)
		return nil, out, err
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name: "type",
		Description: "Type text into a field (field = 1-based index or a label; default: where the cursor is). With field given, the field is " +
			"erased first unless clear=false. then = enter, tab or fieldexit presses that key afterwards and waits for the host. " +
			"Typing into a protected area is an error that lists the input fields.",
		Annotations: keyTool("Type text"),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in TypeArgs) (*mcp.CallToolResult, ScreenOut, error) {
		out, err := m.Type(ctx, in)
		return nil, out, err
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name: "fill",
		Description: "Fill several fields in one call. Each item names its field by index, by row and col, or by label (the text just left " +
			"of the field, e.g. \"Password\"); an unknown or ambiguous label is an error that lists the candidates. Nothing is typed unless " +
			"every field resolves. then = enter, tab or fieldexit presses that key afterwards and waits for the host.",
		Annotations: keyTool("Fill fields"),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in FillArgs) (*mcp.CallToolResult, ScreenOut, error) {
		out, err := m.Fill(ctx, in)
		return nil, out, err
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name: "wait",
		Description: "Wait for a condition: until=unlock (keyboard open and screen quiet), text (substring appears, case-insensitive), " +
			"change (the screen changes after seq) or cursor (row,col). Returns matched=false and the screen when the time runs out.",
		Annotations: readOnlyTool("Wait"),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in WaitArgs) (*mcp.CallToolResult, WaitOut, error) {
		out, err := m.Wait(ctx, in)
		return nil, out, err
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "find",
		Description: "Find text on the current screen: every hit with its 1-based row and col and the whole line.",
		Annotations: readOnlyTool("Find text"),
	}, func(_ context.Context, _ *mcp.CallToolRequest, in FindArgs) (*mcp.CallToolResult, FindOut, error) {
		out, err := m.Find(in)
		return nil, out, err
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "sessions",
		Description: "List the open sessions (handle, site or host, device, connected, since, seq, inhibited) and whether read-only mode is on.",
		Annotations: readOnlyTool("List sessions"),
	}, func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, SessionsOut, error) {
		return nil, m.Sessions(), nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name: "disconnect",
		Description: "End a session. signoff=true first types SIGNOFF at a command line, but only when the screen has one " +
			"(a field labelled \"===>\" or \"Selection or command\"); otherwise the connection is simply closed.",
		Annotations: keyTool("Disconnect"),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in DisconnectArgs) (*mcp.CallToolResult, DisconnectOut, error) {
		out, err := m.Disconnect(ctx, in)
		return nil, out, err
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "trace",
		Description: "The last trace lines of a session (data stream hex, engine commands), or with from_start its first lines (the telnet negotiation). Data typed into hidden fields is masked.",
		Annotations: readOnlyTool("Trace"),
	}, func(_ context.Context, _ *mcp.CallToolRequest, in TraceArgs) (*mcp.CallToolResult, TraceOut, error) {
		out, err := m.Trace(in)
		return nil, out, err
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "dump",
		Description: "Debugging view: the whole screen, then the attribute byte of every cell as hex, one line per row.",
		Annotations: readOnlyTool("Dump screen"),
	}, func(_ context.Context, _ *mcp.CallToolRequest, in DumpArgs) (*mcp.CallToolResult, DumpOut, error) {
		out, err := m.Dump(in)
		return nil, out, err
	})
	return srv
}

// RunStdio builds the manager and the server and serves them on stdin/stdout until the client
// disconnects or ctx ends. Everything it logs goes to stderr; stdout belongs to the protocol.
func RunStdio(ctx context.Context, cfg Config) error {
	m, err := NewManager(cfg)
	if err != nil {
		return fmt.Errorf("agent: %w", err)
	}
	defer m.Close()
	return NewServer(m).Run(ctx, &mcp.StdioTransport{})
}
