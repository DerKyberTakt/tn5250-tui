package agent

import (
	"fmt"
	"strings"
)

// guardedVerbs are the command prefixes ReadOnly mode refuses to type: they delete, clear,
// change, create, update or power things down, or start work, on the host. The test is a
// prefix test on the first word of the text (so DLTF and DLTLIB match DLT, CHGLIBL matches
// CHG). WRK is deliberately absent: Work with ... screens only look. Reading a file, a spool
// file or a message is never guarded.
var guardedVerbs = []string{
	"DLT", "CLR", "RMV", "CHG", "UPD", "CRT", "INZ", "DEL", "DROP", "UPDATE", "INSERT", "MERGE",
	"PWRDWNSYS", "ENDSBS", "ENDSYS", "ENDTCP", "STRSBS", "SBMJOB", "CALL", "RUNSQL", "ADD",
}

// guardedVerb returns the guarded verb the text starts with, "" when it is allowed. A leading
// "?" (prompt) and a library qualifier ("QSYS/DLTF") are looked through.
func guardedVerb(text string) string {
	t := strings.ToUpper(strings.TrimSpace(text))
	t = strings.TrimLeft(t, "? ")
	word := t
	if i := strings.IndexAny(word, " \t("); i >= 0 {
		word = word[:i]
	}
	if i := strings.LastIndex(word, "/"); i >= 0 {
		word = word[i+1:]
	}
	if word == "" {
		return ""
	}
	// The longest verb that is a prefix wins, so the message names UPDATE rather than UPD.
	best := ""
	for _, v := range guardedVerbs {
		if strings.HasPrefix(word, v) && len(v) > len(best) {
			best = v
		}
	}
	return best
}

// checkReadOnly is the read-only gate for typed text.
func checkReadOnly(text string) error {
	if v := guardedVerb(text); v != "" {
		return fmt.Errorf("read-only mode: %s is blocked; ask the user to run it", v)
	}
	return nil
}
