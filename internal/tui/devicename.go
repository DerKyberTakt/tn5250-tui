package tui

import (
	"os"
	"os/user"
	"strings"
)

// maxDeviceName is the longest workstation (device) name IBM i accepts.
const maxDeviceName = 10

// maxDeviceRetries is how many other names are tried when the host closes the connection
// at once (the name is probably in use) before the user is told.
const maxDeviceRetries = 10

// deviceNameClean upper-cases s and keeps letters and digits only.
func deviceNameClean(s string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(s) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// nextDeviceName is the workstation name for connection attempt number attempt (0 = the first
// connection, 1 = the first retry, ...). The rules are those of the TypeScript client:
//
//   - '*' in the template is a letter A..Z, one per attempt (attempt 0 = A);
//   - '=' is a digit 0..9, one per attempt (attempt 0 = 0);
//   - a template with neither is a plain name: attempt 0 is the name itself, attempt 1..9 the
//     name followed by the digit 1..9;
//   - the result is upper case, letters and digits only for templates, at most 10 characters
//     (the base is cut to make room for the suffix).
//
// It returns "" when the template has no further name (plain and '=' names after attempt 9,
// '*' after attempt 25, both after attempt 129). &COMPN and &USERN must already be replaced
// (see expandDeviceVars).
func nextDeviceName(template string, attempt int) string {
	t := strings.TrimSpace(template)
	if t == "" || attempt < 0 {
		return ""
	}
	star := strings.Contains(t, "*")
	eq := strings.Contains(t, "=")
	if !star && !eq {
		name := strings.ToUpper(t)
		if attempt == 0 {
			return name
		}
		if attempt > 9 {
			return ""
		}
		suffix := string(rune('0' + attempt))
		return truncateName(name, len(suffix)) + suffix
	}
	limit := 9
	switch {
	case star && eq:
		limit = 129 // lcm(26, 10) - 1 distinct letter/digit pairs
	case star:
		limit = 25
	}
	if attempt > limit {
		return ""
	}
	base := deviceNameClean(strings.NewReplacer("*", "", "=", "").Replace(t))
	suffix := ""
	if star {
		suffix += string(rune('A' + attempt%26))
	}
	if eq {
		suffix += string(rune('0' + attempt%10))
	}
	return truncateName(base, len(suffix)) + suffix
}

// truncateName cuts name so that name plus a suffix of n characters fits in maxDeviceName.
func truncateName(name string, n int) string {
	room := max(1, maxDeviceName-n)
	if len(name) > room {
		return name[:room]
	}
	return name
}

// expandDeviceVars replaces &COMPN (computer name) and &USERN (user name), any case, with
// their values, as the ACS "Generate Workstation ID" templates do.
func expandDeviceVars(template string) string {
	if !strings.Contains(template, "&") {
		return template
	}
	host, _ := os.Hostname()
	var name string
	if u, err := user.Current(); err == nil {
		name = u.Username
		if i := strings.LastIndexAny(name, `\/`); i >= 0 {
			name = name[i+1:]
		}
	}
	return replaceFold(replaceFold(template, "&COMPN", deviceNameClean(host)), "&USERN", deviceNameClean(name))
}

// replaceFold is strings.ReplaceAll ignoring the case of old.
func replaceFold(s, old, repl string) string {
	var b strings.Builder
	lower, lo := strings.ToLower(s), strings.ToLower(old)
	for {
		i := strings.Index(lower, lo)
		if i < 0 {
			b.WriteString(s)
			return b.String()
		}
		b.WriteString(s[:i])
		b.WriteString(repl)
		s, lower = s[i+len(old):], lower[i+len(old):]
	}
}
