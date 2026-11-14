// Package agent is the agent surface of tn5250-tui: an MCP (Model Context Protocol) server on
// stdio that lets an AI agent drive 5250 sessions headlessly on the same engine and client the
// terminal uses.
//
// The Manager owns the sessions (handles s1, s2, ...). Every tool of the server is a Manager
// method, so another front end (for example a viewer TUI that lets a person watch the agent)
// can attach to a session with Manager.Subscribe and Manager.Snapshot. RunStdio builds the
// Manager and the server and serves them on stdin/stdout; everything the server logs goes to
// stderr, because stdout is the protocol.
package agent
