# tn5250-tui

A lightweight 5250 terminal for the console. One static binary for Windows 10+ and Linux (macOS builds by accident), built on the engine of the tn5250-terminal project: telnet 5250 (RFC 1205/2877/4777), plain display (no enhanced interface, so the host draws windows and menus as text), code pages from generated tables, and a full keyboard model.

> Status: early. Version 0.1.0 is not released yet; keys, commands and file formats may still change.

## Install

Linux and macOS (arm64):

```sh
curl -fsSL https://raw.githubusercontent.com/DerKyberTakt/tn5250-tui/main/install.sh | sh
```

Installs into `~/.local/bin` (or `/usr/local/bin` as root, or `$PREFIX/bin`), after verifying `SHA256SUMS`. Pin a version with `TN5250_TUI_VERSION=v0.1.0`.

Windows (PowerShell 5.1 or newer):

```powershell
iwr -useb https://raw.githubusercontent.com/DerKyberTakt/tn5250-tui/main/install.ps1 | iex
```

Installs into `%LOCALAPPDATA%\Programs\tn5250-tui` and adds that folder to your user PATH.

Arch Linux (AUR):

```sh
yay -S tn5250-tui-bin
```

Release assets: `tn5250-tui_<ver>_linux_amd64.tar.gz`, `_linux_arm64.tar.gz`, `_windows_amd64.zip`, `_windows_arm64.zip`, `_darwin_arm64.tar.gz` and `SHA256SUMS`.

## First run

```
tn5250-tui                    # sites list (or the login form when there are none)
tn5250-tui pub400.com         # connect straight away (host[:port])
tn5250-tui --site NAME        # a saved site
tn5250-tui --trace FILE host  # write a protocol trace
```

Sites live in a JSON file:

| OS | Path |
| --- | --- |
| Linux | `$XDG_CONFIG_HOME/tn5250-tui/sites.json` (default `~/.config/tn5250-tui/sites.json`) |
| Windows | `%APPDATA%\tn5250-tui\sites.json` |

Passwords are not in that file. They go to the operating system's secret store (Windows Credential Manager, macOS Keychain, Linux Secret Service such as GNOME Keyring or KWallet) under the service `tn5250-tui`, and the file carries only `"password": "keyring:"`. On a Linux box without a secret store (headless, a container, an SSH session without D-Bus) the password is encrypted with AES-256-GCM under a random key in `secret.key` next to the sites file (mode 0600; a key file other users can read is refused) and stored as `"enc:..."`. That is protection against copies, backups and sync of the sites file, not a boundary: whoever runs as you can read the key. A clear-text password in the file (a 0.1.0 file, or one the VS Code extension's Enroll wrote) is moved into the store the next time `tn5250-tui`, `tn5250-tui mcp` or `tn5250-tui import` loads the file. `TN5250_TUI_SECRETS=file` forces the encrypted file, `=plain` the old clear text (your call). On Windows the Credential Manager is always used; if it is not usable the error says so rather than falling back. The wire is a separate matter: without TLS the password crosses the network in clear text.

To add a site, type `:sites` and choose add, or fill in the login form (Host, Port, TLS, User, Password) and give it a name under "Save as site". A site holds: name, host, port, tls, trustAnyCertificate, user, password, screenSize (`24x80` or `27x132`), codePage, deviceName, autoSignon. With autoSignon on, the user and password are typed into the first two input fields of the first screen (the second must be non-display) and Enter is pressed.

## Keys

| Key | 5250 key |
| --- | --- |
| F1 - F12 | PF1 - PF12 |
| Shift+F1 - Shift+F12 | PF13 - PF24 |
| Enter | Enter |
| Tab / Shift+Tab | Next / previous field |
| Arrow keys | Move the cursor |
| Home / End | Home / end of field |
| Insert / Delete | Insert mode / delete character |
| PgUp / PgDn | Roll up / roll down |
| Esc | Attn |
| Ctrl+R | Reset |
| Ctrl+X, Shift+Esc | System Request (the terminal must report Shift with Esc: Windows console does, many Linux terminals send a plain Esc) |
| Ctrl+K | Open the command bar |
| Ctrl+T / Ctrl+W | New tab (site list) / close the tab |
| Ctrl+PgUp / Ctrl+PgDn, Alt+1 - Alt+9 | Previous / next tab, tab by number |
| Ctrl+\ | Split view: the next tab beside this one; when split, keyboard to the other pane |

Field Exit, Field+ and Field- have no reliable key in a terminal; use `:key fieldExit`, `:key fieldPlus` and `:key fieldMinus`.

## Sessions (tabs)

Each connection is a tab. A tab bar appears above the screen once there are two: ` 1 PUB400  2 agent `, the active one in reverse video, a dropped one dimmed, a connecting one with `...`. Open another session from the site list (`:sites`, or Ctrl+T for a fresh tab with the list), with `:connect <site>` while this tab is connected, or with `:new <site>`. Switch with Ctrl+PgUp / Ctrl+PgDn or Alt+1 - Alt+9 (`:tab 2`, `:tab next`); Ctrl+W closes the tab (it asks when the tab is connected). Background tabs keep their sessions alive: a sign-on in progress finishes, and a dropped one says so when you come back.

**Split view.** `:split` (or Ctrl+\) shows the next tab beside this one, left and right of a divider; `:split 3` picks the tab for the other pane, and with one tab open `:split` opens a new one with the site list. The keyboard, the cursor and the OIA line belong to one pane; `:split` or Ctrl+\ again moves them to the other pane, and switching to the tab that is already in the other pane does the same. Tab switching otherwise changes the focused pane's tab. The other pane's tab is underlined in the tab bar. `:unsplit` goes back to one pane and keeps both tabs. Each pane gets half the width: two 132-column screens fit side by side from 266 columns, two 80-column ones from 162; below that the pane scrolls to follow the cursor.

## Local command line

`:!dir` or `:!ls -l` runs a command on this machine through `cmd /c` or `sh -c` and streams its output into an eight-row pane under the 5250 screen (`:local` hides and shows it, `:local clear` empties it; one command at a time). For a real shell, `:sh` hands the console to `$SHELL` / `%COMSPEC%` (or `TN5250_TUI_SHELL`) and redraws the terminal when you type `exit`; every session stays connected meanwhile. Neither touches the host.

## Commands

Open the command bar with `:` (when the cursor is not in an input field) or with Ctrl+K anywhere.

| Command | What it does |
| --- | --- |
| `connect [site\|host[:port]] [--tls]` | Connect to a site or host: in this tab when it holds no connection, else in a new tab |
| `new [site\|host[:port]] [--tls]` | Open a new tab (no argument: the site list) |
| `tab <n>\|next\|prev` | Switch tab |
| `close` | Close the tab and its connection |
| `split [n]` | Show tab n beside this one (no n: the next tab, or a new one); when split, move the keyboard to the other pane |
| `unsplit` | Back to one pane; the other tab stays open |
| `!<command>` | Run a command on this machine (not the host); its output streams into a pane under the screen |
| `local [on\|off\|clear]` | Show, hide or empty that pane |
| `sh` | Your shell in this console (`$SHELL`, `%COMSPEC%`, or `TN5250_TUI_SHELL`); `exit` returns to the terminal, the sessions stay connected |
| `disconnect` | Close the connection, keep the tab |
| `sites` | Site manager: list, add, edit, delete, connect |
| `settings` | Settings page: screen size, code page, device name, column separators, SEU colouring, auto sign-on, trace file, keep-alive; saved as `defaults` in sites.json (also: `s` in the site list, Settings button on the login form) |
| `import [settings.json]` | Import the tn5250-terminal VS Code extension's settings (see below) |
| `signon` | Typed sign-on now |
| `key <action>` | Press any action (`enter`, `pf13`, `reset`, `sysreq`, ...) |
| `send <text>` | Type the text into the field and press Enter |
| `trace on\|off [file]` | Protocol trace |
| `dump [file]` | Write the screen text to a file (default `tn5250-screen.txt`) |
| `size 27x132\|24x80` | Screen size for the next connection (27x132 is the default) |
| `cp <ccsid>` | Code page (default 37) |
| `help` | Show help |
| `q`, `quit` | Quit |

## Settings from the VS Code extension

If you use the [tn5250-terminal](https://github.com/DerKyberTakt/tn5250-terminal) VS Code extension (or the desktop app, which shares its settings), `:import` or

```
tn5250-tui import [--from path/to/settings.json] [--dry-run]
```

reads its `settings.json` (VS Code, Insiders, VSCodium or Cursor; `TN5250_VSCODE_SETTINGS` overrides) and brings over:

- the saved sessions as sites (name, host, port, TLS, certificate check, user, screen size, code page, device name, auto sign-on, demo mode);
- the defaults: screen size, code page, device name, column separators, auto sign-on;
- the seven attribute colours (`tn5250.colors.*`);
- the key overrides (`tn5250.keyboard.keymap`), as far as a console can deliver them: F1-F24 with Ctrl/Alt/Shift, Ctrl+letter, the named keys (Enter, Tab, Escape, Backspace, Delete, Insert, Home, End, PageUp, PageDown, arrows). Alt+letter, Ctrl+Shift+letter, Meta, Numpad, Space and Pause have no console form and are skipped;
- the text watermark and its position (an image watermark cannot be drawn in a terminal).

Passwords never come across: VS Code keeps them in its secret store, which no other program can read. Type each one once in the site form; an existing site keeps its password. The import only reads `settings.json`; it never writes to VS Code. The result goes into `sites.json` (`defaults.colors`, `defaults.keys`, `defaults.watermark`, `demo` on a site) and applies at once. A demo site shows its label tiled over the screen and on the status line.

## For AI agents: `tn5250-tui mcp` and `tn5250-tui attach`

`tn5250-tui mcp` is an [MCP](https://modelcontextprotocol.io) server on stdin/stdout that hosts 5250 sessions for an agent such as Claude Code, on the same engine as the terminal. In the workspace's `.mcp.json`:

```json
{ "mcpServers": { "tn5250": { "command": "tn5250-tui", "args": ["mcp", "--site", "agent", "--read-only"] } } }
```

`agent` is a saved site whose user is the agent's own profile (give the agent its own user profile; the host, not the agent, then decides what it may do). Tools: `connect`, `screen`, `press`, `type`, `fill`, `wait`, `find`, `sessions`, `disconnect`, `trace`, `dump`; every key waits for the host (keyboard unlocked and the screen quiet for 120 ms, 15 s cap). `--read-only` refuses typed text that starts a command that changes things (DLT*, CLR*, CHG*, CRT*, RMV*, UPD*, SBMJOB, CALL, RUNSQL, ...). `--audit FILE` appends one JSON line per agent action (passwords never appear; traces are masked as in the terminal). Credentials never travel through the MCP transport: they come from the sites file, `--user/--password` or `TN5250_USER`/`TN5250_PASSWORD`. A skill for Claude Code is in [`skill/tn5250-agent`](skill/tn5250-agent/SKILL.md).

Watch what the agent does: the server listens on 127.0.0.1 (random port, token in `<sites dir>/agent/<pid>.json`, mode 0600) and

```
tn5250-tui attach            # the newest server's first session, live, read-only
tn5250-tui attach --list     # the running servers and their sessions
tn5250-tui attach s2         # a session by handle; Tab cycles, r reloads, q quits
```

renders it in any terminal (a VS Code terminal too). `--no-listen` turns the listener off.

## Try it without an IBM i

`fakehost` replays a recorded PUB400.COM sign-on screen, then answers every Enter with a "FAKE HOST  Enter received" screen:

```sh
go run ./cmd/fakehost                 # listens on 127.0.0.1:2323
tn5250-tui 127.0.0.1:2323             # in a second terminal
```

Flags: `-listen 127.0.0.1:2323`, `-capture testdata/pub400-signon-3477fc.json`, `-strict` (close the connection when the client differs from the capture; default is to log the difference and carry on).

## Build from source

Go 1.27 or newer.

```sh
go build ./cmd/tn5250-tui
go test ./...
./scripts/build.sh          # dist/tn5250-tui, version from git describe
```

On Windows: `.\scripts\build.ps1`.

## License

MIT, see [LICENSE](LICENSE). Changes are listed in [CHANGELOG.md](CHANGELOG.md).
