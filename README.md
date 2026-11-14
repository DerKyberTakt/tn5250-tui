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

Sites live in a plain JSON file:

| OS | Path |
| --- | --- |
| Linux | `$XDG_CONFIG_HOME/tn5250-tui/sites.json` (default `~/.config/tn5250-tui/sites.json`) |
| Windows | `%APPDATA%\tn5250-tui\sites.json` |

The password is stored in clear text, by design: no credential manager, just a file (mode 0600 on Linux). Do not put it anywhere other people can read it.

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
| Ctrl+X | System Request |
| Ctrl+K | Open the command bar |

Field Exit, Field+ and Field- have no reliable key in a terminal; use `:key fieldExit`, `:key fieldPlus` and `:key fieldMinus`.

## Commands

Open the command bar with `:` (when the cursor is not in an input field) or with Ctrl+K anywhere.

| Command | What it does |
| --- | --- |
| `connect [site\|host[:port]] [--tls]` | Connect to a site or host |
| `disconnect` | Close the connection |
| `sites` | Site manager: list, add, edit, delete, connect |
| `signon` | Typed sign-on now |
| `key <action>` | Press any action (`enter`, `pf13`, `reset`, `sysreq`, ...) |
| `send <text>` | Type the text into the field and press Enter |
| `trace on\|off [file]` | Protocol trace |
| `dump [file]` | Write the screen text to a file (default `tn5250-screen.txt`) |
| `size 24x80\|27x132` | Screen size for the next connection |
| `cp <ccsid>` | Code page (default 37) |
| `help` | Show help |
| `q`, `quit` | Quit |

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
