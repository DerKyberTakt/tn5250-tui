package runscript

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	hs "github.com/DerKyberTakt/tn5250-tui/internal/hostserver"
	"github.com/DerKyberTakt/tn5250-tui/internal/hostserver/db"
	"github.com/DerKyberTakt/tn5250-tui/internal/hostserver/rmtcmd"
)

// Options describe the host and the user the statements run as.
type Options struct {
	Host string
	// Port 0 asks the port mapper (449) for each server, then the well-known port.
	// DatabasePort and CommandPort, when set, name the ports of the two servers (tests).
	Port                int
	DatabasePort        int
	CommandPort         int
	TLS                 bool
	TrustAnyCertificate bool
	User, Password      string
	// Naming is "sql" (schema.table, the default) or "sys" (library/file).
	Naming        string
	DefaultSchema string
	Timeout       time.Duration
}

func (o Options) dial(port int) hs.DialOptions {
	if port == 0 {
		port = o.Port
	}
	return hs.DialOptions{Host: o.Host, Port: port, TLS: o.TLS, TrustAnyCertificate: o.TrustAnyCertificate,
		Timeout: o.Timeout, User: o.User, Password: o.Password}
}

// Result is what one statement produced.
type Result struct {
	Statement Classified
	// Kind: "rows" (Columns and Rows), "count" (UpdateCount), "none" (ran, nothing to show),
	// "error" (the host refused it; Messages say why).
	Kind        string
	Columns     []string
	Types       []string
	Rows        [][]*string
	More        bool // more rows can be fetched
	UpdateCount int
	Messages    []string
	Elapsed     time.Duration
}

// Session holds the connections one script runs over: the database server (opened on
// the first SQL statement, or to read what a CL or Qshell command printed) and the
// remote command server (opened on the first CL or Qshell statement). One query is open
// at a time; a new statement closes it.
type Session struct {
	opts Options
	mu   sync.Mutex
	db   *db.Client
	// cancelDB is s.db as Cancel sees it. Run holds mu for the whole statement, so Cancel
	// cannot take mu: it reads this copy under its own lock.
	cancelMu sync.Mutex
	cancelDB *db.Client
	aux      *db.Client // second database connection: reads spooled and stream files while a query is open
	cmd      *rmtcmd.Client
	rows     *db.Rows
	// paseCCSID is true once the server job was given CCSID 37 for PASE.
	paseCCSID bool
}

// New prepares a session; nothing connects until Run.
func New(o Options) *Session {
	if o.Timeout <= 0 {
		o.Timeout = 20 * time.Second
	}
	return &Session{opts: o}
}

// Close drops every connection.
func (s *Session) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dropAll()
}

// dropAll closes every host-server connection; the next statement opens fresh ones. The
// caller holds s.mu.
func (s *Session) dropAll() {
	if s.rows != nil {
		_ = s.rows.Close()
		s.rows = nil
	}
	for _, c := range []*db.Client{s.db, s.aux} {
		if c != nil {
			_ = c.Close()
		}
	}
	s.db, s.aux = nil, nil
	s.publishDB()
	if s.cmd != nil {
		_ = s.cmd.Close()
		s.cmd = nil
	}
}

// Cancel interrupts the running SQL statement (a side connection asks the host). It does not
// wait for the statement: Run holds the session lock until the host answers.
func (s *Session) Cancel() error {
	s.cancelMu.Lock()
	c := s.cancelDB
	s.cancelMu.Unlock()
	if c == nil {
		return errors.New("no SQL statement is running")
	}
	return c.Cancel()
}

// Job names the remote command server job once it is open ("" before).
func (s *Session) Job() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cmd == nil {
		return ""
	}
	return s.cmd.JobName()
}

func (s *Session) database() (*db.Client, error) {
	if s.db != nil {
		return s.db, nil
	}
	c, err := db.Open(db.Options{DialOptions: s.opts.dial(s.opts.DatabasePort), Naming: s.opts.Naming, DefaultSchema: s.opts.DefaultSchema})
	if err != nil {
		return nil, fmt.Errorf("database server: %w", err)
	}
	s.db = c
	s.publishDB()
	return c, nil
}

func (s *Session) auxDB() (*db.Client, error) {
	if s.aux != nil {
		return s.aux, nil
	}
	c, err := db.Open(db.Options{DialOptions: s.opts.dial(s.opts.DatabasePort), Naming: "sql"})
	if err != nil {
		return nil, fmt.Errorf("database server: %w", err)
	}
	s.aux = c
	return c, nil
}

func (s *Session) command() (*rmtcmd.Client, error) {
	if s.cmd != nil {
		return s.cmd, nil
	}
	c, err := rmtcmd.Open(s.opts.dial(s.opts.CommandPort))
	if err != nil {
		return nil, fmt.Errorf("remote command server: %w", err)
	}
	s.cmd = c
	return c, nil
}

// Run runs one statement. block is the number of rows a query returns at first; Fetch
// brings the next ones. A refused statement is a Result of Kind "error", not an error:
// an error means the connection itself failed (it is dropped and reopened next time).
func (s *Session) Run(st Classified, block int) (Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	start := time.Now()
	had := s.db != nil || s.cmd != nil
	out, err := s.runOnce(st, block)
	if err != nil && had && connLost(err) {
		// A connection that was open died under the statement (the server job ended, the
		// network dropped, an idle socket was closed): every connection is dropped and the
		// statement runs once more on fresh ones, as a person would do by hand.
		s.dropAll()
		out, err = s.runOnce(st, block)
		if err == nil {
			out.Messages = append(out.Messages, "reconnected: the host-server connection had been lost")
		}
	}
	out.Statement = st
	out.Elapsed = time.Since(start)
	return out, err
}

func (s *Session) runOnce(st Classified, block int) (Result, error) {
	switch st.Kind {
	case CL:
		return s.runCL(st.Text)
	case QSH:
		return s.runQSH(st.Text)
	case PASE:
		return s.runPASE(st.Text)
	default:
		return s.runSQL(st.Text, block)
	}
}

// Fetch brings the next block of the open query.
func (s *Session) Fetch(block int) (Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	start := time.Now()
	if s.rows == nil {
		return Result{}, errors.New("no query is open; run it again")
	}
	data, err := s.rows.Next()
	if err != nil {
		var e *db.Error
		if errors.As(err, &e) {
			s.rows = nil
			return Result{Kind: "error", Messages: []string{e.Error()}, Elapsed: time.Since(start)}, nil
		}
		s.dropDB()
		return Result{}, err
	}
	out := s.page(data)
	out.Elapsed = time.Since(start)
	return out, nil
}

func (s *Session) dropDB() {
	if s.db != nil {
		_ = s.db.Close()
	}
	s.db, s.rows = nil, nil
	s.publishDB()
}

// publishDB makes the current database connection visible to Cancel (the caller holds mu).
func (s *Session) publishDB() {
	s.cancelMu.Lock()
	s.cancelDB = s.db
	s.cancelMu.Unlock()
}

func (s *Session) page(data [][]*string) Result {
	out := Result{Kind: "rows", Rows: data, More: !s.rows.Done}
	for _, c := range s.rows.Columns {
		out.Columns = append(out.Columns, c.Name)
		out.Types = append(out.Types, c.Type)
	}
	for _, m := range s.rows.Messages {
		out.Messages = append(out.Messages, messageText(m))
	}
	if s.rows.Done {
		_ = s.rows.Close()
		s.rows = nil
	}
	return out
}

func messageText(m db.Message) string {
	switch {
	case m.ID != "" && m.Text != "":
		return m.ID + ": " + m.Text
	case m.Text != "":
		return m.Text
	}
	return fmt.Sprintf("SQLCODE %d (SQLSTATE %s)", m.SQLCode, m.SQLState)
}

var (
	queryStart = []string{"SELECT", "VALUES", "WITH"}
	dmlStart   = []string{"INSERT", "UPDATE", "DELETE", "MERGE"}
)

func startsWithAny(text string, words []string) bool {
	v := Verb(text)
	v = strings.TrimLeft(v, "(")
	for _, w := range words {
		if v == w {
			return true
		}
	}
	return false
}

func (s *Session) runSQL(text string, block int) (Result, error) {
	c, err := s.database()
	if err != nil {
		return Result{}, err
	}
	if s.rows != nil {
		_ = s.rows.Close()
		s.rows = nil
	}
	text = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(text), ";"))
	if startsWithAny(text, queryStart) {
		rows, data, err := c.Query(text, block)
		if err != nil {
			return s.sqlFailure(err)
		}
		s.rows = rows
		return s.page(data), nil
	}
	res, err := c.Exec(text)
	if err != nil {
		return s.sqlFailure(err)
	}
	out := Result{Kind: "none", UpdateCount: res.UpdateCount}
	if startsWithAny(text, dmlStart) {
		out.Kind = "count"
	}
	for _, m := range res.Messages {
		out.Messages = append(out.Messages, messageText(m))
	}
	return out, nil
}

func (s *Session) sqlFailure(err error) (Result, error) {
	var e *db.Error
	if errors.As(err, &e) {
		return Result{Kind: "error", Messages: []string{e.Error()}}, nil
	}
	s.dropDB()
	return Result{}, err
}

func (s *Session) runCL(text string) (Result, error) {
	c, err := s.command()
	if err != nil {
		return Result{}, err
	}
	res, err := c.Run(text)
	if err != nil {
		_ = c.Close()
		s.cmd = nil
		return Result{}, fmt.Errorf("remote command server: %w", err)
	}
	out := Result{Kind: "none"}
	for _, m := range res.Messages {
		out.Messages = append(out.Messages, m.ID+": "+m.Text)
	}
	if res.Failed() {
		out.Kind = "error"
		if len(out.Messages) == 0 {
			out.Messages = []string{res.Err().Error()}
		}
	}
	// A display command has no screen in a server job: its output is a spooled file.
	rows, notes := s.captureSpool(c)
	if len(rows) > 0 {
		out.Columns, out.Types, out.Rows = []string{"OUTPUT"}, []string{"VARCHAR"}, rows
		if out.Kind == "none" {
			out.Kind = "rows"
		}
	}
	out.Messages = append(out.Messages, notes...)
	return out, nil
}

// all runs a query on the second database connection and returns every row.
func (s *Session) all(sql string) ([][]*string, error) {
	c, err := s.auxDB()
	if err != nil {
		return nil, err
	}
	rows, data, err := c.Query(sql, 1000)
	if err != nil {
		var e *db.Error
		if !errors.As(err, &e) {
			_ = c.Close()
			s.aux = nil
		}
		return nil, err
	}
	for !rows.Done {
		d, err := rows.Next()
		if err != nil {
			return nil, err
		}
		data = append(data, d...)
	}
	_ = rows.Close()
	return data, nil
}

// captureSpool reads and deletes the spooled files of the command server job (there are
// none but the ones the command just made) through SYSTOOLS.SPOOLED_FILE_DATA.
func (s *Session) captureSpool(c *rmtcmd.Client) (rows [][]*string, notes []string) {
	parts := strings.Fields(c.JobName())
	if len(parts) != 3 {
		return nil, nil
	}
	job := parts[2] + "/" + parts[1] + "/" + parts[0]
	list, err := s.all("SELECT SPOOLED_FILE_NAME, FILE_NUMBER FROM TABLE(QSYS2.SPOOLED_FILE_INFO(JOB_NAME => '" + job + "')) ORDER BY FILE_NUMBER")
	if err != nil {
		list, err = s.all("SELECT SPOOLED_FILE_NAME, FILE_NUMBER FROM QSYS2.OUTPUT_QUEUE_ENTRIES_BASIC WHERE JOB_NAME = '" + job + "' ORDER BY FILE_NUMBER")
	}
	if err != nil {
		return nil, []string{"the command's spooled output could not be listed: " + err.Error()}
	}
	for _, f := range list {
		if len(f) < 2 || f[0] == nil || f[1] == nil {
			continue
		}
		name, num := strings.TrimSpace(*f[0]), strings.TrimSpace(*f[1])
		data, err := s.all(fmt.Sprintf("SELECT SPOOLED_DATA FROM TABLE(SYSTOOLS.SPOOLED_FILE_DATA(JOB_NAME => '%s', SPOOLED_FILE_NAME => '%s', SPOOLED_FILE_NUMBER => %s)) ORDER BY ORDINAL_POSITION", job, name, num))
		if err != nil {
			notes = append(notes, fmt.Sprintf("spooled file %s (%s) could not be read: %s", name, num, err.Error()))
			continue
		}
		if len(list) > 1 {
			hdr := fmt.Sprintf("---- %s (%s)", name, num)
			rows = append(rows, []*string{&hdr})
		}
		rows = append(rows, data...)
		where := "shown here and deleted from the server job"
		if res, err := c.Run(fmt.Sprintf("DLTSPLF FILE(%s) JOB(%s) SPLNBR(%s)", name, job, num)); err != nil || res.Failed() {
			where = "shown here; it is still in the server job's output queue"
		}
		notes = append(notes, fmt.Sprintf("Output: spooled file %s, %d lines, %s.", name, len(data), where))
	}
	return rows, notes
}

// quote wraps text in CL apostrophes, doubling the ones inside.
func quote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// tempName is a stream file name for a Qshell command's output.
func tempName() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return "/tmp/tn5250-tui-" + hex.EncodeToString(b[:]) + ".txt"
}

// runQSH runs a Qshell line with its output redirected to a stream file, reads the file
// back through QSYS2.IFS_READ on the database server and removes it.
// paseExitPrefix starts the last line a PASE run writes: the command's exit status.
const paseExitPrefix = "tn5250-exit="

// paseCommand runs a PASE line through QP2SHELL: /QOpenSys/usr/bin/sh -c with the open-source
// packages on PATH, output and errors to stmf, then the exit status as a last line (QP2SHELL
// itself does not fail when the command does).
func paseCommand(text, stmf string) string {
	script := "PATH=/QOpenSys/pkgs/bin:/QOpenSys/usr/bin:/usr/bin:$PATH; export PATH; { " + strings.TrimSpace(text) + " ; } > " + stmf + " 2>&1; echo \"" + paseExitPrefix + "$?\" >> " + stmf
	return "CALL PGM(QP2SHELL) PARM('/QOpenSys/usr/bin/sh' '-c' " + quote(script) + ")"
}

// runPASE runs a PASE command line with its output redirected to a stream file, reads the
// file back and returns the lines as rows; a non-zero exit status is an error result. PASE
// refuses a job whose CCSID is 65535: when the first run says so, the server job is given
// CCSID 37 and the line runs again.
func (s *Session) runPASE(text string) (Result, error) {
	c, err := s.command()
	if err != nil {
		return Result{}, err
	}
	stmf := tempName()
	res, err := c.Run(paseCommand(text, stmf))
	if err != nil {
		_ = c.Close()
		s.cmd = nil
		return Result{}, fmt.Errorf("remote command server: %w", err)
	}
	if res.Failed() && !s.paseCCSID {
		for _, m := range res.Messages {
			if strings.Contains(m.Text, "65535") {
				if r2, err2 := c.Run("CHGJOB CCSID(37)"); err2 == nil && !r2.Failed() {
					s.paseCCSID = true
					if res, err = c.Run(paseCommand(text, stmf)); err != nil {
						_ = c.Close()
						s.cmd = nil
						return Result{}, fmt.Errorf("remote command server: %w", err)
					}
				}
				break
			}
		}
	}
	out := Result{Kind: "none"}
	for _, m := range res.Messages {
		out.Messages = append(out.Messages, m.ID+": "+m.Text)
	}
	lines, err := s.all("SELECT LINE FROM TABLE(QSYS2.IFS_READ(PATH_NAME => '" + stmf + "', END_OF_LINE => 'ANY')) ORDER BY LINE_NUMBER")
	if err != nil {
		out.Messages = append(out.Messages, "the command's output could not be read: "+err.Error())
	} else {
		exit := ""
		if n := len(lines); n > 0 && len(lines[n-1]) > 0 && lines[n-1][0] != nil && strings.HasPrefix(*lines[n-1][0], paseExitPrefix) {
			exit = strings.TrimSpace(strings.TrimPrefix(*lines[n-1][0], paseExitPrefix))
			lines = lines[:n-1]
		}
		if len(lines) > 0 {
			out.Kind = "rows"
			out.Columns, out.Types, out.Rows = []string{"OUTPUT"}, []string{"VARCHAR"}, lines
		}
		if exit != "" && exit != "0" {
			out.Kind = "error"
			out.Messages = append(out.Messages, "the command ended with exit status "+exit)
		}
	}
	_, _ = c.Run("RMVLNK OBJLNK(" + quote(stmf) + ")")
	if res.Failed() {
		out.Kind = "error"
		if len(out.Messages) == 0 {
			out.Messages = []string{res.Err().Error()}
		}
	}
	return out, nil
}

func (s *Session) runQSH(text string) (Result, error) {
	c, err := s.command()
	if err != nil {
		return Result{}, err
	}
	stmf := tempName()
	res, err := c.Run("QSH CMD(" + quote(strings.TrimSpace(text)+" > "+stmf+" 2>&1") + ")")
	if err != nil {
		_ = c.Close()
		s.cmd = nil
		return Result{}, fmt.Errorf("remote command server: %w", err)
	}
	out := Result{Kind: "none"}
	for _, m := range res.Messages {
		if m.ID == "QSH0005" { // "Command ended normally with exit status N": the status is the point
			out.Messages = append(out.Messages, m.Text)
			continue
		}
		out.Messages = append(out.Messages, m.ID+": "+m.Text)
	}
	lines, err := s.all("SELECT LINE FROM TABLE(QSYS2.IFS_READ(PATH_NAME => '" + stmf + "', END_OF_LINE => 'ANY')) ORDER BY LINE_NUMBER")
	if err != nil {
		out.Messages = append(out.Messages, "the command's output could not be read: "+err.Error())
	} else if len(lines) > 0 {
		out.Kind = "rows"
		out.Columns, out.Types, out.Rows = []string{"OUTPUT"}, []string{"VARCHAR"}, lines
	}
	_, _ = c.Run("RMVLNK OBJLNK(" + quote(stmf) + ")")
	if res.Failed() {
		out.Kind = "error"
		if len(out.Messages) == 0 {
			out.Messages = []string{res.Err().Error()}
		}
	}
	return out, nil
}
