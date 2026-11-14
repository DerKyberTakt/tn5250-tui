// Package tn5250 is a 5250 display emulation engine: telnet framing (RFC 1205, 2877, 4777),
// GDS records, Write To Display orders, the read and save/restore commands, fields and the
// keyboard model (field exit, mandatory enter, auto enter, command attention keys, operator
// errors).
//
// It is a port of the TypeScript engine of tn5250-terminal and has no I/O: bytes from the
// socket go in through Session.Feed, bytes for the socket come out through Events.Send. The
// engine answers the host's Query as a plain 5250 display (no enhanced interface), so the host
// draws windows and menus as text.
//
// A Session is safe for use from several goroutines. Callbacks in Events run after the
// session's lock is released, in the order the engine produced them, so a callback may call
// back into the Session (for example Update calling Snapshot).
package tn5250
