package tn5250

// The 5250 display attribute bytes are X'20'..X'3F'.
//
// Bit layout (5494 Functions Reference, display attribute table):
//
//	0x01  reverse image
//	0x02  colour variant (white / blink / yellow / blue, by group)
//	0x04  underline
//	0x18  colour group: 0 green/white, 1 red, 2 turquoise/yellow, 3 pink/blue
//	low three bits all set (x7, xF) = nondisplay
//
// Group 2 (turquoise/yellow) always carries column separators.
const (
	// AttrMin is the lowest attribute byte.
	AttrMin = 0x20
	// AttrMax is the highest attribute byte.
	AttrMax = 0x3f
	// AttrNormal is green, normal intensity: the attribute in effect at the start of a screen.
	AttrNormal = 0x20
)

// Attr is a decoded display attribute.
type Attr struct {
	// Color is one of green, white, red, turquoise, yellow, pink, blue.
	Color string
	// Reverse is reverse image.
	Reverse bool
	// Underline is underlined text.
	Underline bool
	// Blink is blinking text (the red group with the variant bit).
	Blink bool
	// ColumnSeparators is set for the turquoise/yellow group.
	ColumnSeparators bool
	// Nondisplay hides the field text (passwords).
	Nondisplay bool
}

func isAttribute(b byte) bool { return b >= AttrMin && b <= AttrMax }

// DecodeAttr decodes a display attribute byte. A byte outside X'20'..X'3F' is not an attribute
// and decodes as AttrNormal.
func DecodeAttr(b byte) Attr {
	if !isAttribute(b) {
		b = AttrNormal
	}
	group := (b >> 3) & 0x03
	variant := b&0x02 != 0
	a := Attr{
		Reverse:    b&0x01 != 0,
		Underline:  b&0x04 != 0,
		Nondisplay: b&0x07 == 0x07,
	}
	switch group {
	case 0:
		a.Color = "green"
		if variant {
			a.Color = "white"
		}
	case 1:
		a.Color = "red"
		a.Blink = variant
	case 2:
		a.Color = "turquoise"
		if variant {
			a.Color = "yellow"
		}
		a.ColumnSeparators = true
	default:
		a.Color = "pink"
		if variant {
			a.Color = "blue"
		}
	}
	return a
}
