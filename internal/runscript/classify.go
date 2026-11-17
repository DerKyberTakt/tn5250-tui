// Package runscript runs the lines of a script against an IBM i: SQL on the database
// server, CL commands on the remote command server, Qshell command lines through QSH with
// their output read back. The kind of each statement is told from its text (Classify), so
// a script mixes the three without prefixes; an explicit `SQL:`, `CL:` or `QSH:` prefix
// always wins.
//
// A misclassified line goes to the wrong interpreter and comes back as an error, nothing
// worse: the host is the judge. The one real ambiguity is CALL (a CL `CALL PGM(X)` or
// `CALL X PARM(...)` against an SQL `CALL LIB.PROC(...)`), decided by its shape. Qshell is
// only chosen when the line starts like a shell command (a path, a known command name, a
// `VAR=value` assignment, or a pipeline), because Qshell can run anything. What is left
// is SQL.
package runscript

import (
	"regexp"
	"strings"
)

// Kind of statement.
type Kind int

const (
	SQL Kind = iota
	CL
	QSH
	PASE
)

func (k Kind) String() string {
	switch k {
	case CL:
		return "CL"
	case QSH:
		return "QSH"
	case PASE:
		return "PASE"
	}
	return "SQL"
}

// Classified is one statement with its kind.
type Classified struct {
	Kind Kind
	Text string // the statement without a prefix
	// Why says what decided the kind, for the status line: "prefix", "SQL keyword SELECT",
	// "CL verb WRKACTJOB", "shell command ls", "pipeline", "default".
	Why string
}

var (
	prefixRe   = regexp.MustCompile(`(?i)^\s*(SQL|CL|QSH|SH|PASE)\s*:\s*`)
	commentRe  = regexp.MustCompile(`^(\s|--[^\n]*(\n|$)|/\*[\s\S]*?\*/)+`)
	firstWord  = regexp.MustCompile(`^([A-Za-z0-9_$#@./~-]+)`)
	clShapeRe  = regexp.MustCompile(`(?i)^([A-Z0-9$#@_]{1,10}/)?[A-Z][A-Z0-9$#@_]{1,9}$`)
	clParmRe   = regexp.MustCompile(`(?i)^\s*(\*[A-Z0-9]+|[A-Z0-9$#@_]+\s*\(|'[^']*'|&[A-Z0-9]+|[A-Z0-9$#@_./]+\s*$)`)
	assignRe   = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)
	callClRe   = regexp.MustCompile(`(?i)^CALL\s+(PGM\s*\(|[A-Z0-9$#@_]+(/[A-Z0-9$#@_]+)?\s*(PARM\s*\(|$))`)
	wordStopRe = regexp.MustCompile(`[\s(]`)
)

// paseOnly are tools of /QOpenSys/pkgs/bin (the open-source packages) or that need the PASE
// shell; the same list as the desktop's runKind.ts.
var paseOnly = map[string]bool{
	"bash": true, "zsh": true, "git": true, "python": true, "python3": true, "pip": true, "pip3": true, "node": true, "npm": true,
	"npx": true, "yum": true, "rpm": true, "curl": true, "wget": true, "jq": true, "gcc": true, "g++": true, "make": true,
	"cmake": true, "perl": true, "ruby": true, "php": true, "vim": true, "nano": true, "rsync": true, "tmux": true, "go": true,
	"cargo": true, "rustc": true, "sudo": true, "htop": true, "tree": true, "ncdu": true, "bat": true, "rg": true, "fd": true,
}

// sqlKeywords start a statement the database server runs.
var sqlKeywords = map[string]bool{
	"SELECT": true, "VALUES": true, "WITH": true, "INSERT": true, "UPDATE": true, "DELETE": true,
	"MERGE": true, "CREATE": true, "ALTER": true, "DROP": true, "DECLARE": true, "SET": true,
	"GRANT": true, "REVOKE": true, "COMMENT": true, "LABEL": true, "TRUNCATE": true, "RENAME": true,
	"BEGIN": true, "COMMIT": true, "ROLLBACK": true, "SAVEPOINT": true, "RELEASE": true, "LOCK": true,
	"EXPLAIN": true, "REFRESH": true, "TRANSFER": true, "CONNECT": true, "DISCONNECT": true,
	"PREPARE": true, "EXECUTE": true, "DESCRIBE": true, "OPEN": true, "FETCH": true, "CLOSE": true,
	"ANALYZE": true, "SIGNAL": true, "RESIGNAL": true, "CASE": true, "IF": true, "WHILE": true,
	"FOR": true, "LOOP": true, "REPEAT": true, "RETURN": true, "GET": true, "FREE": true, "HOLD": true,
	"ALLOCATE": true, "ASSOCIATE": true, "ITERATE": true, "LEAVE": true, "GOTO": true,
}

// shellCommands are the Qshell / PASE command names a line can start with.
var shellCommands = map[string]bool{
	"ls": true, "cat": true, "cd": true, "pwd": true, "echo": true, "grep": true, "egrep": true,
	"find": true, "export": true, "env": true, "printenv": true, "head": true, "tail": true,
	"wc": true, "sort": true, "uniq": true, "cut": true, "sed": true, "awk": true, "tr": true,
	"touch": true, "mkdir": true, "rm": true, "rmdir": true, "cp": true, "mv": true, "chmod": true,
	"chown": true, "chgrp": true, "ln": true, "df": true, "du": true, "ps": true, "kill": true,
	"date": true, "uname": true, "whoami": true, "id": true, "which": true, "iconv": true,
	"system": true, "db2": true, "java": true, "javac": true, "python": true, "python3": true,
	"node": true, "npm": true, "git": true, "curl": true, "wget": true, "ssh": true, "scp": true,
	"sftp": true, "tar": true, "gzip": true, "gunzip": true, "zip": true, "unzip": true, "jar": true,
	"sh": true, "bash": true, "ksh": true, "qsh": true, "test": true, "true": true, "false": true,
	"sleep": true, "basename": true, "dirname": true, "diff": true, "cmp": true, "file": true,
	"less": true, "more": true, "md5sum": true, "sha256sum": true, "openssl": true, "pax": true,
	"setccsid": true, "attr": true, "liblist": true, "datarea": true, "dataq": true, "Rfile": true,
	"rfile": true, "ulimit": true, "umask": true, "xargs": true, "tee": true, "nohup": true,
	"perl": true, "ruby": true, "php": true, "make": true, "gcc": true, "ps1": true, "hostname": true,
	"netstat": true, "ping": true, "nslookup": true, "dig": true, "ifconfig": true, "mount": true,
	"su": true, "sudo": true, "exit": true, "clear": true, "history": true, "alias": true,
	"unset": true, "source": true, "yum": true, "rpm": true, "pip": true, "pip3": true,
}

// Classify tells what kind of statement text is. Leading SQL comments do not change the
// kind; an explicit prefix (`CL:`, `QSH:`/`SH:`, `SQL:`) is stripped from Text.
func Classify(text string) Classified {
	if m := prefixRe.FindStringSubmatch(text); m != nil {
		rest := strings.TrimSpace(text[len(m[0]):])
		switch strings.ToUpper(m[1]) {
		case "CL":
			return Classified{Kind: CL, Text: rest, Why: "prefix CL:"}
		case "QSH", "SH":
			return Classified{Kind: QSH, Text: rest, Why: "prefix QSH:"}
		case "PASE":
			return Classified{Kind: PASE, Text: rest, Why: "prefix PASE:"}
		}
		return Classified{Kind: SQL, Text: rest, Why: "prefix SQL:"}
	}
	text = strings.TrimSpace(text)
	body := strings.TrimSpace(commentRe.ReplaceAllString(text, ""))
	if body == "" {
		return Classified{Kind: SQL, Text: text, Why: "empty"}
	}
	first := firstWord.FindString(body)
	if first == "" {
		if strings.HasPrefix(body, "(") {
			return Classified{Kind: SQL, Text: text, Why: "SQL (parenthesised query)"}
		}
		return Classified{Kind: SQL, Text: text, Why: "default"}
	}
	upper := strings.ToUpper(first)
	rest := strings.TrimSpace(body[len(first):])

	// PASE: a path under /QOpenSys, or a tool of the open-source packages that Qshell's PATH
	// does not reach. Shell: a path, an assignment, a known command, or a pipeline that is not SQL.
	if strings.HasPrefix(first, "/QOpenSys/") {
		return Classified{Kind: PASE, Text: text, Why: "PASE path " + first}
	}
	if strings.HasPrefix(first, "/") || strings.HasPrefix(first, "./") || strings.HasPrefix(first, "~") || strings.HasPrefix(first, "../") {
		return Classified{Kind: QSH, Text: text, Why: "shell path " + first}
	}
	if paseOnly[first] {
		return Classified{Kind: PASE, Text: text, Why: "PASE command " + first}
	}
	if assignRe.MatchString(body) && !sqlKeywords[upper] {
		return Classified{Kind: QSH, Text: text, Why: "shell assignment"}
	}
	if upper == "CALL" {
		// CL: CALL PGM(...), CALL NAME, CALL LIB/NAME PARM(...). SQL: CALL NAME(...), CALL SCHEMA.NAME(...).
		if callClRe.MatchString(body) {
			return Classified{Kind: CL, Text: text, Why: "CL CALL"}
		}
		return Classified{Kind: SQL, Text: text, Why: "SQL CALL"}
	}
	if sqlKeywords[upper] {
		return Classified{Kind: SQL, Text: text, Why: "SQL keyword " + upper}
	}
	if shellCommands[first] || shellCommands[strings.ToLower(first)] && first == strings.ToLower(first) {
		return Classified{Kind: QSH, Text: text, Why: "shell command " + first}
	}
	// CL: VERB or LIB/VERB, in CL's shape, followed by nothing or by CL parameters.
	if clShapeRe.MatchString(first) && (rest == "" || clParmRe.MatchString(rest)) && !looksLikeSQLTail(rest) {
		return Classified{Kind: CL, Text: text, Why: "CL verb " + upper}
	}
	if strings.Contains(body, "|") && !strings.Contains(strings.ToUpper(body), "||") {
		return Classified{Kind: QSH, Text: text, Why: "pipeline"}
	}
	return Classified{Kind: SQL, Text: text, Why: "default"}
}

// looksLikeSQLTail catches the few shapes a CL parameter list never has.
func looksLikeSQLTail(rest string) bool {
	u := " " + strings.ToUpper(rest) + " "
	for _, w := range []string{" FROM ", " WHERE ", " JOIN ", " SELECT ", " VALUES ", " INTO "} {
		if strings.Contains(u, w) {
			return true
		}
	}
	return strings.Contains(rest, ";") && !strings.Contains(rest, "'")
}

// Verb is the first word of a statement in upper case (the CL command or SQL keyword).
func Verb(text string) string {
	body := strings.TrimSpace(commentRe.ReplaceAllString(strings.TrimSpace(text), ""))
	if i := wordStopRe.FindStringIndex(body); i != nil {
		body = body[:i[0]]
	}
	return strings.ToUpper(body)
}
