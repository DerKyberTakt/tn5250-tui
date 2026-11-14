package agent

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/DerKyberTakt/tn5250-tui/internal/tn5250"
)

// keyNames maps a normalised key name (lower case, no spaces or underscores) to the engine
// action. Built once from the engine's action names plus the aliases an agent is likely to use.
var keyNames = buildKeyNames()

func buildKeyNames() map[string]tn5250.Action {
	m := map[string]tn5250.Action{}
	for _, a := range []tn5250.Action{
		tn5250.ActionEnter, tn5250.ActionFieldExit, tn5250.ActionFieldPlus, tn5250.ActionFieldMinus,
		tn5250.ActionTab, tn5250.ActionBacktab, tn5250.ActionLeft, tn5250.ActionRight, tn5250.ActionUp,
		tn5250.ActionDown, tn5250.ActionHome, tn5250.ActionNewline, tn5250.ActionBackspace,
		tn5250.ActionDelete, tn5250.ActionInsert, tn5250.ActionEndOfField, tn5250.ActionEraseEOF,
		tn5250.ActionEraseInput, tn5250.ActionDup, tn5250.ActionFieldMark, tn5250.ActionReset,
		tn5250.ActionAttn, tn5250.ActionSysReq, tn5250.ActionClear, tn5250.ActionHelp,
		tn5250.ActionPageUp, tn5250.ActionPageDown, tn5250.ActionPrint,
		tn5250.ActionRecordBackspace, tn5250.ActionTestRequest, tn5250.ActionEraseField,
	} {
		m[strings.ToLower(string(a))] = a
	}
	aliases := map[string]tn5250.Action{
		"return":        tn5250.ActionEnter,
		"field+":        tn5250.ActionFieldPlus,
		"field-":        tn5250.ActionFieldMinus,
		"shifttab":      tn5250.ActionBacktab,
		"attention":     tn5250.ActionAttn,
		"sysrq":         tn5250.ActionSysReq,
		"systemrequest": tn5250.ActionSysReq,
		"pageup":        tn5250.ActionPageUp,
		"rolldown":      tn5250.ActionPageUp,
		"pagedown":      tn5250.ActionPageDown,
		"rollup":        tn5250.ActionPageDown,
		"erase":         tn5250.ActionEraseEOF,
	}
	for k, v := range aliases {
		m[k] = v
	}
	for n := 1; n <= 24; n++ {
		m["pf"+strconv.Itoa(n)] = tn5250.PF(n)
		m["f"+strconv.Itoa(n)] = tn5250.PF(n)
	}
	return m
}

// ResolveKey maps a key name or alias to the engine action; case-insensitive. The error lists
// the accepted names.
func ResolveKey(name string) (tn5250.Action, error) {
	k := strings.ToLower(strings.TrimSpace(name))
	k = strings.NewReplacer(" ", "", "_", "").Replace(k)
	if a, ok := keyNames[k]; ok {
		return a, nil
	}
	return "", fmt.Errorf("unknown key %q; use one of: %s", name, KeyList())
}

// KeyList is the accepted key names, for error messages and tool descriptions.
func KeyList() string {
	seen := map[string]bool{}
	var names []string
	for k := range keyNames {
		if strings.HasPrefix(k, "pf") && len(k) > 2 && k[2] >= '0' && k[2] <= '9' {
			continue // f1..f24 stands for both spellings
		}
		if seen[k] {
			continue
		}
		seen[k] = true
		names = append(names, k)
	}
	sort.Strings(names)
	return strings.Join(names, ", ") + " (f1..f24 or pf1..pf24)"
}

// postsToHost reports whether pressing the action sends a record to the host, so the caller
// has to wait for the answer: the AID keys and the out-of-band requests.
func postsToHost(a tn5250.Action) bool {
	if _, ok := tn5250.AIDOf(a); ok {
		return true
	}
	switch a {
	case tn5250.ActionAttn, tn5250.ActionSysReq, tn5250.ActionTestRequest:
		return true
	}
	return false
}
