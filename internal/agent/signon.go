package agent

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/DerKyberTakt/tn5250-tui/internal/tn5250"
)

var (
	hostMsgID       = regexp.MustCompile(`\b(CPF|CPD|CPI|CPA)[0-9A-F]{4}\b`)
	sessionLimitMsg = regexp.MustCompile(`(?i)\bCPF1220\b|device session limit`)
)

// isSignonScreen reports whether the screen has the two fields a sign-on is typed into: a
// first input field and a non-display second one (the test typedSignon uses).
func isSignonScreen(snap tn5250.Snapshot) bool {
	n := 0
	for _, f := range snap.Fields {
		if f.Bypass {
			continue
		}
		n++
		if n == 2 {
			return f.Nondisplay
		}
	}
	return false
}

// signonRefusal reads the screen that came back after connect typed a sign-on. A host that
// turns the sign-on down shows the sign-on display again with its message; refused is then
// true and msg is that message (the line with a message id when there is one, else the last
// line with text). sessionLimit marks CPF1220, which is not a password problem.
func signonRefusal(snap tn5250.Snapshot) (msg string, refused, sessionLimit bool) {
	if !isSignonScreen(snap) {
		return "", false, false
	}
	last := ""
	for r := snap.Rows - 1; r >= 0; r-- {
		line := strings.TrimSpace(snap.RowText(r))
		if line == "" {
			continue
		}
		if last == "" {
			last = line
		}
		if hostMsgID.MatchString(line) {
			msg = line
			break
		}
	}
	if msg == "" {
		msg = last
	}
	return msg, true, sessionLimitMsg.MatchString(snap.Text())
}

// refusedKey names an endpoint and user for the sign-on gate.
func refusedKey(host string, port int, user string) string {
	return fmt.Sprintf("%s:%d/%s", strings.ToLower(strings.TrimSpace(host)), port, strings.ToUpper(strings.TrimSpace(user)))
}

// signonGate returns an error when this user's sign-on at this endpoint was already refused
// with the same password: every further attempt counts toward disabling the profile
// (QMAXSIGN), so the server makes none until the password changes or the server restarts.
func (m *Manager) signonGate(t target) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if why, ok := m.refused[refusedKey(t.host, t.port, t.user)]; ok && why.password == t.password {
		return fmt.Errorf("not signing on again as %s at %s: the host already refused this sign-on (%s). Another attempt counts toward disabling the profile; correct the password in the site first (or pass signon=false to connect without signing on)", strings.ToUpper(t.user), t.host, why.message)
	}
	return nil
}

func (m *Manager) noteRefused(t target, message string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.refused == nil {
		m.refused = map[string]refusal{}
	}
	m.refused[refusedKey(t.host, t.port, t.user)] = refusal{password: t.password, message: message}
}

type refusal struct {
	password string
	message  string
}
