# tn5250-tui design (v0.1, 2026-11-14)

A lightweight 5250 terminal for the console, in Go, built from what tn5250-terminal and tn5250-desktop taught us.
Windows 10+ and Linux (and macOS by accident). One static binary, installed with `curl | sh` / `iwr | iex` from
GitHub Releases, later from the AUR. No credential manager: a sites file in plain text, by design.

## Decisions
- **Go** (same language as the desktop's host-server work; one toolchain, static binaries, cross-compiles from one box).
  No C/Rust: nothing here needs them.
- **tcell v2 + tview** for the terminal: explicit key modifiers (Shift+F1..F12 = F13..F24, Ctrl, Alt), Windows console
  and Linux terminals, 256/true colour, mouse. tview supplies the forms and modals (sites, settings, command palette);
  the 5250 screen is a custom primitive.
- **The engine is a port of the TypeScript engine** (tn5250-terminal `src/engine`): telnet (RFC 1205/2877/4777),
  GDS, Write To Display orders, the read commands, save/restore, roll, error codes, the Query reply, fields and the
  keyboard model (field exit, mandatory enter, auto enter, CA keys, operator errors). **No enhanced interface**: the
  Query reply says "plain display", so the host draws windows and menus as text, which is exactly what a TUI wants.
  Code pages come from the same tables (`internal/ebcdic`, generated).
- **Sites file**: `$XDG_CONFIG_HOME/tn5250-tui/sites.json` (Linux), `%APPDATA%\tn5250-tui\sites.json` (Windows).
  Plain JSON, password in clear (kept simple on purpose), file mode 0600 on Linux. A site = name, host, port,
  tls, trustAnyCertificate, user, password, screenSize (24x80 | 27x132), codePage, deviceName, autoSignon.
- **Auto sign-on** = guarded typed sign-on: when the first screen after connect has at least two input fields and the
  second one is non-display, the user and password go into them and Enter is pressed. RFC 4777 password substitutes
  (the desktop's auth.go) come later, if ever.
- **Agent surface comes later** (MCP server, companion VSIX): this round makes the engine, the TUI and the install story.
  The engine keeps no I/O so the MCP layer can drive it headless.

## Packages
```
cmd/tn5250-tui          main: flags, wires client + engine + tui
internal/ebcdic         code pages (generated tables)
internal/tn5250         the engine: telnet.go gds.go attr.go state.go screen.go datastream.go keyboard.go session.go nls.go
internal/client         TCP/TLS connection, reader/writer goroutines, reconnect, trace file
internal/sites          sites.json load/save, default paths
internal/tui            tcell/tview app: screen view, OIA line, command bar, login, site manager, help
internal/fakehost       a TCP 5250 host that replays testdata captures (tests, demos without an IBM i)
testdata/               captures (PUB400 only)
packaging/aur           PKGBUILD for tn5250-tui-bin
install.sh install.ps1  curl | sh and iwr | iex installers from GitHub Releases
.github/workflows       ci.yml (vet, test, build) and release.yml (tag -> binaries, SHA256SUMS, GitHub Release)
```

## Engine API (contract the TUI builds against)
```go
package tn5250

type Options struct {
    ScreenSize   string // "24x80" (IBM-3179-2) or "27x132" (IBM-3477-FC)
    CodePage     int    // default 37
    DeviceName   string
    KeyboardType string
    NoTypeAhead  bool // type-ahead is ON unless set
}

type Events struct {
    Send   func(b []byte, forTrace []byte) // bytes for the socket; forTrace = same frame, secrets blanked (nil = no secret)
    Update func()                           // screen or OIA changed
    Alarm  func()
    Trace  func(line string)
    Error  func(err error)
}

type Session struct { ... }
func New(o Options, ev Events) (*Session, error)
func (s *Session) Feed(b []byte)            // bytes from the socket
func (s *Session) Press(a Action)           // a key action (see Action)
func (s *Session) Type(text string)         // text into the field at the cursor
func (s *Session) SetCursor(row, col int)
func (s *Session) Snapshot() Snapshot
func (s *Session) OIA(connected bool) OIA
func (s *Session) Reset()                   // disconnect: state back to start

type Action string  // "enter" "fieldExit" "fieldPlus" "fieldMinus" "tab" "backtab" "left" "right" "up" "down" "home"
                    // "newline" "backspace" "delete" "insert" "endOfField" "eraseEof" "eraseInput" "dup" "fieldMark"
                    // "reset" "attn" "sysreq" "clear" "help" "pageUp" "pageDown" "print" "recordBackspace" "testRequest"
                    // "fastUp" "fastDown" "fastLeft" "fastRight" "wordLeft" "wordRight" "beginningOfField" "deleteWord"
                    // "eraseField" "pf1".."pf24"

type Cell struct { Ch rune; Attr byte }   // Attr = the 5250 attribute byte in effect (0x20..0x3f); decode with DecodeAttr
type Snapshot struct {
    Rows, Cols int
    Cells      []Cell          // Rows*Cols; attribute bytes themselves are spaces with their own attr
    Fields     []FieldInfo     // Start (offset), Length, Bypass, Nondisplay, MDT
    Cursor     struct{ Row, Col int }
    Seq        uint64          // increases on every change (agents poll on it)
}
type OIA struct {
    Connected      bool
    InputInhibited string // "none" | "system" | "error"
    Insert         bool
    MessageWaiting bool
    DeviceName     string
    Message        string  // operator error or host error line text
    QueuedKeys     int
}
type Attr struct { Color string; Reverse, Underline, Blink, ColumnSeparators, Nondisplay bool }
func DecodeAttr(b byte) Attr
```
Non-display field text never leaves the engine: `Snapshot` blanks it (as the TS engine does).

## TUI
- Full-screen: the 5250 grid centred (24x80 or 27x132; a terminal narrower than the screen scrolls horizontally),
  one OIA line at the bottom (connection, X SYSTEM / X II, insert, MW, device, error text, cursor row/col), and a
  command bar above it that opens on `:` (vim-style) when the cursor is not in a field, or always on Ctrl+K.
- Keys: F1-F12, Shift+F1-F12 = F13-F24, Enter, Tab/Shift+Tab, arrows, Home/End, Insert/Delete/Backspace,
  PgUp/PgDn (roll), Esc = Attn, Ctrl+R = Reset, Ctrl+S = SysReq? (Ctrl+S is XOFF in some terminals: use Shift+Esc or
  `:sysreq`), Ctrl+Q quits? (XON) -> `:q`. Numpad +/- are plain +/- in a terminal: Field Exit = Ctrl+Enter or Shift+Enter
  where the terminal delivers it, else `:fieldexit`; Field+ / Field- via the command bar. Ctrl+K opens the command bar
  anywhere. Keep the mapping in one table so it can be changed.
- Commands (`:`): `connect [site|host[:port]] [--tls]`, `disconnect`, `sites` (manager: list, add, edit, delete, connect),
  `signon` (typed sign-on now), `key <action>` (any Action), `send <text>` (type + Enter), `trace on|off [file]`,
  `dump [file]` (screen text), `size 24x80|27x132`, `cp <ccsid>`, `help`, `q`/`quit`.
- Start-up: no args -> the sites list (or the login form when empty); `tn5250-tui host[:port]` connects straight away;
  `--site NAME`; `--user/--password` for one-off sign-on; `--trace FILE`.
- Login form: Host, Port, TLS, User, Password (shown as typed, with a toggle to mask), Save as site [name].
- Colours: the ACS palette on a black background; reverse, underline and blink honoured; non-display fields show
  nothing; the cursor is the terminal's cursor.

## Install
- `curl -fsSL https://raw.githubusercontent.com/DerKyberTakt/tn5250-tui/main/install.sh | sh` -> downloads the latest
  release asset for the OS/arch into `~/.local/bin` (or `/usr/local/bin` with sudo), verifies SHA256SUMS.
- `iwr -useb https://raw.githubusercontent.com/DerKyberTakt/tn5250-tui/main/install.ps1 | iex` -> `%LOCALAPPDATA%\Programs\tn5250-tui`, adds to the user PATH.
- AUR: `packaging/aur/PKGBUILD` for `tn5250-tui-bin` (published under the maintainer's AUR account).
- Release assets: `tn5250-tui_<ver>_linux_amd64.tar.gz`, `_linux_arm64.tar.gz`, `_windows_amd64.zip`, `_windows_arm64.zip`,
  `_darwin_arm64.tar.gz`, `SHA256SUMS`.

## Out of scope this round
Enhanced interface (GUI windows, scroll bars, selection fields), printers, macros, credential stores, MCP server and the
companion VSIX (next phase: the engine's Snapshot/Seq + a JSON-RPC or MCP front end).
