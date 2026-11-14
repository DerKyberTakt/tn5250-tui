# Changelog

## 0.1.0 - unreleased

- Engine: telnet (RFC 1205/2877/4777), GDS records, Write To Display orders, read commands, fields and the keyboard model, ported from the tn5250-terminal engine (plain display, no enhanced interface).
- TUI: full-screen 5250 grid on tcell/tview with an OIA line, a `:` command bar, Shift+F1-F12 as F13-F24, login form and site manager.
- Sites: plain-text `sites.json` per user (password in clear, by design).
- Client: TCP/TLS connection with trace file support.
- Test host: `internal/fakehost` and `cmd/fakehost` replay a PUB400 capture so the TUI runs without an IBM i.
- Install: `install.sh` (Linux, macOS), `install.ps1` (Windows), AUR `tn5250-tui-bin` PKGBUILD.
- CI and releases: GitHub Actions for vet/test/build and tagged releases with SHA256SUMS.
