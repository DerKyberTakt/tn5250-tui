# Changelog

## 0.1.0 - unreleased
- Split view: `:split [n]` / Ctrl+\ shows a second tab beside the active one (divider, each pane half the width, the other pane's tab underlined in the bar); `:split` again or Ctrl+\ moves the keyboard to the other pane, `:unsplit` goes back to one pane.
- Passwords leave the sites file: OS secret store (Credential Manager, Keychain, Secret Service) with the marker `keyring:`, or AES-256-GCM under a 0600 key file where there is no store (`enc:`); clear-text entries are moved on the next load; `TN5250_TUI_SECRETS=keyring|file|plain`. The site form masks the password unless "Show password" is ticked.
- Several sessions at once, as tabs: a tab bar above the screen once there are two (active in reverse video, dropped dimmed, connecting with `...`); `:connect` on a connected tab opens a new one, `:new`, `:tab <n>|next|prev`, `:close`; Ctrl+T, Ctrl+W (asks when connected), Ctrl+PgUp/PgDn, Alt+1-9. Background tabs keep their sessions fed and finish their sign-on.
- `tn5250-tui mcp`: an MCP server (stdio, official Go SDK) that hosts 5250 sessions for an AI agent: connect, screen, press, type, fill, wait, find, sessions, disconnect, trace, dump; settle logic (keyboard unlocked + 120 ms quiet, 15 s cap), `--read-only` command guard, `--audit` JSON lines, passwords never logged. `tn5250-tui attach` watches those sessions live (read-only) through a loopback listener with a per-process token file. Skill for Claude Code in `skill/tn5250-agent`.
- `:import` / `tn5250-tui import`: the tn5250-terminal VS Code extension's settings.json (VS Code, Insiders, VSCodium, Cursor) becomes sites and defaults: sessions, screen size, code page, device name, separators, auto sign-on, the seven attribute colours, the key overrides a console can deliver, the text watermark; demo mode marks a site (tiled label + DEMO on the status line). Passwords stay in VS Code; existing sites keep theirs. sites.json gains `defaults.colors`, `defaults.keys`, `defaults.watermark` and `demo`/`demoLabel` on a site (older builds ignore them).
- Ctrl+K, Ctrl+X, Ctrl+R, Ctrl+E, Ctrl+A, Ctrl+Q, Ctrl+J and Ctrl+Backspace work: tcell reports a Ctrl chord with the modifier set and the key table matched the bare code; Ctrl+Enter on Windows arrives as Ctrl+M and is Field Exit too.
- SEU edit and browse screens are coloured by language (CL, DDS, RPG fixed and free, SQL incl. embedded) as the emulators do, from the same rules; off with the "SEU syntax colouring" setting.
- :settings opens a small settings page (screen size, code page, device name, separators, SEU colouring, auto sign-on, trace file, keep-alive) saved as defaults in sites.json.
- Shift+Esc sends System Request, as in the emulators (Ctrl+X stays).
- New sites and host targets default to the 27x132 screen (IBM-3477-FC); `:size 24x80` or the site setting gives the small screen.
- Screen: a null cell outside any field shows nothing, and the attribute position is a plain blank; the sign-on screen no longer has underlines running from the end of each field to the right edge (the rule the emulator learned on its first day).

- Engine: telnet (RFC 1205/2877/4777), GDS records, Write To Display orders, read commands, fields and the keyboard model, ported from the tn5250-terminal engine (plain display, no enhanced interface).
- TUI: full-screen 5250 grid on tcell/tview with an OIA line, a `:` command bar, Shift+F1-F12 as F13-F24, login form and site manager.
- Sites: plain-text `sites.json` per user (password in clear, by design).
- Client: TCP/TLS connection with trace file support.
- Test host: `internal/fakehost` and `cmd/fakehost` replay a PUB400 capture so the TUI runs without an IBM i.
- Install: `install.sh` (Linux, macOS), `install.ps1` (Windows), AUR `tn5250-tui-bin` PKGBUILD.
- CI and releases: GitHub Actions for vet/test/build and tagged releases with SHA256SUMS.
- Device name in use: when the host closes the connection within 8 s of connecting without ever sending a screen, the terminal reconnects with the next workstation name (`*` = a letter A-Z, `=` = a digit 0-9 per attempt, a plain name gets a suffix 1-9; at most 10 retries) and then says "workstation NAME in use?" on the OIA line.
- Sign-on: when the host shows the sign-on screen again after a typed or automatic sign-on whose password held characters that differ between code pages (! @ # $ [ ] { } | \ ^ ~ and accented letters), a one-time notice names those characters and the code page in use (`internal/tn5250/variant.go`, ported from the TypeScript engine); the password is never shown or logged.
- Screen: a Dup (X'1C') shows as `*` and a Field Mark (X'1E') as `;`, as the overscored glyphs of a real display, instead of the hollow box.
- STRPCCMD / PC Organizer: the engine detects the request (`internal/tn5250/pco.go`); the terminal presses Enter at once so the host program resumes and shows "PC command not run here: <command>" (a command from the host is never run).
- Client: TCP keep-alive probes every 15 s (was 30 s), as the TypeScript client.
- Engine tests: Read Screen and Read Screen Extended answers (also to Print, also at 27x132), the Query reply beside a Save Screen at 27x132, and the Print key flow (AID F6, then Read Screen to Print).
- Engine: type-ahead is on by default, as in the TypeScript engine; `Options.NoTypeAhead` turns it off (replaces `Options.TypeAhead`).
