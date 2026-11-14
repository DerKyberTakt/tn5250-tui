package tn5250

import (
	"sync"

	"github.com/DerKyberTakt/tn5250-tui/internal/ebcdic"
)

// lowestGraphic: bytes below X'40' are nulls and controls, never typed characters.
const lowestGraphic = 0x40

var (
	variantOnce  sync.Once
	variantBytes [256]bool
)

// IsVariantByte reports whether EBCDIC byte b means a different character in at least two of
// the code pages the engine offers: the "variant" characters ! @ # $ [ ] { } | \ ^ ~ and the
// accented letters. The emulator sends raw EBCDIC and the host reads it with the CCSID of the
// sign-on job, so a password typed under the wrong code page arrives as other characters and
// fails although it is right. The sign-on hint looks for exactly these bytes.
func IsVariantByte(b byte) bool {
	variantOnce.Do(func() {
		var pages []*ebcdic.CodePage
		for _, id := range ebcdic.Supported() {
			if cp, err := ebcdic.Get(id); err == nil {
				pages = append(pages, cp)
			}
		}
		for b := lowestGraphic; b < 256; b++ {
			for _, cp := range pages[1:] {
				if cp.Decode(byte(b)) != pages[0].Decode(byte(b)) {
					variantBytes[b] = true
					break
				}
			}
		}
	})
	return variantBytes[b]
}

// DefaultVariantLimit is how many variant characters VariantChars lists when limit is 0.
const DefaultVariantLimit = 10

// VariantChars lists the distinct variant characters among b, decoded with cp, in the order
// they appear (at most limit; 0 means DefaultVariantLimit). Blanks and nulls are skipped. The
// result is empty when every byte means the same in every code page.
func VariantChars(b []byte, cp *ebcdic.CodePage, limit int) []string {
	if limit <= 0 {
		limit = DefaultVariantLimit
	}
	var out []string
	for _, x := range b {
		if len(out) >= limit {
			break
		}
		if x < lowestGraphic || x == 0x40 || !IsVariantByte(x) {
			continue
		}
		ch := string(cp.Decode(x))
		dup := false
		for _, have := range out {
			if have == ch {
				dup = true
				break
			}
		}
		if !dup {
			out = append(out, ch)
		}
	}
	return out
}
