package tn5250

import "strings"

// hostNLS holds the NEW-ENVIRON values that go with a host code page (RFC 4777 section 4:
// KBDTYPE, CODEPAGE and CHARSET are USERVARs sent with or without DEVNAME; CODEPAGE and CHARSET are only
// used when a KBDTYPE goes with them, otherwise the host takes its system values).
//
// CHARSET is the graphic character set id of the CCSID; KBDTYPE the 3-letter keyboard type of
// IBM's "National language keyboard types and SBCS code pages" table (CRTDEVDSP KBDTYPE). The
// euro code pages 1140-1149 are the same character sets with the euro sign (GCSGID 695) and the
// euro keyboard types CRTDEVDSP lists for them (USE, AGE, DME, FNE, ITE, SPE, UKE, FAE, ICE);
// 1148 keeps INB, IBM lists no euro keyboard for International. Several countries share a code
// page (500, 277, 278, 284, 297 ...): the first keyboard IBM lists is the default, the keyboard
// type option overrides it.
type hostNLS struct {
	charset string
	kbdType string
}

var nlsTable = map[int]hostNLS{
	37:   {"697", "USB"},
	273:  {"697", "AGB"},
	277:  {"697", "DMB"},
	278:  {"697", "FNB"},
	280:  {"697", "ITB"},
	284:  {"697", "SPB"},
	285:  {"697", "UKB"},
	297:  {"697", "FAB"},
	420:  {"235", "CLB"},
	424:  {"941", "NCB"},
	500:  {"697", "INB"},
	838:  {"1176", "THB"},
	870:  {"959", "CSB"},
	871:  {"697", "ICB"},
	875:  {"925", "GNB"},
	880:  {"960", "CYB"},
	1025: {"1150", "BGB"},
	1026: {"1152", "TKB"},
	1140: {"695", "USE"},
	1141: {"695", "AGE"},
	1142: {"695", "DME"},
	1143: {"695", "FNE"},
	1144: {"695", "ITE"},
	1145: {"695", "SPE"},
	1146: {"695", "UKE"},
	1147: {"695", "FAE"},
	1148: {"695", "INB"},
	1149: {"695", "ICE"},
}

// lookupNLS returns the values for a code page, with an optional keyboard type override; false
// when the code page is not in the table (send nothing, the host uses its defaults).
func lookupNLS(ccsid int, keyboardType string) (hostNLS, bool) {
	base, ok := nlsTable[ccsid]
	if !ok {
		return hostNLS{}, false
	}
	if kbd := strings.ToUpper(strings.TrimSpace(keyboardType)); kbd != "" {
		return hostNLS{charset: base.charset, kbdType: kbd}, true
	}
	return base, true
}
