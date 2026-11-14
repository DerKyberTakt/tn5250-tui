package agent

import (
	"context"
	"io"
	"time"

	"github.com/DerKyberTakt/tn5250-tui/internal/client"
	"github.com/DerKyberTakt/tn5250-tui/internal/sites"
)

// Version is the server version reported to the MCP client. The caller may set it from its
// release build (agent.Version = version).
var Version = "dev"

// ServerName is the name the server reports to the MCP client.
const ServerName = "tn5250-tui"

// Defaults of the manager.
const (
	// DefaultMaxSessions is the most live sessions at once when Config.MaxSessions is 0.
	DefaultMaxSessions = 4
	// DefaultTimeout is how long a tool waits for the host when the caller gives no timeout_ms.
	DefaultTimeout = 15 * time.Second
	// MaxTimeout caps timeout_ms.
	MaxTimeout = 120 * time.Second
	// SettleQuiet is how long the screen must stay unchanged, with the keyboard unlocked,
	// before the host counts as settled.
	SettleQuiet = 120 * time.Millisecond
	// TraceLines is the size of the per-session trace ring.
	TraceLines = 200
)

// Config configures the manager and the server. Environment fallbacks (flags, variables) are
// the caller's job; empty fields mean "not set".
type Config struct {
	// Sites is the saved-sites file; nil means no saved sites.
	Sites *sites.File
	// Site is the default site name, used by connect when the call names neither a site nor a
	// host and Host is empty.
	Site string
	// Host, Port, TLS, TrustAny, User, Password, ScreenSize, CodePage and DeviceName are the
	// defaults for connect where neither the call nor the site gives a value.
	Host       string
	Port       int
	TLS        bool
	TrustAny   bool
	User       string
	Password   string
	ScreenSize string
	CodePage   int
	DeviceName string
	// AuditFile, when set, receives the audit log (JSON lines, appended) as well as stderr.
	AuditFile string
	// AuditWriter replaces stderr as the first audit destination (tests); nil = os.Stderr.
	AuditWriter io.Writer
	// ReadOnly refuses typing that starts a guarded command (see guardedVerbs).
	ReadOnly bool
	// MaxSessions is the most live sessions at once; 0 = DefaultMaxSessions.
	MaxSessions int
	// Dialer opens the connection; nil dials TCP/TLS with internal/client. Tests replace it.
	Dialer Dialer
}

// Link is the write side of a connection.
type Link interface {
	WriteTraced(b, forTrace []byte) error
	Close() error
}

// Dialer opens a connection to the host. The options carry the OnData and OnClosed callbacks
// the manager needs.
type Dialer interface {
	Dial(ctx context.Context, o client.Options) (Link, error)
}

type netDialer struct{}

func (netDialer) Dial(ctx context.Context, o client.Options) (Link, error) {
	c, err := client.Dial(ctx, o)
	if err != nil {
		return nil, err
	}
	return c, nil
}
