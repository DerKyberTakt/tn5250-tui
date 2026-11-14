package tn5250

import "regexp"

// signonMaxFields is the most entry fields a sign-on screen has (user, password, program,
// menu, library, ...).
const signonMaxFields = 6

var signonTitle = regexp.MustCompile(`(?i)\bSign\s*On\b`)

// SignonProbe says whether the screen is a sign-on screen and which variant characters the
// password field holds. It never carries the password itself.
type SignonProbe struct {
	// Screen is true when the screen has the sign-on shape (see Session.SignonProbe).
	Screen bool
	// VariantChars lists the characters in the password field that differ between code pages
	// (see VariantChars), decoded with the session code page. The password is not in it.
	VariantChars []string
	// CodePage is the CCSID this session decodes and encodes with.
	CodePage int
}

// SignonProbe inspects the screen for the sign-on hint. A sign-on screen says "Sign On" in its
// top rows, or has the sign-on shape: a visible user field first and exactly one non-display
// field among two to six entry fields. Customised screens (a "Welcome" banner, other
// languages) have the shape without the words; a Change Password screen has three non-display
// fields and is left alone.
func (s *Session) SignonProbe() SignonProbe {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := SignonProbe{CodePage: s.cp.CCSID}
	f := s.signonPasswordField()
	if f == nil {
		return p
	}
	p.Screen = true
	p.VariantChars = VariantChars(s.screen.fieldBytes(f), s.cp, 0)
	return p
}

// signonPasswordField is the password field of a sign-on screen, nil for any other screen.
// The caller holds s.mu.
func (s *Session) signonPasswordField() *field {
	var inputs, hidden []*field
	for _, f := range s.screen.fields {
		if f.bypass() {
			continue
		}
		inputs = append(inputs, f)
		if f.nondisplay() {
			hidden = append(hidden, f)
		}
	}
	if len(hidden) != 1 || len(inputs) < 2 || len(inputs) > signonMaxFields {
		return nil
	}
	if inputs[0] == hidden[0] && !s.isSignonScreen() {
		return nil
	}
	return hidden[0]
}

// isSignonScreen reports whether "Sign On" is in the first four rows.
func (s *Session) isSignonScreen() bool {
	for r := 0; r < 4 && r < s.screen.rows; r++ {
		if signonTitle.MatchString(s.screen.rowText(r, s.cp)) {
			return true
		}
	}
	return false
}
