---
name: tn5250-agent
description: Drive an IBM i 5250 session through the tn5250-tui MCP server (connect, read the screen, press keys, fill fields, wait for the host). Use when the user asks to navigate green screens, run a menu option, read a display, check a job or a spooled file through the terminal, or reproduce something "as a user would see it".
---

# Driving a 5250 session

The `tn5250` MCP server (`tn5250-tui mcp`) hosts headless 5250 sessions on the same engine as the
terminal. A human can watch every session live with `tn5250-tui attach` in another terminal, so act
as a careful operator would: read first, press second, and say what you see.

## Ground rules
- A 5250 session is a sign-on to a real system. Treat every screen as production unless the user
  says the site is a test or demo system. **Before any command or key that changes data (DLT*, CLR*,
  CHG*, CRT*, RMV*, UPD*, SBMJOB, CALL, RUNSQL, menu options that update), stop and ask the user.**
  In `--read-only` mode the server refuses those itself; the refusal names the verb: relay it, do not
  work around it.
- Never type or log a password. `connect` takes a `site` name; the server holds the credentials.
- One screen at a time. After every key, use the screen the tool returns; do not call `screen`
  again unless something else happened. Use `rows` to read part of a wide screen.
- Say what the screen shows in one line before deciding the next key (the user is watching the
  same screen).

## Tools (all take `session` except `connect` and `sessions`)
| Tool | Use it for |
|---|---|
| `connect {site}` | open a session on a saved site; signs on when the site has a user and password; returns the first unlocked screen |
| `screen {rows?, fields_only?}` | re-read the screen (text, fields with 1-based row/col, cursor, OIA, seq) |
| `press {key, times?}` | one key: `enter`, `f3`, `f12`, `pageup`, `pagedown`, `tab`, `fieldexit`, `reset`, `attn`, `sysreq`, `home`… waits for the host |
| `type {text, field?, then?}` | type into the field at the cursor or `field` (index); `then: "enter"` presses Enter after |
| `fill {fields:[{label|index|row,col, text}], then?}` | several fields at once, by the label to their left ("Option", "Library") |
| `wait {until: "unlock"|"text"|"change"|"cursor", text?, timeout_ms?}` | wait for the host without pressing anything |
| `find {text}` | where a text appears (row, col, line) |
| `sessions` | the open sessions and their state |
| `disconnect {signoff?}` | close; `signoff: true` types SIGNOFF first when a command line is on screen |
| `trace {lines?}` / `dump` | protocol trace (passwords masked) / full screen with attributes, for debugging |

## Reading a screen
- `oia.inhibited` = `system` means the host is still working: call `wait {until:"unlock"}` instead of pressing again.
  `error` means an operator error (the `oia.message` says which): press `reset`, fix the input, retry.
- `fields` lists the input fields in screen order with their current text; `bypass` fields are protected.
- The command line of a menu is the field labelled `===>`; `fill {fields:[{label:"===>", text:"WRKACTJOB"}], then:"enter"}`.
- Subfiles: `pagedown` / `pageup` roll; `More...` or `Bottom` on the last line says whether there is more.
- `F3` exits a program, `F12` goes back one screen, `F5` refreshes, `F10`/`F11` often toggle views.

## Patterns
- **Run a menu path**: `connect` → read the menu → `type {text:"1", then:"enter"}` per level → read.
- **Run a command**: `fill {fields:[{label:"===>", text:"DSPLIBL"}], then:"enter"}` → read → `press f3` when done.
- **Wait for a long job**: `wait {until:"text", text:"Bottom", timeout_ms:30000}`.
- **Something is off**: `dump` (attributes), `trace {lines: 100}`, then report; do not retry blindly.
- **Finish**: `disconnect {signoff:true}` when you signed on; leave sessions the user opened alone.

## Set-up (for the user, once)
```json
// .mcp.json in the workspace
{ "mcpServers": { "tn5250": { "command": "tn5250-tui", "args": ["mcp", "--site", "agent", "--read-only"] } } }
```
`agent` is a site in `sites.json` (`tn5250-tui` → `:sites` → add) whose user is the agent's own profile.
Watch the agent: `tn5250-tui attach` in another terminal.
