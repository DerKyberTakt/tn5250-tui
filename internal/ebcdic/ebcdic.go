// Package ebcdic converts between IBM EBCDIC single-byte code pages and Unicode, with the
// same rules the tn5250-terminal engine uses: nulls and the attribute range decode to a
// space, control bytes to a hollow box, unmapped characters encode to X'6F' ('?').
package ebcdic

import "fmt"

// Unprintable is what a byte that is not a printable character shows as (hollow box, as on ACS).
const Unprintable = '□'

// Substitute is the byte an unmapped character encodes to ('?' in every EBCDIC page).
const Substitute = 0x6f

// CodePage is one EBCDIC code page.
type CodePage struct {
	CCSID  int
	decode [256]rune
	encode map[rune]byte
}

var cache = map[int]*CodePage{}

// Get returns the code page for a CCSID, or an error when it is not in the tables.
func Get(ccsid int) (*CodePage, error) {
	if cp, ok := cache[ccsid]; ok {
		return cp, nil
	}
	t, ok := tables[ccsid]
	if !ok {
		return nil, fmt.Errorf("unsupported code page %d", ccsid)
	}
	cp := &CodePage{CCSID: ccsid, decode: t, encode: make(map[rune]byte, 256)}
	// Iterate downward so that lower byte values win when a character maps twice.
	for b := 255; b >= 0; b-- {
		cp.encode[t[b]] = byte(b)
	}
	cache[ccsid] = cp
	return cp, nil
}

// MustGet is Get for a CCSID known to exist (37 is always there).
func MustGet(ccsid int) *CodePage {
	cp, err := Get(ccsid)
	if err != nil {
		panic(err)
	}
	return cp
}

// Supported lists the CCSIDs in the tables, ascending.
func Supported() []int {
	out := make([]int, 0, len(tables))
	for k := range tables {
		out = append(out, k)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1] > out[j]; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}

// Decode converts one byte. Nulls and the attribute range (X'20'-X'3F', never text) become a
// space; control bytes the host wrote as data (X'01'-X'1F', and X'FF' which maps to a C1
// control) become Unprintable.
func (cp *CodePage) Decode(b byte) rune {
	if b == 0 || (b >= 0x20 && b < 0x40) {
		return ' '
	}
	if b < 0x20 {
		return Unprintable
	}
	r := cp.decode[b]
	if r < 0x20 || (r >= 0x7f && r <= 0x9f) {
		return Unprintable
	}
	return r
}

// Encode converts one character; Substitute when it is not in the page.
func (cp *CodePage) Encode(r rune) byte {
	if b, ok := cp.encode[r]; ok {
		return b
	}
	return Substitute
}

// DecodeBytes converts a byte slice to text, one rune per byte.
func (cp *CodePage) DecodeBytes(b []byte) string {
	out := make([]rune, len(b))
	for i, x := range b {
		out[i] = cp.Decode(x)
	}
	return string(out)
}

// EncodeString converts text to bytes, one byte per rune.
func (cp *CodePage) EncodeString(s string) []byte {
	rs := []rune(s)
	out := make([]byte, len(rs))
	for i, r := range rs {
		out[i] = cp.Encode(r)
	}
	return out
}
